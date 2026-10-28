package cli

import (
	"bufio"
	"cmp"
	"context"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/brand"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/termfmt"
)

var outboxUsage = "usage: " + brand.Command + " outbox [list [--color <when>] | show <id> [--color <when>] | new <unit|message> <markdown> [--react <emoji>] [--send] [--yes] | approve <id> [--text <markdown>] [--yes] | edit <id> <markdown> | discard <id> [--reason <why>] | retry <id> [--confirm] [--yes]]"

// outboxInteractive says whether e reads from a terminal; tests replace it.
var outboxInteractive = func(e *Env) bool {
	f, ok := e.In.(*os.File)
	return ok && term.IsTerminal(int(f.Fd()))
}

func runOutbox(ctx context.Context, e *Env, args []string) error {
	sub := "list"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	run, ok := map[string]func(context.Context, *Env, *client.Client, []string) error{
		"list": outboxList, "show": outboxShow, "new": outboxNew, "approve": outboxApprove, "edit": outboxEdit, "discard": outboxDiscard, "retry": outboxRetry,
	}[sub]
	if !ok {
		return usageError{usage: outboxUsage, why: fmt.Sprintf("unknown outbox command %q", sub)}
	}
	c, err := e.daemonClient(ctx)
	if err != nil {
		return err
	}
	return run(ctx, e, c, args)
}

func outboxList(ctx context.Context, e *Env, c *client.Client, args []string) error {
	fs := flag.NewFlagSet("outbox list", flag.ContinueOnError)
	fs.SetOutput(e.Err)
	var color termfmt.Mode
	fs.Var(&color, "color", "lay the list out for a terminal: auto, always or never")
	pos, err := parseInterspersed(fs, args)
	if err != nil || len(pos) > 0 {
		return usageError{usage: outboxUsage}
	}
	ds, err := c.Drafts(ctx, "proposed", "failed", "sending")
	if err != nil {
		return err
	}
	t := e.term(color)
	switch {
	case !t.TTY:
		writeOutboxPlain(e.Out, ds)
	case len(ds) == 0:
		fmt.Fprintln(e.Out, "the Outbox is empty")
	default:
		writeOutbox(e.Out, t, ds, e.Now())
	}
	return nil
}

// draftLabel names the draft's unit as its row does: the label and the title, else the unit key.
func draftLabel(d api.Draft) string {
	switch {
	case d.Label == "":
		return d.Unit
	case d.Title == "" || strings.HasPrefix(d.Title, d.Label):
		return cmp.Or(d.Title, d.Label)
	}
	return d.Label + " · " + d.Title
}

func draftTarget(d api.Draft) string {
	if d.Target == "" {
		return "(the unit)"
	}
	return d.Target
}

func draftWhere(d api.Draft) string {
	if d.Where == "" {
		return "destination unknown"
	}
	return d.Where
}

func approvalOf(d api.Draft) string {
	if d.Approval.Window != "" {
		return d.Approval.Mode + " " + d.Approval.Window
	}
	return d.Approval.Mode
}

// draftArg is the one draft id args hold.
func draftArg(args []string) (int64, error) {
	if len(args) != 1 {
		return 0, usageError{usage: outboxUsage}
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil || id <= 0 {
		return 0, usageError{usage: outboxUsage, why: fmt.Sprintf("%q is not a draft id", args[0])}
	}
	return id, nil
}

func outboxShow(ctx context.Context, e *Env, c *client.Client, args []string) error {
	fs := flag.NewFlagSet("outbox show", flag.ContinueOnError)
	fs.SetOutput(e.Err)
	var color termfmt.Mode
	fs.Var(&color, "color", "color the draft: auto, always or never")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return usageError{usage: outboxUsage}
	}
	id, err := draftArg(pos)
	if err != nil {
		return err
	}
	d, err := c.Draft(ctx, id)
	if err != nil {
		return err
	}
	writeDraftView(e.Out, e.term(color), d, e.Now())
	return nil
}

