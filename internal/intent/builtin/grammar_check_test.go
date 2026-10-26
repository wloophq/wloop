package builtin

import (
	"strconv"
	"strings"
	"testing"
)

func roleValues() []Value {
	return []Value{{Name: "pr.review"}, {Name: "pr.address"}, {Name: "probe"}}
}

func modeValues() []Value {
	return []Value{{Name: "send"}, {Name: "wait"}, {Name: "delay"}, {Name: "dry_run"}}
}

var testRows = []Key{
	{Path: "provider", In: SurfaceWorkflow, Kind: KindText},
	{Path: "summary", In: SurfaceWorkflow, Kind: KindText},
	{Path: "observe", In: SurfaceWorkflow, Kind: KindBool},
	{Path: "drafts", In: SurfaceWorkflow, Kind: KindTable},
	{Path: "drafts.approval", In: SurfaceWorkflow, Kind: KindTable},
	{Path: "drafts.approval.mode", In: SurfaceWorkflow, Kind: KindEnum, Values: modeValues},
	{Path: "drafts.approval.window", In: SurfaceWorkflow, Kind: KindDur},
	{Path: "drafts.approval.rule[]", In: SurfaceWorkflow, Kind: KindTable},
	{Path: "drafts.approval.rule[].when", In: SurfaceWorkflow, Kind: KindPred},
	{Path: "drafts.approval.rule[].mode", In: SurfaceWorkflow, Kind: KindEnum, Values: modeValues},
	{Path: "runners", In: SurfaceIntent, Kind: KindList, Elem: KindText},
	{Path: "effects", In: SurfaceIntent, Kind: KindList, Elem: KindText},
	{Path: "role", In: SurfaceIntent, Kind: KindEnum, Values: roleValues},
	{Path: "inputs", In: SurfaceIntent, Kind: KindMap},
	{Path: "inputs.*.type", In: SurfaceIntent, Kind: KindEnum, Values: func() []Value { return []Value{{Name: "bool"}, {Name: "text"}} }},
	{Path: "resume.changed", In: SurfaceIntent, Kind: KindTemplate},
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

func TestCheckRefusesAnUnknownKey(t *testing.T) {
	wants(t, check(testRows, SurfaceWorkflow, map[string]any{"provder": "github", "zzz": 1, "summary": "s"}),
		"provder: unknown key; did you mean provider?", "zzz: unknown key")
	wants(t, check(testRows, SurfaceIntent, map[string]any{"resume": map[string]any{"head_moved": "t"}}), "resume.head_moved: unknown key")
	wants(t, check(testRows, SurfaceIntent, map[string]any{"inputs": map[string]any{"a": map[string]any{"typ": "text"}}}),
		"inputs.a.typ: unknown key; did you mean type?")
}

func TestCheckRefusesAnUnknownValue(t *testing.T) {
	wants(t, check(testRows, SurfaceIntent, map[string]any{"role": "pr.reviw"}), `role: "pr.reviw" is not one of "pr.review", "pr.address" or "probe"; did you mean "pr.review"?`)
	wants(t, check(testRows, SurfaceIntent, map[string]any{"inputs": map[string]any{"self_review": map[string]any{"type": "flag"}}}),
		`inputs.self_review.type: "flag" is not one of "bool" or "text"`)
	wants(t, check(testRows, SurfaceWorkflow, map[string]any{"drafts": map[string]any{"approval": map[string]any{"mode": "dry-run",
		"rule": []any{map[string]any{"when": "true", "mode": "wiat"}}}}}),
		`drafts.approval.mode: "dry-run" is not one of "send", "wait", "delay" or "dry_run"; did you mean "dry_run"?`,
		`drafts.approval.rule[0].mode: "wiat" is not one of "send", "wait", "delay" or "dry_run"; did you mean "wait"?`)
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
		case KindNumber:
			n, err := strconv.ParseFloat(k.Default, 64)
			if err != nil {
				t.Errorf("%s %s: default %q is no number", k.In, k.Path, k.Default)
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

func TestCheckTakesNullAsUnset(t *testing.T) {
	doc := map[string]any{"runners": nil, "inputs": nil, "resume": nil, "role": nil}
	wants(t, check(testRows, SurfaceIntent, doc))
	wants(t, check(testRows, SurfaceConfig, map[string]any{"turns": map[string]any{"per_day": nil}}))
}

func TestCheckReadsANumber(t *testing.T) {
	rows := []Key{{Path: "per_call_usd", In: SurfaceConfig, Kind: KindNumber}}
	wants(t, check(rows, SurfaceConfig, map[string]any{"per_call_usd": 0.5}))
	wants(t, check(rows, SurfaceConfig, map[string]any{"per_call_usd": int64(2)}))
	wants(t, check(rows, SurfaceConfig, map[string]any{"per_call_usd": "0.5"}), "per_call_usd: must be a number")
}

func TestCheckStepReadsOnlyItsStepsArguments(t *testing.T) {
	wants(t, CheckStep(StepNotifySelf, map[string]any{"text": "a.md"}))
	wants(t, CheckStep(StepNotifySelf, map[string]any{"reason": "x"}), "reason: unknown key")
	wants(t, CheckStep(StepTurnStart, map[string]any{"intent": "review", "stage": "frist"}), `stage: "frist" is not one of`)
	wants(t, CheckStep("session.close", map[string]any{"reason": "x"}), "reason: unknown key")
}
