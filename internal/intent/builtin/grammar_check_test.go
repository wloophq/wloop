package builtin

import (
	"slices"
	"strconv"
	"strings"
	"testing"
)

func roleValues() []Value {
	return []Value{{Name: "pr.review", Was: []string{"round.review"}}, {Name: "pr.address", Was: []string{"round.address"}}, {Name: "probe"}}
}

func modeValues() []Value {
	return []Value{{Name: "send"}, {Name: "wait"}, {Name: "delay"}, {Name: "dry_run", Was: []string{"record"}}}
}

var testRows = []Key{
	{Path: "provider", In: SurfaceWorkflow, Kind: KindText, Was: []string{"subject"}},
	{Path: "summary", In: SurfaceWorkflow, Kind: KindText, Was: []string{"description"}},
	{Path: "observe", In: SurfaceWorkflow, Kind: KindBool},
	{Path: "drafts", In: SurfaceWorkflow, Kind: KindTable},
	{Path: "drafts.approval", In: SurfaceWorkflow, Kind: KindTable, Was: []string{"draft_approval"}},
	{Path: "drafts.approval.mode", In: SurfaceWorkflow, Kind: KindEnum, Values: modeValues},
	{Path: "drafts.approval.window", In: SurfaceWorkflow, Kind: KindDur},
	{Path: "drafts.approval.rule[]", In: SurfaceWorkflow, Kind: KindTable},
	{Path: "drafts.approval.rule[].when", In: SurfaceWorkflow, Kind: KindPred},
	{Path: "drafts.approval.rule[].mode", In: SurfaceWorkflow, Kind: KindEnum, Values: modeValues},
	{Path: "runners", In: SurfaceIntent, Kind: KindList, Elem: KindText, Was: []string{"modes"}},
	{Path: "effects", In: SurfaceIntent, Kind: KindList, Elem: KindText, Was: []string{"on_sent"}},
	{Path: "role", In: SurfaceIntent, Kind: KindEnum, Values: roleValues},
	{Path: "inputs", In: SurfaceIntent, Kind: KindMap},
	{Path: "inputs.*.type", In: SurfaceIntent, Kind: KindEnum, Values: func() []Value { return []Value{{Name: "bool", Was: []string{"flag"}}, {Name: "text"}} }},
	{Path: "resume.changed", In: SurfaceIntent, Kind: KindTemplate, Was: []string{"resume.head_moved"}},
	{Path: "resume", In: SurfaceIntent, Kind: KindTable},
	{Path: "context", In: SurfaceIntent, Kind: KindText, Bundled: true},
	{Path: "workflows", In: SurfaceConfig, Kind: KindMap},
	{Path: "workflows.*.only_when", In: SurfaceConfig, Kind: KindPred},
	{Path: "turns.per_day", In: SurfaceConfig, Kind: KindInt},
	{Path: "turns", In: SurfaceConfig, Kind: KindTable},
}

func messages(ps []Problem) string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Path+": "+p.Msg)
	}
	return strings.Join(out, "\n")
}

func wants(t *testing.T, ps []Problem, want ...string) {
	t.Helper()
	got := messages(ps)
	for _, w := range want {
		if !strings.Contains(got, w) {
			t.Errorf("no %q in:\n%s", w, got)
		}
	}
	if len(ps) != len(want) {
		t.Errorf("%d problems, want %d:\n%s", len(ps), len(want), got)
	}
}

func TestCheckNamesTheNewKey(t *testing.T) {
	wants(t, check(testRows, SurfaceWorkflow, map[string]any{"subject": "github", "provder": "github", "summary": "s"}),
		"subject: subject was renamed provider", "provder: unknown key; did you mean provider?")
	wants(t, check(testRows, SurfaceIntent, map[string]any{"modes": []any{"session"}, "on_sent": []any{"x"}, "resume": map[string]any{"head_moved": "t"}}),
		"modes: modes was renamed runners", "on_sent: on_sent was renamed effects", "resume.head_moved: resume.head_moved was renamed resume.changed")
	wants(t, check(testRows, SurfaceWorkflow, map[string]any{"draft_approval": map[string]any{"mode": "wait"}}),
		"draft_approval: draft_approval was renamed drafts.approval")
}