var mentionPattern = regexp.MustCompile(`@\{[^}\s]+\}|(?:^|[^\w@{])@[A-Za-z0-9][\w./-]*`)

// mentionsOf are the people text names: structured mentions and raw @handles.
func mentionsOf(text string) []string {
	var out []string
	for _, m := range mentionPattern.FindAllString(text, -1) {
		m = m[strings.Index(m, "@"):]
		if !slices.Contains(out, m) {
			out = append(out, m)
		}
	}
	return out
}

// printDraft writes d whole, with text as what would post.
func printDraft(w io.Writer, d api.Draft, text string) {
	fmt.Fprintf(w, "draft %d (%s): a %s on %s\n", d.ID, d.State, d.Kind, d.Unit)
	fmt.Fprintf(w, "target:   %s\n", draftTarget(d))
	if d.Permalink == "" {
		fmt.Fprintf(w, "to:       %s\n", draftWhere(d))
	}
	origin := d.Origin.Kind
	switch {
	case d.Origin.TurnSeq > 0:
		origin += fmt.Sprintf(" %d (%s)", d.Origin.TurnSeq, d.Origin.Intent) + originOn(d)
	case d.Origin.Rule != "":
		origin += " " + d.Origin.Rule
	}
	fmt.Fprintf(w, "origin:   %s\n", origin)
	fmt.Fprintf(w, "approval: %s\n", approvalOf(d))
	if d.Deadline != nil {
		fmt.Fprintf(w, "deadline: %s\n", d.Deadline.Local().Format("2006-01-02 15:04 MST"))
	}
	if d.Error != "" {
		fmt.Fprintf(w, "error:    %s\n", d.Error)
	}
	if d.Permalink != "" {
		fmt.Fprintf(w, "posted:   %s\n", d.Permalink)
	}
	if ms := mentionsOf(text); len(ms) > 0 {
		fmt.Fprintf(w, "mentions: %s\n", strings.Join(ms, ", "))
	}
	if d.Reaction != "" {
		fmt.Fprintf(w, "reaction: :%s:\n", d.Reaction)
		return
	}
	fmt.Fprintln(w, "text:")
	for _, l := range strings.Split(text, "\n") {
		fmt.Fprintln(w, "  "+l)
	}
	if d.Edited && text == d.Text {
		fmt.Fprintln(w, "as proposed:")
		for _, l := range strings.Split(d.Original, "\n") {
			fmt.Fprintln(w, "  "+l)
		}
	}
	if d.Replaced != "" {
		fmt.Fprintln(w, "replaced:")
		for _, l := range strings.Split(d.Replaced, "\n") {
			fmt.Fprintln(w, "  "+l)
		}
	}
}

// userAtTerminal refuses to post from a shell an agent session runs, or one with no terminal to ask in: what
// posts is approved by the user, who reads it whole first.
func (e *Env) userAtTerminal(verb string) error {
	if err := e.notAnAgent(verb); err != nil {
		return err
	}
	if !outboxInteractive(e) {
		return fmt.Errorf("outbox %s posts as you, so it shows the draft and asks at a terminal, and standard input is not one: approve the draft in the cockpit or the terminal UI", verb)
	}
	return nil
}

// notAnAgent refuses outbox verb from a shell an agent session runs: it posts as the user.
func (e *Env) notAnAgent(verb string) error {
	getenv := e.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	for _, kind := range slices.Sorted(maps.Keys(e.Harness.Agents)) {
		if e.Harness.Agents[kind].SessionID(getenv) != "" {
			return fmt.Errorf("outbox %s posts as you, and this shell belongs to a %s agent session: approve the draft in the cockpit or the terminal UI, or run this in a terminal of your own", verb, kind)
		}
	}
	return nil
}

func (e *Env) confirmed(question string) bool {
	fmt.Fprintf(e.Out, "%s [y/N] ", question)
	in := e.In
	if in == nil {
		in = os.Stdin
	}
	answer, _ := bufio.NewReader(in).ReadString('\n')
	a := strings.ToLower(strings.TrimSpace(answer))
	return a == "y" || a == "yes"
}

