package home_test

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/home"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/decision"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

var t0 = time.Date(2026, 3, 2, 10, 0, 0, 0, time.UTC)

type fakeLists struct {
	sets  map[task.View][]task.Task
	asked [][]task.View
}

func (f *fakeLists) Sets(ctx context.Context, views []task.View, keep coretasks.Keep) (map[task.View][]task.Task, error) {
	f.asked = append(f.asked, views)
	out := map[task.View][]task.Task{}
	for _, v := range views {
		for _, e := range f.sets[v] {
			if keep != nil {
				if k, err := keep(ctx, e); err != nil || !k {
					continue
				}
			}
			out[v] = append(out[v], e)
		}
	}
	return out, nil
}

type fakeDecisions map[task.ID][]decision.Found

func (f fakeDecisions) Of(_ context.Context, e task.Task) ([]decision.Found, error) {
	return f[e.ID], nil
}

type fakeWords struct {
	profile home.Profile
	kinds   map[decision.Kind]home.KindWords
	plugins map[unit.ProviderID]home.PluginWords
}

func (w fakeWords) Profile() home.Profile                       { return w.profile }
func (w fakeWords) Kind(k decision.Kind) home.KindWords         { return w.kinds[k] }
func (w fakeWords) Plugin(p unit.ProviderID) home.PluginWords   { return w.plugins[p] }
func (w fakeWords) Form(n int) string                           { return map[bool]string{true: "one", false: "other"}[n == 1] }
func plural(one, other string) home.Forms                       { return home.Forms{"one": one, "other": other} }
func found(k decision.Kind, p unit.ProviderID) []decision.Found { return foundAt(k, p, t0) }
func foundAt(k decision.Kind, p unit.ProviderID, at time.Time) []decision.Found {
	f := decision.Found{Kind: k, Since: at}
	if p != "" {
		f.Unit = unit.Unit{Provider: p, Key: "k"}
	}
	return []decision.Found{f}
}

func words() fakeWords {
	return fakeWords{
		profile: home.Profile{Template: "default", Join: ", ", LastJoin: " and ", End: ".", Count: "{n} {noun}", PerSentence: 3, Empty: "All clear.",
			Counters: []home.Counter{{ID: "answer", Kind: decision.Answer}, {ID: "last-step", Kind: decision.LastStep},
				{ID: "loop", View: "in-loop", Noun: plural("task", "tasks"), Clause: home.Forms{"other": "the loop handles {counter}"}}},
			Sections: []home.Section{{ID: "blocked", View: "home-blocked", Label: "Blocked by you", Time: "owed", Limit: 3}},
		},
		kinds: map[decision.Kind]home.KindWords{
			decision.Answer:   {View: "decide-answer", Noun: plural("question", "questions"), Clause: home.Forms{"other": "the agent has {counter} for you"}},
			decision.LastStep: {View: "decide-last-step", Noun: plural("item", "items"), Clause: plural("{counter} only waits for its last step", "{counter} only wait for their last step")},
		},
		plugins: map[unit.ProviderID]home.PluginWords{
			"alpha": {Noun: plural("alpha item", "alpha items"), Kinds: map[decision.Kind]home.PluginKind{decision.LastStep: {Clause: plural("{counter} only waits for alpha", "{counter} only wait for alpha")}}},
			"beta":  {Noun: plural("beta thread", "beta threads")},
		},
	}
}

func tasks(prefix string, n int) []task.Task {
	out := make([]task.Task, n)
	for i := range out {
		out[i] = task.Task{ID: task.ID(fmt.Sprintf("%s-%d", prefix, i)), Title: fmt.Sprintf("%s %d", prefix, i)}
	}
	return out
}

// sentence is the briefing's sentence i, its counters written as [label].
func sentence(h home.Home, i int) string {
	var b strings.Builder
	for _, p := range h.Sentences[i] {
		if p.Counter >= 0 {
			fmt.Fprintf(&b, "[%s]", h.Counts[p.Counter].Label)
		} else {
			b.WriteString(p.Text)
		}
	}
	return b.String()
}

func service(lists *fakeLists, d fakeDecisions, w fakeWords) home.Service {
	return home.Service{Lists: lists, Decisions: d, Words: w}
}

func TestBriefingOmitsZeroCounters(t *testing.T) {
	answers := tasks("q", 2)
	d := fakeDecisions{"q-0": found(decision.Answer, ""), "q-1": found(decision.Answer, "")}
	h, err := service(&fakeLists{sets: map[task.View][]task.Task{"decide-answer": answers}}, d, words()).Home(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Sentences) != 1 || sentence(h, 0) != "The agent has [2 questions] for you." {
		t.Fatalf("sentences %+v", h.Sentences)
	}
	if len(h.Counts) != 3 || h.Counts[1].N != 0 || h.Counts[0].View != "decide-answer" {
		t.Fatalf("counts %+v, want every counter in profile order", h.Counts)
	}
}

