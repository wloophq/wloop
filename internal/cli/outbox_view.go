package cli

import (
	"cmp"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/termfmt"
)

// closes says whether sending d closes its unit's items.
func closes(d api.Draft) bool { return slices.Contains(d.Effects, builtin.EffectUnitClosed) }

// itemsVerb is how d's items read beside it, from what sending it does.
func itemsVerb(d api.Draft) string {
	switch {
	case closes(d):
		return "Closes"
	case slices.Contains(d.Effects, builtin.EffectEntryNudged):
		return "Asks about"
	}
	return "Answers"
}

func groupTitle(purpose string) string {
	return strings.ToUpper(purposeName(purpose))
}

// purposeName is one draft's purpose as a person reads it.
func purposeName(purpose string) string {
	return strings.ReplaceAll(purpose, "_", " ")
}

var mentionToken = regexp.MustCompile(`@\{([^{}\s]+)\}`)

// mentionName is how a mention key reads: the name the unit's items know, else the id after the provider.
func mentionName(d api.Draft, key string) string {
	if n, ok := d.Names[key]; ok {
		return "@" + n
	}
	_, id, ok := strings.Cut(key, ":")
	if !ok {
		return "@" + key
	}
	return "@" + id
}

// renderedText is d's text with each @{provider:id} mention as the person reads it.
func renderedText(d api.Draft) string {
	return mentionToken.ReplaceAllStringFunc(d.Text, func(m string) string { return mentionName(d, m[2:len(m)-1]) })
}

// draftMentions are the people d's text names, as they read.
func draftMentions(d api.Draft) []string {
	var out []string
	for _, m := range mentionsOf(d.Text) {
		if strings.HasPrefix(m, "@{") {
			m = mentionName(d, strings.TrimSuffix(strings.TrimPrefix(m, "@{"), "}"))
		}
		if !slices.Contains(out, m) {
			out = append(out, m)
		}
	}
	return out
}

var bullet = regexp.MustCompile(`^\s*[-*]\s+(.*?)(?::\s*\S+)?\s*$`)

// gist is the first meaningful line of d: what a closing closes, a reaction's emoji, else the first line
// of its rendered text.
func gist(d api.Draft) string {
	switch {
	case d.Reaction != "":
		return ":" + d.Reaction + ":"
	case closes(d):
		var closed []string
		for _, l := range strings.Split(d.Text, "\n") {
			if m := bullet.FindStringSubmatch(l); m != nil {
				closed = append(closed, m[1])
			}
		}
		n := max(len(closed), len(d.Items))
		if n > 0 {
			items := "items"
			if n == 1 {
				items = "item"
			}
			return oneLine(fmt.Sprintf("closes %d %s: %s", n, items, strings.Join(closed, "; ")))
		}
	}
	for _, l := range strings.Split(renderedText(d), "\n") {
		if l = oneLine(l); l != "" {
			return l
		}
	}
	return ""
}

func draftAge(d api.Draft, now time.Time) string {
	return shortAge(now.Sub(d.CreatedAt), d.CreatedAt.IsZero())
}

// constant says whether f gives every draft the same value.
func constant(ds []api.Draft, f func(api.Draft) string) bool {
	for _, d := range ds[1:] {
		if f(d) != f(ds[0]) {
			return false
		}
	}
	return true
}