func outboxApprove(ctx context.Context, e *Env, c *client.Client, args []string) error {
	fs := flag.NewFlagSet("outbox approve", flag.ContinueOnError)
	fs.SetOutput(e.Err)
	text := fs.String("text", "", "the text to post instead of the draft's")
	yes := fs.Bool("yes", false, "approve without asking, after showing the draft")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	id, err := draftArg(pos)
	if err != nil {
		return err
	}
	if err := e.userAtTerminal("approve"); err != nil {
		return err
	}
	d, err := c.Draft(ctx, id)
	if err != nil {
		return err
	}
	shown := d.Text
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "text" {
			shown = *text
		}
	})
	printDraft(e.Out, d, shown)
	if !*yes && !e.confirmed("Approve and post this?") {
		fmt.Fprintln(e.Out, "Not approved.")
		return exitCode(1)
	}
	got, err := c.DraftDo(ctx, id, "approve", api.DraftAction{Text: &shown, Rev: &d.Rev})
	if err != nil {
		return err
	}
	fmt.Fprintf(e.Out, "draft %d %s; it posts on the daemon's next send\n", got.ID, got.State)
	return nil
}

// outboxNew writes a draft on a unit: saved to the Outbox for approval, or with --send posted through it
// after the user reads it at a terminal and says yes, or at once with --yes.
func outboxNew(ctx context.Context, e *Env, c *client.Client, args []string) error {
	fs := flag.NewFlagSet("outbox new", flag.ContinueOnError)
	fs.SetOutput(e.Err)
	react := fs.String("react", "", "react with this emoji instead of replying: to the Slack message named, else to the unit's message")
	send := fs.Bool("send", false, "post it now instead of saving it to the Outbox")
	yes := fs.Bool("yes", false, "with --send, post without asking")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	switch {
	case *react == "" && len(pos) != 2, *react != "" && len(pos) != 1:
		return usageError{usage: outboxUsage, why: "outbox new takes a unit and the reply's markdown, or a unit and --react <emoji>"}
	case *yes && !*send:
		return usageError{usage: outboxUsage, why: "--yes goes with --send"}
	}
	u, err := e.unitTarget(ctx, pos[0])
	if err != nil {
		return err
	}
	cr := api.DraftCreate{Unit: u, Source: string(dturn.Terminal)}
	if e.pluginTakes(pos[0]) {
		cr.Target = pos[0]
	}
	if *react != "" {
		cr.Kind, cr.Reaction = api.DraftReact, *react
	} else {
		cr.Kind, cr.Text = api.DraftReply, pos[1]
	}
	if !*send {
		d, err := c.CreateDraft(ctx, cr)
		if err != nil {
			return err
		}
		fmt.Fprintf(e.Out, "draft %d saved to the Outbox for your approval: %s\n", d.ID, draftLabel(d))
		return nil
	}
	if err := e.notAnAgent("new --send"); err != nil {
		return err
	}
	return e.sendUserDraft(ctx, c, cr, *yes, func() error { return e.userAtTerminal("new --send") }, true)
}

// sendUserDraft creates cr and approves it: at once with yes, else once ready says the user is at a
// terminal, they read the draft and say yes. A no discards the draft, or keeps it in the Outbox with keep.
// It then waits a moment for the post.
func (e *Env) sendUserDraft(ctx context.Context, c *client.Client, cr api.DraftCreate, yes bool, ready func() error, keep bool) error {
	if yes {
		cr.Send = true
		d, err := c.CreateDraft(ctx, cr)
		if err != nil {
			return err
		}
		return e.reportPosted(ctx, c, d, d.Where)
	}
	if err := ready(); err != nil {
		return err
	}
	d, err := c.CreateDraft(ctx, cr)
	if err != nil {
		return err
	}
	printDraft(e.Out, d, d.Text)
	if !e.confirmed("Post this?") {
		if keep {
			fmt.Fprintf(e.Out, "Not sent; draft %d stays in the Outbox.\n", d.ID)
			return exitCode(1)
		}
		if _, err := c.DraftDo(ctx, d.ID, "discard", api.DraftAction{Reason: "user"}); err != nil {
			return fmt.Errorf("not sent, and draft %d stays in the Outbox: %w", d.ID, err)
		}
		fmt.Fprintln(e.Out, "Not sent.")
		return exitCode(1)
	}
	got, err := c.DraftDo(ctx, d.ID, "approve", api.DraftAction{Rev: &d.Rev})
	if err != nil {
		return err
	}
	return e.reportPosted(ctx, c, got, d.Where)
}