func TestCheckNamesTheNewValue(t *testing.T) {
	wants(t, check(testRows, SurfaceIntent, map[string]any{"role": "round.review"}), `role: "round.review" was renamed; did you mean "pr.review"?`)
	wants(t, check(testRows, SurfaceIntent, map[string]any{"role": "pr.reviw"}), `role: "pr.reviw" is not one of "pr.review", "pr.address" or "probe"; did you mean "pr.review"?`)
	wants(t, check(testRows, SurfaceIntent, map[string]any{"inputs": map[string]any{"self_review": map[string]any{"type": "flag"}}}),
		`inputs.self_review.type: "flag" was renamed; did you mean "bool"?`)
	wants(t, check(testRows, SurfaceWorkflow, map[string]any{"drafts": map[string]any{"approval": map[string]any{"mode": "record",
		"rule": []any{map[string]any{"when": "true", "mode": "record"}}}}}),
		`drafts.approval.mode: "record" was renamed; did you mean "dry_run"?`, `drafts.approval.rule[0].mode: "record" was renamed; did you mean "dry_run"?`)
}

func TestCheckReadsKinds(t *testing.T) {
	wants(t, check(testRows, SurfaceWorkflow, map[string]any{"observe": "true", "drafts": map[string]any{"approval": map[string]any{"window": "1x"}}}),
		"observe: must be true or false", "drafts.approval.window: ")
	wants(t, check(testRows, SurfaceIntent, map[string]any{"runners": "session"}), "runners: must be a list")
	wants(t, check(testRows, SurfaceConfig, map[string]any{"turns": map[string]any{"per_day": 2.5}}), "turns.per_day: must be an integer")
	wants(t, check(testRows, SurfaceConfig, map[string]any{"turns": map[string]any{"per_day": float64(20)}, "workflows": map[string]any{"x": map[string]any{"only_when": "true"}}}))
	wants(t, check(testRows, SurfaceWorkflow, map[string]any{"drafts": map[string]any{"approval": map[string]any{"window": "1d", "mode": "delay"}}}))
}

func TestCheckUserRefusesABundledKey(t *testing.T) {
	doc := map[string]any{"context": "synthetic"}
	wants(t, check(testRows, SurfaceIntent, doc))
	wants(t, checkUser(testRows, SurfaceIntent, doc), "context: only the bundled profile sets context")
}

func TestASurfaceWithNoRowsIsNotChecked(t *testing.T) {
	if ps := check(testRows, SurfaceLabels, map[string]any{"anything": 1}); ps != nil {
		t.Fatalf("%v", ps)
	}
}

func TestKeyMessageNamesTheReplacement(t *testing.T) {
	for path, want := range map[string]string{
		"subject":           "subject was renamed provider",
		"resume.head_moved": "resume.head_moved was renamed resume.changed",
		"provder":           "",
	} {
		if got := keyMessage(testRows, SurfaceWorkflow, strings.Split(path, ".")); path == "resume.head_moved" {
			if got := keyMessage(testRows, SurfaceIntent, strings.Split(path, ".")); got != want {
				t.Errorf("%s: %q, want %q", path, got, want)
			}
		} else if got != want {
			t.Errorf("%s: %q, want %q", path, got, want)
		}
	}
}

func TestNoRemovedKeyReturns(t *testing.T) {
	if r := returnedKeys(Grammar); len(r) > 0 {
		t.Errorf("%v", r)
	}
	planted := append(slices.Clone(testRows), Key{Path: "subject", In: SurfaceWorkflow, Kind: KindTable})
	if r := returnedKeys(planted); !slices.Equal(r, []string{"workflow: subject was provider's, and returns as a table"}) {
		t.Errorf("%v", r)
	}
}

func TestEveryPredKeyHasTheQuotingHint(t *testing.T) {
	n := 0
	for _, k := range Grammar {
		if k.Kind != KindPred {
			continue
		}
		n++
		last := k.Path[strings.LastIndex(k.Path, ".")+1:]
		if !IsPredKey(last) {
			t.Errorf("%s %s: no quoting hint", k.In, k.Path)
		}
	}
	if !IsPredKey("only_when") || IsPredKey("label") {
		t.Error("only_when has no hint, or label has one")
	}
	if n == 0 {
		t.Fatal("no predicate key in the grammar")
	}
}

func TestGrammarDefaultsAreValid(t *testing.T) {
	for _, k := range Grammar {
		if k.Default == "" {
			continue
		}
		var v any = k.Default
		switch k.Kind {
		case KindBool:
			v = k.Default == "true"
			if k.Default != "true" && k.Default != "false" {
				t.Errorf("%s %s: default %q is no bool", k.In, k.Path, k.Default)
			}
		case KindInt:
			n, err := strconv.Atoi(k.Default)
			if err != nil {
				t.Errorf("%s %s: default %q is no integer", k.In, k.Path, k.Default)
			}
			v = n
		case KindList, KindTable, KindMap:
			t.Errorf("%s %s: a %s has no default", k.In, k.Path, kindName(k.Kind))
			continue
		}
		w := walker{}
		w.leaf(k, k.Path, v)
		for _, p := range w.problems {
			t.Errorf("%s %s: default %q: %s", k.In, k.Path, k.Default, p.Msg)
		}
	}
}
