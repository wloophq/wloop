package cli

import (
	"cmp"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/brand"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/termfmt"
)

var needsUsage = "usage: " + brand.Command + " needs [<query>] [--kind <kind>]... [--open] [--color <when>] | needs show <ref> [--color <when>] | " +
	"needs approve <ref> [--text <markdown> --rev <n>] [--payload <json> --rev <n>] [--confirm] [--yes] | needs answer <ref> [<option>] [--text <answer>] [--yes]"

// The kinds of a queue item's primary action the CLI runs.
const (
	primaryGate = "gate"
	primaryVerb = "verb"
)

func runNeeds(ctx context.Context, e *Env, args []string) error {
	run := needsList
	if len(args) > 0 {
		if sub, ok := map[string]func(context.Context, *Env, *client.Client, []string) error{
			"show": needsShow, "approve": needsApprove, "answer": needsAnswer,
		}[args[0]]; ok {
			run, args = sub, args[1:]
		}
	}
	c, err := e.daemonClient(ctx)
	if err != nil {
		return err
	}
	return run(ctx, e, c, args)
}

// itemRef is how the CLI names item it: its gate's id, else, for a situation of one of its task's units, which
// no gate decides, the item's kind and the unit, since a unit makes at most one decision of each kind.
func itemRef(it api.QueueItem) string {
	switch {
	case it.Gate != nil:
		return strconv.FormatInt(it.Gate.ID, 10)
	case it.Situation != nil:
		return it.Kind + "@" + it.Situation.Unit
	}
	return ""
}

func needsList(ctx context.Context, e *Env, c *client.Client, args []string) error {
	fs := flag.NewFlagSet("needs", flag.ContinueOnError)
	fs.SetOutput(e.Err)
	var kinds listFlag
	fs.Var(&kinds, "kind", "keep the items of this decision kind; repeat it for more")
	open := fs.Bool("open", false, "open the cockpit on the same queue")
	var color termfmt.Mode
	fs.Var(&color, "color", "lay the list out for a terminal: auto, always or never")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return usageError{usage: needsUsage}
	}
	if *open && len(kinds) > 1 {
		return usageError{usage: needsUsage, why: "the cockpit narrows the queue by one kind: --open takes at most one --kind"}
	}
	query := strings.Join(pos, " ")
	q, err := c.QueueAll(ctx, query, kinds...)
	if err != nil {
		return err
	}
	if *open {
		return e.openCockpit(ctx, c, needsRoute(q.View, query, kinds))
	}
	t := e.term(color)
	switch {
	case !t.TTY:
		writeNeedsPlain(e.Out, q.Items)
	case len(q.Items) == 0:
		fmt.Fprintln(e.Out, q.Empty)
	default:
		writeNeeds(e.Out, t, q, e.Now())
	}
	return nil
}

// needsRoute is the cockpit's route to view, the queue, narrowed as query and kinds narrow it.
func needsRoute(view, query string, kinds []string) string {
	route := "view=" + url.QueryEscape(view)
	if len(kinds) == 1 {
		route += "&kind=" + url.QueryEscape(kinds[0])
	}
	if query != "" {
		route += "&q=" + url.QueryEscape(query)
	}
	return route
}

// needsItem is the item of the queue ref names.
func needsItem(ctx context.Context, c *client.Client, ref string) (api.QueueItem, error) {
	q, err := c.QueueAll(ctx, "")
	if err != nil {
		return api.QueueItem{}, err
	}
	for _, it := range q.Items {
		if itemRef(it) == ref {
			return it, nil
		}
	}
	return api.QueueItem{}, fmt.Errorf("%s holds no item %s; `%s needs` lists them", cmp.Or(q.Title, "the queue"), ref, brand.Command)
}

// refArg is the one item reference args hold, then the rest.
func refArg(args []string, most int) (string, []string, error) {
	if len(args) == 0 || len(args) > most {
		return "", nil, usageError{usage: needsUsage}
	}
	return args[0], args[1:], nil
}

// gateDraft is the draft draft gate g stands for.
func gateDraft(g api.GateView) (api.Draft, bool) {
	var d api.Draft
	if g.Kind != api.GateKindDraft || json.Unmarshal(g.Subject, &d) != nil || d.ID == 0 {
		return api.Draft{}, false
	}
	return d, true
}

