package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/source/slack/extract"
	"github.com/wloophq/wloop/internal/core/turns"
)

const fixturesDir = "testdata/fixtures"

// scripted answers each stage by its schema and records every call.
type scripted struct {
	mu    sync.Mutex
	set   Set
	cost  float64
	calls []turns.Ask
}

func (s *scripted) Ask(_ context.Context, q turns.Ask) (turns.Answer, error) {
	s.mu.Lock()
	s.calls = append(s.calls, q)
	s.mu.Unlock()
	c := s.cost
	var out any
	switch q.Schema {
	case briefSchema:
		out = map[string]any{"my_actions": []any{}, "my_decisions": []any{}, "chase": []any{}, "resolved": []any{"brief marker"},
			"can_leave_list": false, "missing_to_leave": "", "urgency": "low", "urgency_why": ""}
	case judgeSchema:
		sec := map[string]any{"reference_count": 1, "matched": 1, "missed": []any{}, "extra": []any{}, "contradicted": []any{}}
		out = map[string]any{"same_actions": true, "severity": "none",
			"sections":         map[string]any{"my_actions": sec, "my_decisions": sec, "chase": sec, "resolved": sec, "leave": sec},
			"reference_errors": []any{}, "ledger_root_causes": []any{}, "prompt_fix_hints": []any{}}
	case extract.Schema():
		f := s.fixtureOf(q.Prompt)
		out = extraction(f)
	default:
		return turns.Answer{}, fmt.Errorf("unexpected schema")
	}
	raw, _ := json.Marshal(out)
	return turns.Answer{JSON: raw, CostUSD: &c, Model: "scripted"}, nil
}

func (s *scripted) fixtureOf(prompt string) Fixture {
	for _, f := range s.set.Fixtures {
		if strings.Contains(prompt, "ts="+f.Messages[0].TS+" ") {
			return f
		}
	}
	return Fixture{}
}

func extraction(f Fixture) map[string]any {
	origin := ""
	if len(f.Messages) > 0 {
		origin = f.Messages[0].TS
	}
	item := map[string]any{"ref": "n1", "kind": "request", "summary": "summary of " + f.ID, "origin_ts": origin,
		"requester": "U0000000002", "assignees": []any{"U0000000001"}, "assignment_ts": origin, "assignment_basis": "explicit_mention",
		"waiting_on": []any{"U0000000001"}, "state": "open", "resolution": "none", "evidence_ts": "", "evidence_kind": "none",
		"confidence": "high", "ambiguity": "", "links": []any{}, "context": "", "options": []any{}, "deadline": "",
		"mode": "none", "place": map[string]any{"context": "", "why": ""}}
	return map[string]any{"brief": "brief of " + f.ID, "viewer_role": "assignee", "started_by_viewer": false, "items": []any{item},
		"verdict": "waits_on_viewer", "verdict_why": "", "closure_posted": false, "segments": []any{}, "links": []any{}}
}

func (s *scripted) byStage() map[string][]turns.Ask {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string][]turns.Ask{}
	for _, q := range s.calls {
		switch {
		case q.Schema == judgeSchema:
			out["judge"] = append(out["judge"], q)
		case q.Schema == extract.Schema():
			out["ledger"] = append(out["ledger"], q)
		case strings.Contains(q.Prompt, "## What the tool shows"):
			out["blind"] = append(out["blind"], q)
		default:
			out["oracle"] = append(out["oracle"], q)
		}
	}
	return out
}

