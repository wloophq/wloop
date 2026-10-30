package eval

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/wloophq/wloop/plugins/slack/extract"
)

// LedgerView is what the tool shows the viewer about f: the extracted items
// with names, never the messages.
func LedgerView(f Fixture, r extract.Result, names map[string]string, now time.Time) string {
	who := func(ps []extract.PersonOut) string {
		var out []string
		for _, p := range ps {
			if p.ID != "" || p.Name != "" {
				out = append(out, cmpOr(names[p.ID], cmpOr(p.Name, p.ID)))
			}
		}
		if len(out) == 0 {
			return "nobody"
		}
		return strings.Join(out, ", ")
	}
	when := func(ts string) string {
		t, ok := tsTime(ts)
		if !ok {
			return "?"
		}
		return t.In(now.Location()).Format("2006-01-02 15:04")
	}
	yes := func(b bool) string {
		if b {
			return "yes"
		}
		return "no"
	}
	lines := []string{
		fmt.Sprintf("Conversation: #%s (%s), %s", f.ChannelName, f.Kind, f.Permalink),
		fmt.Sprintf("Tool verdict: %s (%s)", r.Verdict, r.VerdictWhy),
	}
	if r.Brief != "" {
		lines = append(lines, "Brief: "+r.Brief)
	}
	lines = append(lines, fmt.Sprintf("You started it: %s; your role: %s; closure posted: %s", yes(r.StartedByViewer), r.ViewerRole, yes(r.ClosurePosted)), "")
	for _, it := range r.Items {
		lines = append(lines,
			fmt.Sprintf("- [%s] %s", it.Kind, it.Summary),
			fmt.Sprintf("  requested by %s on %s; assignees: %s (assigned %s, %s)", who([]extract.PersonOut{it.Requester}), when(it.OriginTS), who(it.Assignees), when(it.AssignmentTS), it.AssignmentBasis))
		state := fmt.Sprintf("  waiting on: %s; state: %s", who(it.WaitingOn), it.State)
		if it.State == "resolved" {
			state += fmt.Sprintf(", %s on %s (%s)", it.Resolution, when(it.EvidenceTS), it.EvidenceKind)
		}
		lines = append(lines, state)
		if it.Context != "" {
			lines = append(lines, "  context: "+it.Context)
		}
		for _, o := range it.Options {
			opt := "  option: " + o.Option
			if len(o.BackedBy) > 0 {
				opt += " (backed by " + who(o.BackedBy) + ")"
			}
			lines = append(lines, opt)
		}
		if it.Deadline != "" {
			lines = append(lines, "  deadline: "+it.Deadline)
		}
		for _, l := range it.Links {
			lines = append(lines, fmt.Sprintf("  link: %s %s", l.Label, l.URL))
		}
		if it.Confidence == "low" {
			lines = append(lines, "  low confidence: "+it.Ambiguity)
		}
	}
	if len(r.Items) == 0 {
		lines = append(lines, "(no items)")
	}
	for _, l := range r.Links {
		lines = append(lines, fmt.Sprintf("linked: %s (%s)", l.Target, l.Kind))
	}
	return strings.Join(lines, "\n")
}

func tsTime(ts string) (time.Time, bool) {
	sec, frac, _ := strings.Cut(ts, ".")
	s, err := strconv.ParseInt(sec, 10, 64)
	if err != nil || ts == "" {
		return time.Time{}, false
	}
	us, _ := strconv.ParseInt((frac + "000000")[:6], 10, 64)
	return time.Unix(s, us*1000), true
}