// gateMessage is the message gate g asks.
func gateMessage(g api.GateView) (api.Message, bool) {
	var m api.Message
	if g.Kind == api.GateKindDraft || json.Unmarshal(g.Subject, &m) != nil || m.ID == 0 {
		return api.Message{}, false
	}
	return m, true
}

// answerIntent is the intent of g that takes an answer.
func answerIntent(g api.GateView) (api.IntentView, bool) {
	i := slices.IndexFunc(g.Intents, func(in api.IntentView) bool { return in.Answer })
	if i < 0 {
		return api.IntentView{}, false
	}
	return g.Intents[i], true
}

// choice is an answer an item's question offers, with the held draft picking it approves.
type choice struct {
	id, label, consequence string
	recommended            bool
	draft                  *api.Draft
}

// choicesOf are the answers it's question offers: the queue's options of a question that pins drafts, else its
// message's yes and no and its options.
func choicesOf(it api.QueueItem) []choice {
	var out []choice
	if len(it.Options) > 0 {
		for _, o := range it.Options {
			ch := choice{id: o.ID, label: o.Label, recommended: o.Recommended}
			if o.Draft != nil {
				if d, ok := gateDraft(*o.Draft); ok {
					ch.draft = &d
				}
			}
			out = append(out, ch)
		}
		return out
	}
	if it.Gate == nil {
		return nil
	}
	m, ok := gateMessage(*it.Gate)
	if !ok {
		return nil
	}
	if m.Confirm {
		out = append(out, choice{id: "yes", label: "yes", recommended: m.Recommend == "yes"}, choice{id: "no", label: "no", recommended: m.Recommend == "no"})
	}
	for _, o := range m.Options {
		out = append(out, choice{id: o.ID, label: o.Label, consequence: o.Consequence, recommended: o.ID == m.Recommend})
	}
	return out
}

// itemAsks is what it asks of the user, in one line: its draft's gist, its question, its gate's words, or its
// unit's situation.
func itemAsks(it api.QueueItem) string {
	switch {
	case it.Situation != nil:
		return it.Situation.Label
	case it.Gate == nil:
		return it.Task.Title
	}
	if d, ok := gateDraft(*it.Gate); ok {
		return gist(d)
	}
	if m, ok := gateMessage(*it.Gate); ok && m.Text != "" {
		return oneLine(m.Text)
	}
	if it.Gate.Text != "" {
		return oneLine(it.Gate.Text)
	}
	if p := it.Gate.Plan; p != nil {
		return cmp.Or(p.Summary, p.Workflow)
	}
	return it.Task.Title
}

// writeNeedsPlain writes one tab-separated line per item for scripts: its reference, kind, task, the task's
// title, when the decision began, its primary action and what it asks.
func writeNeedsPlain(w io.Writer, its []api.QueueItem) {
	for _, it := range its {
		fields := []string{itemRef(it), it.Kind, it.Task.ID, it.Task.Title, it.Since.UTC().Format(time.RFC3339), it.Primary.Label, itemAsks(it)}
		for i, f := range fields {
			fields[i] = strings.ReplaceAll(oneLine(f), "\t", " ")
		}
		fmt.Fprintln(w, strings.Join(fields, "\t"))
	}
}

// writeNeeds lays the queue out for a person, in its order: each item's reference, kind, task, age and primary
// action, and under it what it asks.
func writeNeeds(w io.Writer, t termfmt.Term, q api.Queue, now time.Time) {
	fmt.Fprintln(w, t.Bold(q.Title)+"  "+t.Dim(fmt.Sprintf("%d waiting on you", len(q.Items))))
	width := reportWidth(t) - 2
	cols := []termfmt.Column{{}, {}, {Flex: true}, {Right: true}, {}}
	var rows [][]termfmt.Cell
	for _, it := range q.Items {
		rows = append(rows, []termfmt.Cell{
			{Text: itemRef(it), Style: t.Cyan}, {Text: cmp.Or(it.Word, it.Kind), Style: t.Dim}, {Text: it.Task.Title},
			{Text: shortAge(now.Sub(it.Since), it.Since.IsZero()), Style: t.Dim}, {Text: it.Primary.Label, Style: t.Bold},
		})
	}
	for i, l := range t.Columns(width, 2, cols, rows) {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "  "+l)
		for _, a := range termfmt.Wrap(itemAsks(q.Items[i]), width-4, width-4) {
			fmt.Fprintln(w, "    "+a)
		}
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, t.Dim("Next: "+brand.Command+" needs show <ref> · approve <ref> · answer <ref> <option>"))
}