func loadSet(t *testing.T, dir string) Set {
	t.Helper()
	s, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func options(t *testing.T, a turns.Asker, s Set) Options {
	return Options{Asker: a, Set: s, Out: t.TempDir(), Label: "shipped", MaxUSD: 20, Jobs: 3,
		ExtractorTier: turns.TierFast, JudgeTier: turns.TierFrontier, Log: testLog{t}}
}

type testLog struct{ t *testing.T }

func (l testLog) Write(p []byte) (int, error) {
	l.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

func inputOf(t *testing.T, o Options, f Fixture) extract.Input {
	t.Helper()
	now, err := f.NowAt(o.Now)
	if err != nil {
		t.Fatal(err)
	}
	return f.Input(o.Set.Viewer, o.names(), now, cmpOr(o.Language, "English"))
}

func TestRunWritesEveryStage(t *testing.T) {
	set := loadSet(t, fixturesDir)
	a := &scripted{set: set, cost: 0.01}
	o := options(t, a, set)
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	for _, f := range set.Fixtures {
		for _, p := range []string{filepath.Join(o.Out, "oracle"), filepath.Join(o.Out, "shipped", "ledger"), filepath.Join(o.Out, "shipped", "blind"), filepath.Join(o.Out, "shipped", "judge")} {
			for _, file := range []string{f.ID + ".json", f.ID + ".key.json"} {
				if _, err := os.Stat(filepath.Join(p, file)); err != nil {
					t.Errorf("missing %s", filepath.Join(p, file))
				}
			}
		}
	}
	stages := a.byStage()
	for _, st := range []string{"oracle", "ledger", "blind", "judge"} {
		if len(stages[st]) != len(set.Fixtures) {
			t.Fatalf("%s: %d calls, want %d", st, len(stages[st]), len(set.Fixtures))
		}
	}
	for _, f := range set.Fixtures {
		in := inputOf(t, o, f)
		conv := extract.Render(Plain(in))
		want, _ := extract.Prompt(in)
		found := map[string]bool{}
		for _, q := range stages["oracle"] {
			found["oracle"] = found["oracle"] || strings.Contains(q.Prompt, conv)
		}
		for _, q := range stages["ledger"] {
			found["ledger"] = found["ledger"] || q.Prompt == want
		}
		for _, q := range stages["blind"] {
			if !strings.Contains(q.Prompt, "summary of "+f.ID+"\n") {
				continue
			}
			found["blind"] = true
			for _, m := range f.Messages {
				if strings.Contains(q.Prompt, m.Text) {
					t.Errorf("%s: the blind prompt holds a message's text", f.ID)
				}
			}
		}
		for _, q := range stages["judge"] {
			if strings.Contains(q.Prompt, conv) && strings.Contains(q.Prompt, "brief marker") &&
				strings.Contains(q.Prompt, "summary of "+f.ID+"\n") && strings.Contains(q.Prompt, "## BLIND") {
				found["judge"] = true
			}
		}
		for _, st := range []string{"oracle", "ledger", "blind", "judge"} {
			if !found[st] {
				t.Errorf("%s: no %s call got its inputs", f.ID, st)
			}
		}
	}
	var s Score
	if err := readJSON(filepath.Join(o.Out, "shipped", "score.json"), &s); err != nil {
		t.Fatal(err)
	}
	if s[SplitDev].N != 4 || s[SplitHoldout].N != 2 || s[SplitHoldout].SameActions != 1 {
		t.Errorf("score %+v", s)
	}
}

func TestStagesAreCached(t *testing.T) {
	set := loadSet(t, fixturesDir)
	a := &scripted{set: set}
	o := options(t, a, set)
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	first := len(a.calls)
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if len(a.calls) != first {
		t.Errorf("the second run made %d calls", len(a.calls)-first)
	}
	o.Label = "other"
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if got := a.byStage()["oracle"]; len(got) != len(set.Fixtures) {
		t.Errorf("a second label asked the oracle again: %d calls", len(got))
	}
}

func TestCacheKeyMismatchRefuses(t *testing.T) {
	set := loadSet(t, fixturesDir)
	for _, tc := range []struct {
		name   string
		change func(*Options)
	}{
		{"language", func(o *Options) { o.Language = "Portuguese" }},
		{"now", func(o *Options) { o.Now = "2026-12-01T08:00:00Z" }},
		{"tier", func(o *Options) { o.JudgeTier = turns.TierBalanced }},
		{"prompt", func(o *Options) { o.Template = "{{VIEWER_ID}} {{CONVERSATION}}" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &scripted{set: set}
			o := options(t, a, set)
			if _, err := Run(context.Background(), o); err != nil {
				t.Fatal(err)
			}
			first := len(a.calls)
			tc.change(&o)
			_, err := Run(context.Background(), o)
			var stale *StaleError
			if !errors.As(err, &stale) || !strings.Contains(err.Error(), o.Out) {
				t.Fatalf("err %v, want a stale file under %s", err, o.Out)
			}
			if len(a.calls) != first {
				t.Errorf("the refused run made %d calls", len(a.calls)-first)
			}
		})
	}
}

func TestTiersAndBudget(t *testing.T) {
	set := loadSet(t, fixturesDir)
	a := &scripted{set: set, cost: 0.1}
	o := options(t, a, set)
	o.ExtractorTier, o.JudgeTier = turns.TierBalanced, turns.TierFrontier
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	for st, qs := range a.byStage() {
		want := turns.TierFrontier
		if st == "ledger" {
			want = turns.TierBalanced
		}
		for _, q := range qs {
			if q.Tier != want || q.MaxCostUSD <= 0 {
				t.Errorf("%s asked tier %s with cap %v, want %s with a cap", st, q.Tier, q.MaxCostUSD, want)
			}
		}
	}

	a = &scripted{set: set, cost: 0.01}
	o = options(t, a, set)
	o.MaxUSD, o.Jobs = 1.5, 4
	if _, err := Run(context.Background(), o); err != nil {
		t.Fatalf("calls in flight stopped a run that fits: %v", err)
	}

	a = &scripted{set: set, cost: 0.1}
	o = options(t, a, set)
	o.MaxUSD, o.Jobs = 2.0, 2
	_, err := Run(context.Background(), o)
	if !errors.Is(err, ErrBudget) {
		t.Fatalf("err %v, want the budget stop", err)
	}
	if spent := 0.1 * float64(len(a.calls)); spent > o.MaxUSD || len(a.calls) == 0 || len(a.calls) >= 4*len(set.Fixtures) {
		t.Errorf("%d calls for $%.2f under a cap of $%.2f", len(a.calls), spent, o.MaxUSD)
	}
	if _, err := os.Stat(filepath.Join(o.Out, "shipped", "score.json")); err != nil {
		t.Errorf("no score after the budget stop: %v", err)
	}
}

func TestFixtureNow(t *testing.T) {
	dir := t.TempDir()
	set := loadSet(t, fixturesDir)
	for _, f := range set.Fixtures {
		if f.ID == "01-thread-release-checklist" {
			f.Now = "2026-11-02T10:00:00-03:00"
		}
		if err := writeJSON(filepath.Join(dir, f.ID+".json"), f); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeJSON(filepath.Join(dir, viewerFile), map[string]string{"id": set.Viewer.ID, "name": set.Viewer.Name}); err != nil {
		t.Fatal(err)
	}
	set = loadSet(t, dir)
	for _, tc := range []struct {
		override, own, others string
	}{
		{"", "2026-11-02 10:00", "2026-10-21 21:00"},
		{"2026-12-01T08:00:00Z", "2026-12-01 08:00", "2026-12-01 08:00"},
	} {
		a := &scripted{set: set}
		o := options(t, a, set)
		o.Now = tc.override
		if _, err := Run(context.Background(), o); err != nil {
			t.Fatal(err)
		}
		for _, q := range a.calls {
			isOwn := strings.Contains(q.Prompt, "Release checklist") || strings.Contains(q.Prompt, "summary of 01-")
			want := tc.others
			if isOwn {
				want = tc.own
			}
			if !strings.Contains(q.Prompt, "Now is "+want) {
				t.Errorf("override %q: a call lacks now %s", tc.override, want)
			}
		}
	}
}

func TestSplitRule(t *testing.T) {
	for id, want := range map[string]string{"01-a": SplitDev, "02-b": SplitDev, "03-c": SplitHoldout, "12-d": SplitHoldout, "13-e": SplitDev, "xa": SplitDev} {
		if got := (Fixture{ID: id}).SplitOf(); got != want {
			t.Errorf("%s: %s, want %s", id, got, want)
		}
	}
	if got := (Fixture{ID: "03-c", Split: SplitDev}).SplitOf(); got != SplitDev {
		t.Errorf("an explicit split is overridden: %s", got)
	}
	dir := t.TempDir()
	if err := writeJSON(filepath.Join(dir, "03-named.json"), Fixture{ID: "another-id", Messages: []Message{{TS: "1792500000.000100"}}}); err != nil {
		t.Fatal(err)
	}
	if f := loadSet(t, dir).Fixtures[0]; f.ID != "03-named" || f.SplitOf() != SplitHoldout {
		t.Errorf("the file name is not the id: %s %s", f.ID, f.SplitOf())
	}
	set := loadSet(t, fixturesDir)
	o := Options{Set: set, Split: SplitHoldout}
	if got := o.selected(); len(got) != 2 || got[0].ID[:2] != "03" || got[1].ID[:2] != "06" {
		t.Errorf("holdout selects %d fixtures", len(got))
	}
}

func TestExtractorPromptMatchesShipped(t *testing.T) {
	raw, err := os.ReadFile("../../../../../profiles/default/prompts/slack-extraction.md")
	if err != nil {
		t.Fatal(err)
	}
	set := loadSet(t, fixturesDir)
	o := Options{Set: set}
	for _, f := range set.Fixtures {
		in := inputOf(t, o, f)
		want, _ := extract.Prompt(in)
		got, _ := ExtractorPrompt(string(raw), in)
		if got != want {
			t.Errorf("%s: the template fill differs from extract.Prompt", f.ID)
		}
	}
	bare, _ := ExtractorPrompt("{{VIEWER_ID}} {{NOW}}\n{{CONVERSATION}}", inputOf(t, o, set.Fixtures[0]))
	if !strings.Contains(bare, "## Schema notes") {
		t.Errorf("a template without a ledger gets no schema notes")
	}
}

var (
	slackID = regexp.MustCompile(`\b[UCWTBDG][A-Z0-9]*[0-9][A-Z0-9]*\b`)
	urlHost = regexp.MustCompile(`https?://([^/\s"'<>|)]+)`)
)

func TestFixturesAreSynthetic(t *testing.T) {
	var files []string
	for _, g := range []string{"testdata/*/*.json", "*.md", "*.json"} {
		m, _ := filepath.Glob(g)
		files = append(files, m...)
	}
	if len(files) < 10 {
		t.Fatalf("only %d files", len(files))
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range slackID.FindAllString(string(raw), -1) {
			if !regexp.MustCompile(`^[UCT]000`).MatchString(id) {
				t.Errorf("%s holds the id %q", f, id)
			}
		}
		for _, h := range urlHost.FindAllStringSubmatch(string(raw), -1) {
			if h[1] != "example.com" {
				t.Errorf("%s holds the host %q", f, h[1])
			}
		}
	}
	set := loadSet(t, fixturesDir)
	kinds := map[string]bool{}
	for _, f := range set.Fixtures {
		kinds[f.Kind] = true
	}
	if len(set.Fixtures) < 6 || !kinds["thread"] || !kinds["dm_segment"] || !kinds["mention"] {
		t.Errorf("fixtures cover %v", kinds)
	}
	for name, tmpl := range map[string]string{"oracle": oracleTemplate, "blind": blindTemplate, "judge": judgeTemplate} {
		for _, p := range []string{"{{VIEWER_ID}}", "{{VIEWER_NAME}}", "{{NOW}}", "{{LANGUAGE}}"} {
			if !strings.Contains(tmpl, p) {
				t.Errorf("%s.md lacks %s", name, p)
			}
		}
	}
}
