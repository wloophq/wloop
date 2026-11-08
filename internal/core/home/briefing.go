package home

import (
	"cmp"
	"context"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/wloophq/wloop/internal/domain/decision"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Service composes the home and the decision queues.
type Service struct {
	Lists     Lists
	Decisions Decisions
	Words     Words
}

// Home is the briefing, the sections and the capture hint.
type Home struct {
	Template    string
	Sentences   [][]Part
	Counts      []Count
	Empty       string
	Sections    []Rows
	CaptureHint string
}

// Part is a sentence's text, or with Counter at least 0 the count of that index in Home.Counts.
type Part struct {
	Text    string
	Counter int
}

// Count is a counter's count and its words.
type Count struct {
	ID    string
	Kind  decision.Kind
	View  task.View
	N     int
	Label string
	Nouns []PluginNoun
}

// PluginNoun is a plugin's share of a count.
type PluginNoun struct {
	Plugin unit.ProviderID
	N      int
	Noun   string
}

// Rows are a section's first rows and how many it holds.
type Rows struct {
	Section
	Tasks []task.Task
	Total int
}

// Home reads every counter's and section's view in one pass and words the briefing.
func (s Service) Home(ctx context.Context) (Home, error) {
	p := s.Words.Profile()
	var views []task.View
	for _, c := range p.Counters {
		views = append(views, s.viewOf(c))
	}
	for _, sec := range p.Sections {
		views = append(views, sec.View)
	}
	slices.Sort(views)
	sets, err := s.Lists.Sets(ctx, slices.Compact(views), nil)
	if err != nil {
		return Home{}, err
	}
	h := Home{Template: p.Template, CaptureHint: p.CaptureHint}
	var clauses [][]Part
	for i, c := range p.Counters {
		count, clause, err := s.count(ctx, p, c, sets[s.viewOf(c)])
		if err != nil {
			return Home{}, err
		}
		h.Counts = append(h.Counts, count)
		if count.N > 0 {
			clauses = append(clauses, split(clause, i))
		}
	}
	for chunk := range slices.Chunk(clauses, max(p.PerSentence, 1)) {
		h.Sentences = append(h.Sentences, p.sentence(chunk))
	}
	if len(h.Sentences) == 0 {
		h.Empty = p.Empty
	}
	for _, sec := range p.Sections {
		ts := sets[sec.View]
		h.Sections = append(h.Sections, Rows{Section: sec, Tasks: ts[:min(len(ts), sec.Limit)], Total: len(ts)})
	}
	return h, nil
}

func (s Service) viewOf(c Counter) task.View {
	if c.Kind != "" {
		return s.Words.Kind(c.Kind).View
	}
	return c.View
}

// count is counter c over its tasks ts, and its clause in the served plural form: a kind's counter counts the
// decisions its queue lists, a view's the tasks.
func (s Service) count(ctx context.Context, p Profile, c Counter, ts []task.Task) (Count, string, error) {
	n := len(ts)
	var shares []share
	if c.Kind != "" {
		ds, err := s.decisions(ctx, c.Kind, ts)
		if err != nil {
			return Count{}, "", err
		}
		n, shares = len(ds), sharesOf(ds)
	}
	out := Count{ID: c.ID, Kind: c.Kind, View: s.viewOf(c), N: n}
	form := s.Words.Form(n)
	noun, clause := c.Noun, c.Clause
	if c.Kind != "" {
		kw := s.Words.Kind(c.Kind)
		noun, clause = noun.Or(kw.Noun), clause.Or(kw.Clause)
		for _, sh := range shares {
			pn := noun
			if sh.Plugin != "" {
				pn = s.Words.Plugin(sh.Plugin).Noun.Or(noun)
			}
			out.Nouns = append(out.Nouns, PluginNoun{Plugin: sh.Plugin, N: sh.N, Noun: pn.For(s.Words.Form(sh.N))})
		}
		switch {
		case len(shares) == 1 && shares[0].Plugin != "":
			pw := s.Words.Plugin(shares[0].Plugin)
			noun, clause = pw.Noun.Or(noun), pw.Kinds[c.Kind].Clause.Or(clause)
		case len(shares) > 1:
			labels := make([]string, len(out.Nouns))
			for i, pn := range out.Nouns {
				labels[i] = p.countText(pn.N, pn.Noun)
			}
			out.Label = p.list(labels)
		}
	}
	if out.Label == "" {
		out.Label = p.countText(n, noun.For(form))
	}
	return out, clause.For(form), nil
}

// share is a plugin's decisions among a kind's; "" holds those of no unit.
type share struct {
	Plugin unit.ProviderID
	N      int
}

// sharesOf counts ds by the plugin of each decision's unit, the most first.
func sharesOf(ds []taskDecision) []share {
	by := map[unit.ProviderID]int{}
	for _, d := range ds {
		by[d.found.Unit.Provider]++
	}
	out := make([]share, 0, len(by))
	for p, n := range by {
		out = append(out, share{p, n})
	}
	slices.SortFunc(out, func(a, b share) int { return cmp.Or(b.N-a.N, cmp.Compare(a.Plugin, b.Plugin)) })
	return out
}

func (p Profile) countText(n int, noun string) string {
	return strings.NewReplacer("{n}", strconv.Itoa(n), "{noun}", noun).Replace(cmp.Or(p.Count, "{n} {noun}"))
}

// list joins items with Join, LastJoin before the last.
func (p Profile) list(items []string) string {
	if len(items) < 2 {
		return strings.Join(items, "")
	}
	return strings.Join(items[:len(items)-1], p.Join) + p.LastJoin + items[len(items)-1]
}

// split is clause's parts, counter i where "{counter}" stands.
func split(clause string, i int) []Part {
	var out []Part
	for j, text := range strings.Split(clause, "{counter}") {
		if j > 0 {
			out = append(out, Part{Counter: i})
		}
		if text != "" {
			out = append(out, Part{Text: text, Counter: -1})
		}
	}
	return out
}

// sentence joins clauses as list does, ends it with End and capitalizes its first letter.
func (p Profile) sentence(clauses [][]Part) []Part {
	var out []Part
	add := func(ps ...Part) {
		for _, x := range ps {
			if n := len(out); x.Counter < 0 && n > 0 && out[n-1].Counter < 0 {
				out[n-1].Text += x.Text
				continue
			}
			out = append(out, x)
		}
	}
	for i, c := range clauses {
		switch {
		case i == 0:
		case i == len(clauses)-1:
			add(Part{Text: p.LastJoin, Counter: -1})
		default:
			add(Part{Text: p.Join, Counter: -1})
		}
		add(c...)
	}
	add(Part{Text: p.End, Counter: -1})
	if len(out) > 0 && out[0].Counter < 0 {
		r, size := utf8.DecodeRuneInString(out[0].Text)
		out[0].Text = string(unicode.ToUpper(r)) + out[0].Text[size:]
	}
	return out
}