func needsShow(ctx context.Context, e *Env, c *client.Client, args []string) error {
	fs := flag.NewFlagSet("needs show", flag.ContinueOnError)
	fs.SetOutput(e.Err)
	var color termfmt.Mode
	fs.Var(&color, "color", "color the item: auto, always or never")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return usageError{usage: needsUsage}
	}
	ref, _, err := refArg(pos, 1)
	if err != nil {
		return err
	}
	it, err := needsItem(ctx, c, ref)
	if err != nil {
		return err
	}
	writeNeedsItem(e.Out, e.term(color), it, e.Now())
	return nil
}

// writeNeedsItem lays one item out: what it is on, what it asks, the answers it offers and the commands it takes.
func writeNeedsItem(w io.Writer, t termfmt.Term, it api.QueueItem, now time.Time) {
	ref := itemRef(it)
	fmt.Fprintln(w, t.Cyan(ref)+"  "+t.Bold(cmp.Or(it.Word, it.Kind))+"  "+t.Dim(shortAge(now.Sub(it.Since), it.Since.IsZero())+" ago"))
	fmt.Fprintln(w, t.Bold(it.Task.Title)+"  "+t.Dim(it.Task.ID))
	fmt.Fprintln(w)
	if it.Gate != nil {
		if d, ok := gateDraft(*it.Gate); ok {
			writeDraftView(w, t, d, now)
			if it.Primary.Kind == primaryVerb {
				fmt.Fprintln(w, t.Dim(fmt.Sprintf("%s needs approve %s", brand.Command, ref)))
			}
			return
		}
	}
	width := reportWidth(t)
	for _, l := range termfmt.Wrap(itemAsks(it), width-2, width-2) {
		fmt.Fprintln(w, "  "+l)
	}
	if s := it.Situation; s != nil {
		fmt.Fprintln(w, "  "+t.Dim(s.Unit))
	}
	if it.Gate != nil && it.Gate.Plan != nil && it.Gate.Plan.Reason != "" {
		fmt.Fprintln(w, "  "+t.Dim(it.Gate.Plan.Reason))
	}
	if it.Gate != nil {
		for _, u := range it.Gate.Units {
			fmt.Fprintln(w, "  "+cmp.Or(u.Label, u.Title)+" "+t.Dim(u.Title))
		}
	}
	chs := choicesOf(it)
	if len(chs) > 0 {
		fmt.Fprintln(w)
	}
	for _, ch := range chs {
		line := "  " + t.Cyan(ch.id) + "  " + ch.label
		if ch.recommended {
			line += " " + t.Dim("(recommended)")
		}
		fmt.Fprintln(w, line)
		if ch.consequence != "" {
			fmt.Fprintln(w, "     "+t.Dim(ch.consequence))
		}
		if d := ch.draft; d != nil {
			fmt.Fprintln(w, "     "+t.Dim(fmt.Sprintf("posts draft %d on %s:", d.ID, draftLabel(*d)))+" "+gist(*d))
			if d.Stale && len(d.Notes) > 0 {
				fmt.Fprintln(w, "     "+t.Red(d.Notes[0]))
			}
		}
	}
	fmt.Fprintln(w)
	for _, l := range needsCommands(it) {
		fmt.Fprintln(w, t.Dim(l))
	}
}

// needsCommands are the commands it takes: approving its gate with its primary, an answer per choice of a
// question, or opening the task of a situation no gate decides.
func needsCommands(it api.QueueItem) []string {
	ref := itemRef(it)
	if it.Gate == nil {
		return []string{fmt.Sprintf("%s open '#task=%s'", brand.Command, it.Task.ID)}
	}
	var out []string
	if !intentAnswers(*it.Gate, it.Primary.ID) {
		out = append(out, fmt.Sprintf("%s needs approve %s", brand.Command, ref))
	}
	if _, ok := answerIntent(*it.Gate); !ok {
		return out
	}
	chs := choicesOf(it)
	if len(chs) == 0 {
		return append(out, fmt.Sprintf("%s needs answer %s --text <answer>", brand.Command, ref))
	}
	for _, ch := range chs {
		out = append(out, fmt.Sprintf("%s needs answer %s %s", brand.Command, ref, ch.id))
	}
	return out
}