func TestBriefingPluginAloneUsesItsCopy(t *testing.T) {
	ts := tasks("a", 3)
	d := fakeDecisions{}
	for _, e := range ts {
		d[e.ID] = found(decision.LastStep, "alpha")
	}
	h, _ := service(&fakeLists{sets: map[task.View][]task.Task{"decide-last-step": ts}}, d, words()).Home(context.Background())
	if got := sentence(h, 0); got != "[3 alpha items] only wait for alpha." {
		t.Fatalf("sentence %q", got)
	}
	if n := h.Counts[1].Nouns; len(n) != 1 || n[0].Plugin != "alpha" || n[0].N != 3 || n[0].Noun != "alpha items" {
		t.Fatalf("nouns %+v", n)
	}
}

func TestBriefingSeveralPluginsListEachNoun(t *testing.T) {
	ts := tasks("x", 5)
	d := fakeDecisions{}
	for i, e := range ts {
		d[e.ID] = found(decision.LastStep, map[bool]unit.ProviderID{true: "alpha", false: "beta"}[i < 3])
	}
	h, _ := service(&fakeLists{sets: map[task.View][]task.Task{"decide-last-step": ts}}, d, words()).Home(context.Background())
	if got := sentence(h, 0); got != "[3 alpha items and 2 beta threads] only wait for their last step." {
		t.Fatalf("sentence %q", got)
	}
}

func TestBriefingPluralCategory(t *testing.T) {
	ts := tasks("a", 1)
	d := fakeDecisions{"a-0": found(decision.LastStep, "")}
	h, _ := service(&fakeLists{sets: map[task.View][]task.Task{"decide-last-step": ts}}, d, words()).Home(context.Background())
	if got := sentence(h, 0); got != "[1 item] only waits for its last step." {
		t.Fatalf("sentence %q", got)
	}
}

func TestBriefingSplitsSentences(t *testing.T) {
	w := words()
	w.profile.Counters = nil
	sets := map[task.View][]task.Task{}
	for i := range 5 {
		v := task.View(fmt.Sprintf("v%d", i))
		w.profile.Counters = append(w.profile.Counters, home.Counter{ID: string(v), View: v, Noun: plural("task", "tasks"), Clause: home.Forms{"other": "clause " + string(v) + " {counter}"}})
		sets[v] = tasks(string(v), i+1)
	}
	h, _ := service(&fakeLists{sets: sets}, fakeDecisions{}, w).Home(context.Background())
	if len(h.Sentences) != 2 {
		t.Fatalf("%d sentences", len(h.Sentences))
	}
	if got := sentence(h, 0); got != "Clause v0 [1 task], clause v1 [2 tasks] and clause v2 [3 tasks]." {
		t.Fatalf("first %q", got)
	}
	if got := sentence(h, 1); got != "Clause v3 [4 tasks] and clause v4 [5 tasks]." {
		t.Fatalf("second %q", got)
	}
}

func TestBriefingEmpty(t *testing.T) {
	h, _ := service(&fakeLists{}, fakeDecisions{}, words()).Home(context.Background())
	if len(h.Sentences) != 0 || h.Empty != "All clear." {
		t.Fatalf("home %+v", h)
	}
}

func TestSectionsLimitAndTotal(t *testing.T) {
	blocked := tasks("b", 5)
	lists := &fakeLists{sets: map[task.View][]task.Task{"home-blocked": blocked}}
	h, _ := service(lists, fakeDecisions{}, words()).Home(context.Background())
	s := h.Sections[0]
	if len(s.Tasks) != 3 || s.Tasks[0].ID != "b-0" || s.Tasks[2].ID != "b-2" || s.Total != 5 || s.Label != "Blocked by you" {
		t.Fatalf("section %+v", s)
	}
	if len(lists.asked) != 1 {
		t.Fatalf("Sets called %d times, want one pass", len(lists.asked))
	}
}

func TestHomeNamesNoProvider(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if name := e.Name(); strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go") {
			f, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ParseComments)
			if err != nil {
				t.Fatal(err)
			}
			ast.Inspect(f, func(n ast.Node) bool {
				if _, ok := n.(*ast.ImportSpec); ok {
					return false
				}
				var text string
				switch x := n.(type) {
				case *ast.BasicLit:
					text = x.Value
				case *ast.Ident:
					text = x.Name
				case *ast.Comment:
					text = x.Text
				}
				for _, provider := range []string{"github", "slack"} {
					if strings.Contains(strings.ToLower(text), provider) {
						t.Errorf("%s names %s: %s", name, provider, text)
					}
				}
				return true
			})
		}
	}
}