// postWait bounds how long reportPosted waits for the daemon to post an approved draft; postPoll is how
// often it looks.
var postWait, postPoll = 30 * time.Second, 250 * time.Millisecond

// reportPosted waits for approved draft d to go out and says where it landed; where is where it was to go.
func (e *Env) reportPosted(ctx context.Context, c *client.Client, d api.Draft, where string) error {
	deadline := time.Now().Add(postWait)
	for d.State == "approved" || d.State == "sending" {
		if time.Now().After(deadline) {
			fmt.Fprintf(e.Out, "draft %d %s; it posts on the daemon's next send\n", d.ID, d.State)
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(postPoll):
		}
		var err error
		if d, err = c.Draft(ctx, d.ID); err != nil {
			return err
		}
	}
	switch d.State {
	case "sent":
		fmt.Fprintf(e.Out, "Posted to %s: %s\n", cmp.Or(where, d.Where, draftLabel(d)), d.Permalink)
		return nil
	case "failed":
		return fmt.Errorf("draft %d failed: %s", d.ID, d.Error)
	}
	fmt.Fprintf(e.Out, "draft %d %s\n", d.ID, d.State)
	return nil
}

func outboxRetry(ctx context.Context, e *Env, c *client.Client, args []string) error {
	fs := flag.NewFlagSet("outbox retry", flag.ContinueOnError)
	fs.SetOutput(e.Err)
	confirm := fs.Bool("confirm", false, "post again a draft that may have been posted")
	yes := fs.Bool("yes", false, "retry without asking, after showing the draft")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	id, err := draftArg(pos)
	if err != nil {
		return err
	}
	if err := e.userAtTerminal("retry"); err != nil {
		return err
	}
	d, err := c.Draft(ctx, id)
	if err != nil {
		return err
	}
	printDraft(e.Out, d, d.Text)
	if !*yes && !e.confirmed("Post this again?") {
		fmt.Fprintln(e.Out, "Not retried.")
		return exitCode(1)
	}
	got, err := c.DraftDo(ctx, id, "retry", api.DraftAction{Confirm: *confirm, Rev: &d.Rev})
	if err != nil {
		return err
	}
	fmt.Fprintf(e.Out, "draft %d %s; it posts on the daemon's next send\n", got.ID, got.State)
	return nil
}

func outboxEdit(ctx context.Context, e *Env, c *client.Client, args []string) error {
	if len(args) != 2 {
		return usageError{usage: outboxUsage}
	}
	id, err := draftArg(args[:1])
	if err != nil {
		return err
	}
	got, err := c.DraftDo(ctx, id, "edit", api.DraftAction{Text: &args[1]})
	if err != nil {
		return err
	}
	fmt.Fprintf(e.Out, "draft %d edited\n", got.ID)
	return nil
}

func outboxDiscard(ctx context.Context, e *Env, c *client.Client, args []string) error {
	fs := flag.NewFlagSet("outbox discard", flag.ContinueOnError)
	fs.SetOutput(e.Err)
	reason := fs.String("reason", "", "why the draft is dropped")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	id, err := draftArg(pos)
	if err != nil {
		return err
	}
	got, err := c.DraftDo(ctx, id, "discard", api.DraftAction{Reason: *reason})
	if err != nil {
		return err
	}
	fmt.Fprintf(e.Out, "draft %d discarded\n", got.ID)
	return nil
}
