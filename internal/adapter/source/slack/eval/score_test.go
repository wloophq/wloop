package eval

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/core/turns"
)

// judgedOut lays the synthetic judgments of testdata/judgments under a run
// of label "x", with every fixture of names in the manifest.
func judgedOut(t *testing.T, names []string, judged func(id string) bool) string {
	t.Helper()
	out := t.TempDir()
	var m manifest
	for _, id := range names {
		m.Fixtures = append(m.Fixtures, manifestEntry{ID: id, Split: Fixture{ID: id}.SplitOf()})
		if judged != nil && !judged(id) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("testdata", "judgments", id+".json"))
		if err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(out, "x", "judge", id+".json")
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeJSON(manifestPath(out, "x"), m); err != nil {
		t.Fatal(err)
	}
	return out
}

func synthNames() []string {
	var out []string
	for n := 1; n <= 30; n++ {
		out = append(out, fmt.Sprintf("%02d-synthetic", n))
	}
	return out
}

// Produced by loop.py's score over testdata/judgments.
var loopPyScore = map[string]SplitScore{
	SplitDev: {N: 20, SameActions: 0.7, CriticalOrMajor: 0, Recall: map[string]float64{"my_actions": 1.0, "my_decisions": 0.75, "chase": 0.78},
		Contradictions: 20, Extras: 20, Causes: map[string]int{"missing_item": 5, "wrong_assignee": 4, "wrong_waiting_on": 4, "spurious_item": 2,
			"wrong_state": 2, "wrong_resolution": 2, "missing_link": 2, "vague_summary": 2, "missing_context": 2, "other": 2, "wrong_verdict": 1}},
	SplitHoldout: {N: 10, SameActions: 0.8, CriticalOrMajor: 0, Recall: map[string]float64{"my_actions": 0.53, "my_decisions": 1.0, "chase": 1.0},
		Contradictions: 10, Extras: 50, Causes: map[string]int{"spurious_item": 2, "wrong_waiting_on": 2, "wrong_state": 2, "wrong_resolution": 2,
			"missing_link": 2, "missing_context": 2, "wrong_verdict": 2, "other": 2, "vague_summary": 1, "missing_item": 0, "wrong_assignee": 0}},
}

func TestScoreMatchesLoopPy(t *testing.T) {
	out := judgedOut(t, synthNames(), nil)
	s, err := WriteScore(out, "x")
	if err != nil {
		t.Fatal(err)
	}
	for split, want := range loopPyScore {
		got := s[split]
		if got.N != want.N || got.Expected != want.N || got.SameActions != want.SameActions || got.CriticalOrMajor != want.CriticalOrMajor ||
			got.Contradictions != want.Contradictions || got.Extras != want.Extras || !maps.Equal(got.Recall, want.Recall) || !maps.Equal(got.Causes, want.Causes) {
			t.Errorf("%s: got %+v\nwant %+v", split, got, want)
		}
	}
	if err := s.Bar(0.8); err != nil {
		t.Errorf("bar 0.8: %v", err)
	}
	if err := s.Bar(0.81); err == nil {
		t.Error("bar 0.81 passed at 0.8")
	}

	path := filepath.Join(out, "x", "judge", "03-synthetic.json")
	var j map[string]any
	if err := readJSON(path, &j); err != nil {
		t.Fatal(err)
	}
	j["severity"] = "major"
	if err := writeJSON(path, j); err != nil {
		t.Fatal(err)
	}
	if s, err = WriteScore(out, "x"); err != nil {
		t.Fatal(err)
	}
	if err := s.Bar(0.8); err == nil || !strings.Contains(err.Error(), "critical or major") {
		t.Errorf("bar with a major: %v", err)
	}
}

