package expr

import (
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/workflow"
)

var sessionEnv = Env{
	"session.managed":     Bool,
	"session.idle_for":    Duration,
	"session.idle_since":  Time,
	"session.pendency":    List,
	"session.seq":         Int,
	"trigger.kind":        String,
	"config.cleanup.idle": Duration,
}

func TestPredicateErrorGoldens(t *testing.T) {
	turn := Env{"turn.round": String, "pr.mine": Bool, "pr.slug": String}
	action := Env{"row.open": Bool, "pr.mine": Bool, "pr.slug": String}
	cases := []struct {
		src  string
		env  Env
		want string
	}{
		{"", action, "1: empty predicate"},
		{"pr.slug", action, "1: predicate must be bool, got string"},
		{"turn.round == true", turn, "12: cannot compare string == bool"},
		{"row.open && pr.mien", action, "13: unknown field pr.mien; did you mean pr.mine?"},
		{"pr.mien", turn, "1: unknown field pr.mien; did you mean pr.mine?"},
		{`turn.round == "first"`, Env{"pr.mine": Bool}, "1: unknown field turn.round"},
	}
	for _, c := range cases {
		_, err := Compile(c.src, c.env)
		var e *Error
		if !errors.As(err, &e) {
			t.Fatalf("%q: %v", c.src, err)
		}
		if got := fmt.Sprintf("%d: %s", e.Col, e.Msg); got != c.want {
			t.Errorf("%q = %q, want %q", c.src, got, c.want)
		}
	}
}

func TestDurationsAndTimesCompare(t *testing.T) {
	now := time.Date(2026, 10, 21, 15, 0, 0, 0, time.UTC)
	p, err := CompileWorkflow(`session.idle_for > config.cleanup.idle && now - session.idle_since >= duration("90m") && session.idle_since < timestamp("2026-10-21T14:00:00Z")`, sessionEnv)
	if err != nil {
		t.Fatal(err)
	}
	v := workflow.Vars{
		Facts:  workflow.Facts{"session.idle_for": 3 * time.Hour, "session.idle_since": now.Add(-3 * time.Hour)},
		Config: workflow.Facts{"config.cleanup.idle": 2 * time.Hour},
		Now:    now,
	}
	if ok, err := p.Match(v); err != nil || !ok {
		t.Errorf("3h idle: %v %v", ok, err)
	}
	v.Facts["session.idle_for"] = time.Hour
	if ok, err := p.Match(v); err != nil || ok {
		t.Errorf("1h idle: %v %v", ok, err)
	}
	if ok, err := p.Match(workflow.Vars{Now: now}); err != nil || ok {
		t.Errorf("missing facts read as zero values: %v %v", ok, err)
	}

	d := compileEnv(t, `session.idle_for >= duration("2h30m")`, sessionEnv)
	if !d.Eval(mapFacts{"session.idle_for": 150 * time.Minute}) || d.Eval(mapFacts{}) {
		t.Error("duration comparison")
	}
	for src, msg := range map[string]string{
		`session.idle_for > duration("2x")`:          `invalid duration "2x"`,
		`session.idle_since > timestamp("tomorrow")`: `invalid timestamp "tomorrow"; use RFC 3339, as in "2026-10-21T09:00:00-03:00"`,
		`session.idle_for > session.idle_since`:      "cannot compare duration > timestamp",
		`session.idle_for`:                           "predicate must be bool, got duration",
	} {
		_, err := Compile(src, sessionEnv)
		var e *Error
		if !errors.As(err, &e) || e.Msg != msg {
			t.Errorf("%q: %v, want %q", src, err, msg)
		}
	}
}

func TestAWorkflowPredicateNamesItsReads(t *testing.T) {
	p, err := CompileWorkflow(`session.managed && session.idle_for > config.cleanup.idle && trigger.kind == "sweep" && now > session.idle_since && !session.managed && session.pendency.exists(x, x == "a")`, sessionEnv)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"config.cleanup.idle", "session.idle_for", "session.idle_since", "session.managed", "session.pendency", "trigger.kind"}
	if got := p.Reads(); !slices.Equal(got, want) {
		t.Errorf("Reads() = %v, want %v", got, want)
	}
	if _, err := CompileWorkflow("now", Env{}); err == nil {
		t.Error("a bare now is not a predicate")
	}
}

func TestListMembership(t *testing.T) {
	cases := []struct {
		src  string
		want bool
	}{
		{`"a" in session.pendency`, true},
		{`"z" in session.pendency`, false},
		{`session.pendency.exists(p, p == "b")`, true},
		{`size(session.pendency) == 2`, true},
		{`session.pendency == []`, false},
		{`"a" in ["a", "b"] && !("c" in [])`, true},
	}
	f := mapFacts{"session.pendency": []string{"a", "b"}}
	for _, c := range cases {
		if got := compileEnv(t, c.src, sessionEnv).Eval(f); got != c.want {
			t.Errorf("%q = %v, want %v", c.src, got, c.want)
		}
	}
	if !compileEnv(t, `session.pendency == []`, sessionEnv).Eval(mapFacts{}) {
		t.Error("a missing list reads empty")
	}
	if !compileEnv(t, `size(session.pendency) == 0`, sessionEnv).Eval(mapFacts{}) {
		t.Error("a missing list reads empty")
	}
}

func TestARuntimeErrorReadsFalseAndMatchReturnsIt(t *testing.T) {
	p := compileEnv(t, "session.seq > 1", sessionEnv)
	if p.Eval(mapFacts{"session.seq": "three"}) {
		t.Error("a wrongly typed fact reads false")
	}
	for _, src := range []string{`session.seq != 1`, `trigger.kind != "x"`, `!(trigger.kind in ["a"])`, `session.seq == 1`} {
		wrong := mapFacts{"session.seq": "1", "trigger.kind": 3}
		if src == `session.seq == 1` {
			wrong["session.seq"] = 1.0
		}
		if compileEnv(t, src, sessionEnv).Eval(wrong) {
			t.Errorf("%q reads true on a wrongly typed fact", src)
		}
		w, err := CompileWorkflow(src, sessionEnv)
		if err != nil {
			t.Fatal(err)
		}
		if ok, err := w.Match(workflow.Vars{Facts: workflow.Facts(wrong)}); ok || err == nil {
			t.Errorf("Match(%q) = %v, %v; want false and an error", src, ok, err)
		}
	}
	if !p.Eval(mapFacts{"session.seq": int64(3)}) || !p.Eval(mapFacts{"session.seq": 3}) {
		t.Error("int and int64 both read as int")
	}
	w, err := CompileWorkflow("session.seq > 1", sessionEnv)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := w.Match(workflow.Vars{Facts: workflow.Facts{"session.seq": "three"}}); ok || err == nil {
		t.Errorf("Match = %v, %v; want false and an error", ok, err)
	}
}

func compileEnv(t *testing.T, src string, env Env) *Program {
	t.Helper()
	p, err := Compile(src, env)
	if err != nil {
		t.Fatalf("Compile(%q): %v", src, err)
	}
	return p
}