// intentAnswers says whether intent of g takes an answer.
func intentAnswers(g api.GateView, intent string) bool {
	in, ok := answerIntent(g)
	return ok && in.ID == intent
}

func needsApprove(ctx context.Context, e *Env, c *client.Client, args []string) error {
	fs := flag.NewFlagSet("needs approve", flag.ContinueOnError)
	fs.SetOutput(e.Err)
	text := fs.String("text", "", "a draft's: the text to post instead of the draft's")
	payload := fs.String("payload", "", "a draft's: the payload to post instead of the draft's, whole, as JSON, such as a review's {\"event\":\"COMMENT\"}")
	rev := fs.Int64("rev", -1, "with --text or --payload: the rev of the draft you read, as needs show prints it")
	confirm := fs.Bool("confirm", false, "a failed delivery's: post again a draft that may have been posted")
	yes := fs.Bool("yes", false, "a draft's: approve without asking, after showing it")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	ref, _, err := refArg(pos, 1)
	if err != nil {
		return err
	}
	it, err := needsItem(ctx, c, ref)
	if err != nil {
		return err
	}
	switch {
	case it.Gate == nil:
		return fmt.Errorf("%s is a situation of %s, which no gate decides: %s it from its task, `%s open '#task=%s'`",
			ref, cmp.Or(situationUnit(it), it.Task.Title), cmp.Or(it.Primary.Label, it.Primary.ID), brand.Command, it.Task.ID)
	case it.Primary.Kind == primaryVerb:
		d, ok := gateDraft(*it.Gate)
		if !ok {
			return fmt.Errorf("%s retries a delivery whose draft its gate does not name", ref)
		}
		return e.retryDraft(ctx, c, "needs approve", d.ID, *confirm, *yes)
	case it.Primary.Kind != primaryGate:
		return fmt.Errorf("%s waits on %s, which no gate decides", ref, cmp.Or(it.Primary.Label, it.Primary.Kind))
	case intentAnswers(*it.Gate, it.Primary.ID):
		return usageError{usage: needsUsage, why: fmt.Sprintf("%s asks a question: answer it with `%s needs answer %s <option>`", ref, brand.Command, ref)}
	}
	d, isDraft := gateDraft(*it.Gate)
	if !isDraft {
		if flagSet(fs, "text") || flagSet(fs, "payload") {
			return usageError{usage: needsUsage, why: "--text and --payload change a draft, and " + ref + " is no draft"}
		}
		if err := e.namedAgent("needs approve"); err != nil {
			return err
		}
		if _, err := c.ResolveGate(ctx, it.Gate.ID, it.Primary.ID, api.TaskRequest{Source: string(dturn.Terminal)}); err != nil {
			return err
		}
		fmt.Fprintf(e.Out, "%s: %s\n", cmp.Or(it.Primary.Label, it.Primary.ID), it.Task.Title)
		return nil
	}
	shown, seen := d.Text, d.Rev
	decision := map[string]string{}
	if flagSet(fs, "text") || flagSet(fs, "payload") {
		if *rev < 0 {
			return usageError{usage: needsUsage, why: "approve --text or --payload takes --rev, the rev of the draft you read (needs show prints it)"}
		}
		seen = *rev
	}
	if flagSet(fs, "text") {
		shown = *text
	}
	if flagSet(fs, "payload") {
		if !json.Valid([]byte(*payload)) {
			return usageError{usage: needsUsage, why: "approve --payload takes the draft's whole payload as JSON"}
		}
		decision["payload"] = *payload
		d.Fields = nil
	}
	if err := refuseStale(d); err != nil {
		return err
	}
	if err := e.userAtTerminal("needs approve", *yes); err != nil {
		return err
	}
	printDraft(e.Out, d, shown)
	if flagSet(fs, "payload") {
		fmt.Fprintf(e.Out, "payload:  %s\n", *payload)
	}
	if !*yes && !e.confirmed("Approve and post this?") {
		fmt.Fprintln(e.Out, "Not approved.")
		return exitCode(1)
	}
	decision["text"], decision["rev"] = shown, strconv.FormatInt(seen, 10)
	if _, err := c.ResolveGate(ctx, it.Gate.ID, it.Primary.ID, api.TaskRequest{Args: decision, Source: string(dturn.Terminal)}); err != nil {
		return err
	}
	return e.reportQueued(ctx, c, d.ID)
}

