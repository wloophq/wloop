package timeline

import (
	"slices"
	"strconv"
	"strings"
	"text/template"
	"time"
)

// Rule folds events into one node of a timeline, as a profile's [facts.<name>] declares it: a Start kind opens
// the node, an End kind closes it when End is set (a span), and an Absorb kind joins the open span, or the node
// just before it when that node is the rule's.
type Rule struct {
	Name                 string
	Start, End, Absorb   []string
	Label, Running, Tone string
	// Subs words an event's sub, such as a review's verdict; a sub it leaves out reads as itself.
	Subs map[string]string
}

// Word is how one event kind reads on a timeline.
type Word struct{ Label, Tone string }

// Words are what Group reads beside the rules: each kind's words, a duration in words, and an event's sub.
type Words struct {
	Kinds    map[string]Word
	Duration func(time.Duration) string
	// Sub is the short value of an event a label's {{.sub}} reads, such as a review's verdict; nil for none.
	Sub func(Event) string
}

// Node is one fact of a timeline: the events a rule folded, or one event no rule took (Rule ""). End is zero
// for a single event and for a span still Running.
type Node struct {
	Rule, Label, Tone string
	At, End           time.Time
	Running           bool
	Actor             string
	Events            []Event
}

// Group folds events, oldest first, into nodes by rules, tried in their order; the nodes come out newest first.
// No event is dropped: one no rule takes is a node of its own, labeled by its kind's words or, when no layer
// words its kind, by the kind itself.
func Group(events []Event, rules []Rule, words Words) []Node {
	type built struct {
		node   Node
		rule   int
		closed bool
	}
	var nodes []built
	open := map[int]int{}
	add := func(i int, e Event) { nodes[i].node.Events = append(nodes[i].node.Events, e) }
	for _, e := range events {
		if ri, ni, ok := openSpan(rules, open, func(r Rule) bool { return slices.Contains(r.End, e.Kind) }); ok {
			add(ni, e)
			nodes[ni].node.End, nodes[ni].closed = e.At, true
			delete(open, ri)
			continue
		}
		if _, ni, ok := openSpan(rules, open, func(r Rule) bool { return slices.Contains(r.Absorb, e.Kind) || slices.Contains(r.Start, e.Kind) }); ok {
			add(ni, e)
			continue
		}
		if last := len(nodes) - 1; last >= 0 && nodes[last].rule >= 0 && slices.Contains(rules[nodes[last].rule].Absorb, e.Kind) {
			add(last, e)
			continue
		}
		ri := slices.IndexFunc(rules, func(r Rule) bool { return slices.Contains(r.Start, e.Kind) })
		nodes = append(nodes, built{node: Node{At: e.At, Actor: e.Actor, Events: []Event{e}}, rule: ri})
		if ri >= 0 && len(rules[ri].End) > 0 {
			open[ri] = len(nodes) - 1
		}
	}
	labels := labeler{}
	out := make([]Node, 0, len(nodes))
	for _, b := range slices.Backward(nodes) {
		n := b.node
		first := n.Events[0]
		w, declared := words.Kinds[first.Kind]
		event := first.Kind
		if declared && w.Label != "" {
			event = w.Label
		}
		if b.rule < 0 {
			n.Label, n.Tone = event, w.Tone
			out = append(out, n)
			continue
		}
		r := rules[b.rule]
		n.Rule, n.Tone = r.Name, r.Tone
		if n.Tone == "" {
			n.Tone = w.Tone
		}
		n.Running = len(r.End) > 0 && !b.closed
		vars := map[string]string{"actor": n.Actor, "count": strconv.Itoa(len(n.Events)), "event": event}
		if words.Sub != nil {
			sub := words.Sub(first)
			vars["sub"] = sub
			if s, ok := r.Subs[sub]; ok {
				vars["sub"] = s
			}
		}
		if b.closed && words.Duration != nil {
			vars["duration"] = words.Duration(n.End.Sub(n.At))
		}
		text := r.Label
		if n.Running && r.Running != "" {
			text = r.Running
		}
		n.Label = event
		if text != "" {
			n.Label = labels.fill(text, vars)
		}
		out = append(out, n)
	}
	return out
}

// openSpan is the first rule, in order, with a span open whose takes accepts the event, and that span's node.
func openSpan(rules []Rule, open map[int]int, takes func(Rule) bool) (rule, node int, ok bool) {
	for ri, r := range rules {
		if ni, isOpen := open[ri]; isOpen && takes(r) {
			return ri, ni, true
		}
	}
	return 0, 0, false
}

// labeler fills label templates, each parsed once per Group.
type labeler map[string]*template.Template

func (l labeler) fill(text string, vars map[string]string) string {
	tpl, ok := l[text]
	if !ok {
		parsed, err := template.New("label").Option("missingkey=zero").Parse(text)
		if err != nil {
			parsed = nil
		}
		l[text], tpl = parsed, parsed
	}
	if tpl == nil {
		return text
	}
	var b strings.Builder
	if err := tpl.Execute(&b, vars); err != nil {
		return text
	}
	return b.String()
}