func TestBarRefusesPartialHoldout(t *testing.T) {
	var names []string
	for n := 1; n <= 10; n++ {
		names = append(names, fmt.Sprintf("%02d-h", 3*n))
	}
	out := t.TempDir()
	var m manifest
	for i, id := range names {
		m.Fixtures = append(m.Fixtures, manifestEntry{ID: id, Split: SplitHoldout})
		if i == 9 {
			continue
		}
		if err := writeJSON(filepath.Join(out, "x", "judge", id+".json"), Judgment{SameActions: true, Severity: "none"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeJSON(manifestPath(out, "x"), m); err != nil {
		t.Fatal(err)
	}
	s, err := WriteScore(out, "x")
	if err != nil {
		t.Fatal(err)
	}
	h := s[SplitHoldout]
	if h.N != 9 || h.Expected != 10 || h.SameActions != 0.9 || h.CriticalOrMajor != 1 {
		t.Errorf("holdout %+v", h)
	}
	if err := s.Bar(0.8); err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Errorf("bar on a partial holdout: %v", err)
	}
}

// optimizer answers the optimize call with prompt and records what it got.
type optimizer struct {
	prompt string
	got    []string
}

func (o *optimizer) Ask(_ context.Context, q turns.Ask) (turns.Answer, error) {
	o.got = append(o.got, q.Prompt)
	raw, _ := json.Marshal(map[string]any{"prompt": o.prompt, "changelog": []string{"traded a rule"}})
	c := 0.5
	return turns.Answer{JSON: raw, CostUSD: &c}, nil
}

func validTemplate() string { return strings.Join(Placeholders, "\nsome rule\n") }

func optimizeIn(t *testing.T, a turns.Asker) (OptimizeOptions, string) {
	out := judgedOut(t, synthNames(), nil)
	newFile := filepath.Join(t.TempDir(), "candidate.md")
	return OptimizeOptions{Asker: a, Out: out, Label: "x", Current: validTemplate(), New: newFile}, newFile
}

func TestOptimizeRefusesOverCap(t *testing.T) {
	long := validTemplate() + strings.Repeat(" word", MaxPromptWords)
	a := &optimizer{prompt: long}
	o, newFile := optimizeIn(t, a)
	_, _, err := Optimize(context.Background(), o)
	n := len(strings.Fields(long))
	if err == nil || !strings.Contains(err.Error(), fmt.Sprint(n)) {
		t.Fatalf("err %v, want the count %d", err, n)
	}
	if _, err := os.Stat(newFile); !os.IsNotExist(err) {
		t.Errorf("a candidate over the cap was written")
	}
	a.prompt = validTemplate()
	if _, _, err := Optimize(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(newFile); string(raw) != validTemplate() {
		t.Errorf("the candidate was not written")
	}
	if !strings.Contains(a.got[0], "at most "+words(MaxPromptWords)+" words") || strings.Contains(a.got[0], "{{CAP}}") {
		t.Errorf("the optimizer is not told the cap")
	}
}

func TestOptimizeSeesDevOnly(t *testing.T) {
	out := judgedOut(t, synthNames(), nil)
	for _, id := range synthNames() {
		path := filepath.Join(out, "x", "judge", id+".json")
		var j map[string]any
		if err := readJSON(path, &j); err != nil {
			t.Fatal(err)
		}
		j["prompt_fix_hints"] = []string{Fixture{ID: id}.SplitOf() + "-hint-" + id}
		if err := writeJSON(path, j); err != nil {
			t.Fatal(err)
		}
	}
	a := &optimizer{prompt: validTemplate()}
	if _, _, err := Optimize(context.Background(), OptimizeOptions{Asker: a, Out: out, Label: "x", Current: validTemplate(), New: filepath.Join(t.TempDir(), "c.md")}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(a.got[0], "holdout-hint") {
		t.Error("a holdout judgment reached the optimizer")
	}
	if !strings.Contains(a.got[0], "dev-hint-01-synthetic") {
		t.Error("the dev judgments did not reach the optimizer")
	}
}

func TestOptimizeKeepsPlaceholders(t *testing.T) {
	for _, p := range Placeholders {
		for name, tmpl := range map[string]string{
			"drops":   strings.Replace(validTemplate(), p, "", 1),
			"repeats": validTemplate() + "\n" + p,
		} {
			t.Run(name+" "+p, func(t *testing.T) {
				o, newFile := optimizeIn(t, &optimizer{prompt: tmpl})
				if _, _, err := Optimize(context.Background(), o); err == nil || !strings.Contains(err.Error(), p) {
					t.Fatalf("err %v, want it to name %s", err, p)
				}
				if _, err := os.Stat(newFile); !os.IsNotExist(err) {
					t.Error("a broken candidate was written")
				}
			})
		}
	}
}