// writeOutbox lays the drafts out for a person: a group per kind, each draft with its id, its unit as its
// row reads, who it mentions, its age and its gist; the state, the approval and the target show only where
// the drafts differ in them.
func writeOutbox(w io.Writer, t termfmt.Term, ds []api.Draft, now time.Time) {
	drafts := "drafts"
	if len(ds) == 1 {
		drafts = "draft"
	}
	fmt.Fprintln(w, t.Bold("Outbox")+"  "+t.Dim(fmt.Sprintf("%d %s waiting on you", len(ds), drafts)))
	showState := !constant(ds, func(d api.Draft) string { return d.State })
	showApproval := !constant(ds, approvalOf)
	showTarget := slices.ContainsFunc(ds, func(d api.Draft) bool { return d.Target != "" })
	showMentions := slices.ContainsFunc(ds, func(d api.Draft) bool { return len(draftMentions(d)) > 0 })
	width := reportWidth(t) - 2
	cols := []termfmt.Column{{Right: true}, {Max: max(20, min(44, width*2/5))}}
	if showMentions {
		cols = append(cols, termfmt.Column{Max: max(10, min(24, width/6))})
	}
	cols = append(cols, termfmt.Column{Right: true})
	if showState {
		cols = append(cols, termfmt.Column{})
	}
	if showApproval {
		cols = append(cols, termfmt.Column{})
	}
	if showTarget {
		cols = append(cols, termfmt.Column{Max: 24})
	}
	cols = append(cols, termfmt.Column{Flex: true})
	var rows [][]termfmt.Cell
	var heads []int
	for _, g := range purposesOf(ds) {
		heads = append(heads, len(rows))
		for _, d := range ds {
			if d.Purpose != g {
				continue
			}
			r := []termfmt.Cell{{Text: fmt.Sprintf("#%d", d.ID), Style: t.Cyan}, {Text: draftLabel(d)}}
			if showMentions {
				r = append(r, termfmt.Cell{Text: strings.Join(draftMentions(d), " "), Style: t.Dim})
			}
			r = append(r, termfmt.Cell{Text: draftAge(d, now), Style: t.Dim})
			if showState {
				r = append(r, termfmt.Cell{Text: d.State, Style: stateStyle(t, d.State)})
			}
			if showApproval {
				r = append(r, termfmt.Cell{Text: approvalOf(d), Style: t.Dim})
			}
			if showTarget {
				r = append(r, termfmt.Cell{Text: cmp.Or(d.Target, "the unit"), Style: t.Dim})
			}
			rows = append(rows, append(r, termfmt.Cell{Text: gist(d)}))
		}
	}
	lines := t.Columns(width, 2, cols, rows)
	for i, l := range lines {
		if g := slices.Index(heads, i); g >= 0 {
			purpose := purposesOf(ds)[g]
			n := 0
			for _, d := range ds {
				if d.Purpose == purpose {
					n++
				}
			}
			fmt.Fprintln(w)
			fmt.Fprintln(w, t.Bold(groupTitle(purpose))+" "+t.Dim(fmt.Sprint(n)))
		}
		fmt.Fprintln(w, "  "+l)
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, t.Dim("Next: "+brand.Command+" outbox show <id> · approve <id> · discard <id>"))
}

// purposesOf are the purposes of ds in the order they first appear.
func purposesOf(ds []api.Draft) []string {
	var out []string
	for _, d := range ds {
		if !slices.Contains(out, d.Purpose) {
			out = append(out, d.Purpose)
		}
	}
	return out
}

func stateStyle(t termfmt.Term, state string) func(string) string {
	switch state {
	case "failed":
		return t.Red
	case "sending", "approved":
		return t.Yellow
	case "sent":
		return t.Green
	}
	return t.Dim
}

// writeOutboxPlain writes one tab-separated line per draft for scripts: id, kind, state, unit, its label,
// the mentions, the creation time and the gist.
func writeOutboxPlain(w io.Writer, ds []api.Draft) {
	for _, d := range ds {
		fields := []string{fmt.Sprint(d.ID), d.Kind, d.State, d.Unit, draftLabel(d), strings.Join(draftMentions(d), ","),
			d.CreatedAt.UTC().Format(time.RFC3339), gist(d)}
		for i, f := range fields {
			fields[i] = strings.ReplaceAll(oneLine(f), "\t", " ")
		}
		fmt.Fprintln(w, strings.Join(fields, "\t"))
	}
}