// reportQueued says where approved draft id stands.
func (e *Env) reportQueued(ctx context.Context, c *client.Client, id int64) error {
	got, err := c.Draft(ctx, id)
	if err != nil {
		return err
	}
	fmt.Fprintf(e.Out, "draft %d %s; it posts on the daemon's next send\n", got.ID, got.State)
	return nil
}

// refuseStale refuses draft d when its unit moved past what it was written against, as the daemon says why.
func refuseStale(d api.Draft) error {
	if !d.Stale {
		return nil
	}
	why := "its unit moved past what it was written against"
	if len(d.Notes) > 0 {
		why = d.Notes[0]
	}
	return fmt.Errorf("draft %d is stale, so it is not approved: %s", d.ID, why)
}

func situationUnit(it api.QueueItem) string {
	if it.Situation == nil {
		return ""
	}
	return it.Situation.Unit
}

func needsAnswer(ctx context.Context, e *Env, c *client.Client, args []string) error {
	fs := flag.NewFlagSet("needs answer", flag.ContinueOnError)
	fs.SetOutput(e.Err)
	text := fs.String("text", "", "the answer in your words, after the option when one is given")
	yes := fs.Bool("yes", false, "with an option that posts a draft: post without asking, after showing it")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	ref, rest, err := refArg(pos, 2)
	if err != nil {
		return err
	}
	it, err := needsItem(ctx, c, ref)
	if err != nil {
		return err
	}
	if it.Gate == nil {
		return fmt.Errorf("%s asks no question: `%s needs show %s` says what it waits on", ref, brand.Command, ref)
	}
	intent, ok := answerIntent(*it.Gate)
	if !ok {
		return fmt.Errorf("%s asks no question: approve it with `%s needs approve %s`", ref, brand.Command, ref)
	}
	option := ""
	if len(rest) == 1 {
		option = rest[0]
	}
	chs := choicesOf(it)
	var picked *choice
	if option != "" {
		i := slices.IndexFunc(chs, func(ch choice) bool { return ch.id == option })
		if i < 0 {
			ids := make([]string, len(chs))
			for j, ch := range chs {
				ids[j] = ch.id
			}
			return usageError{usage: needsUsage, why: fmt.Sprintf("%s offers no option %q; its options are %s", ref, option, cmp.Or(strings.Join(ids, ", "), "none: answer with --text"))}
		}
		picked = &chs[i]
	}
	if option == "" && strings.TrimSpace(*text) == "" {
		return usageError{usage: needsUsage, why: "answer takes one of the question's options, or --text"}
	}
	answer := strings.Join(slices.DeleteFunc([]string{option, strings.TrimSpace(*text)}, func(s string) bool { return s == "" }), "\n")
	decision := map[string]string{"answer": answer}
	if picked != nil && picked.draft != nil {
		d := *picked.draft
		if err := refuseStale(d); err != nil {
			return err
		}
		if err := e.userAtTerminal("needs answer", *yes); err != nil {
			return err
		}
		printDraft(e.Out, d, d.Text)
		if !*yes && !e.confirmed(fmt.Sprintf("Answer %s and post this?", picked.label)) {
			fmt.Fprintln(e.Out, "Not answered.")
			return exitCode(1)
		}
		decision["rev"] = strconv.FormatInt(d.Rev, 10)
	} else if err := e.namedAgent("needs answer"); err != nil {
		return err
	}
	if _, err := c.ResolveGate(ctx, it.Gate.ID, intent.ID, api.TaskRequest{Args: decision, Source: string(dturn.Terminal)}); err != nil {
		return err
	}
	fmt.Fprintf(e.Out, "%s: %s\n", cmp.Or(intent.Label, intent.ID), answer)
	return nil
}