// draftCommands are the commands d takes now, from its actions.
func draftCommands(d api.Draft) []string {
	var out []string
	for _, a := range d.Actions {
		verb, ok := strings.CutPrefix(a.ID, "draft.")
		if !a.Enabled || !ok || !slices.Contains(api.DraftActions, verb) {
			continue
		}
		line := fmt.Sprintf("%s outbox %s %d", brand.Command, verb, d.ID)
		switch {
		case verb == "edit":
			line += " <markdown>"
		case verb == "retry" && a.Confirm != "":
			line += " --confirm"
		}
		out = append(out, line)
	}
	return out
}

// writeDraftView lays one draft out for a person: its unit and link, the text as it reads, the items it is
// about, where it goes, where it came from and the commands it takes.
func writeDraftView(w io.Writer, t termfmt.Term, d api.Draft, now time.Time) {
	width := reportWidth(t)
	head := t.Cyan(fmt.Sprintf("#%d", d.ID)) + "  " + t.Bold(purposeName(d.Purpose)) + "  " + stateStyle(t, d.State)(d.State)
	fmt.Fprintln(w, head)
	fmt.Fprintln(w, t.Link(d.Link, t.Bold(draftLabel(d))))
	if d.Link != "" {
		fmt.Fprintln(w, t.Dim(d.Link))
	}
	fmt.Fprintln(w)
	if d.Reaction != "" {
		fmt.Fprintln(w, "  :"+d.Reaction+":")
	} else {
		for _, l := range strings.Split(renderedText(d), "\n") {
			if strings.TrimSpace(l) == "" {
				fmt.Fprintln(w)
				continue
			}
			for _, x := range termfmt.Wrap(l, width-2, width-2) {
				fmt.Fprintln(w, "  "+x)
			}
		}
	}
	fmt.Fprintln(w)
	field := func(name, value string) {
		lines := termfmt.Wrap(value, width-12, width-12)
		for i, l := range lines {
			label := ""
			if i == 0 {
				label = name
			}
			fmt.Fprintln(w, t.Dim(padRight(label, 10))+"  "+l)
		}
	}
	if len(d.ItemDetails) > 0 {
		for i, it := range d.ItemDetails {
			label := ""
			if i == 0 {
				label = itemsVerb(d)
			}
			fmt.Fprintln(w, t.Dim(padRight(label, 10))+"  "+cmp.Or(oneLine(it.Summary), it.ID)+" "+t.Dim("("+cmp.Or(it.State, "unknown")+")"))
		}
	}
	if ms := draftMentions(d); len(ms) > 0 {
		field("Mentions", strings.Join(ms, ", "))
	}
	field("To", cmp.Or(d.Where, d.Target, "destination unknown"))
	field("Origin", draftOrigin(d)+", "+draftAge(d, now)+" ago")
	field("Approval", approvalOf(d))
	if d.Deadline != nil {
		field("Deadline", d.Deadline.Local().Format("2006-01-02 15:04 MST"))
	}
	if d.Error != "" {
		field("Error", d.Error)
	}
	if d.State == "discarded" && d.Reason != "" {
		field("Discarded", d.Reason)
	}
	if d.Permalink != "" {
		field("Posted", d.Permalink)
	}
	if cmds := draftCommands(d); len(cmds) > 0 {
		fmt.Fprintln(w)
		for _, c := range cmds {
			fmt.Fprintln(w, t.Dim(c))
		}
	}
}

func draftOrigin(d api.Draft) string {
	switch {
	case d.Origin.Kind == "user":
		return "written by you"
	case d.Origin.TurnSeq > 0:
		return fmt.Sprintf("turn %d (%s)", d.Origin.TurnSeq, d.Origin.Intent) + originOn(d)
	case d.Origin.Rule != "":
		return "rule " + d.Origin.Rule
	}
	return cmp.Or(d.Origin.Kind, "unknown")
}

// originOn names the unit of a draft's proposing turn when it is not the draft's.
func originOn(d api.Draft) string {
	if d.Origin.Unit == "" {
		return ""
	}
	return " on " + d.Origin.Unit
}
