package expr

import (
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/workflow"
)

func enumEnv() Env {
	env := Env{"workspace.role": String, "pr.review.action": String, "pr.author": String, "turn.stage": String}
	env.SetEnum("workspace.role", []string{"author", "review", "none"})
	env.SetEnum("pr.review.action", []string{"", "follow-up", "complete"})
	return env
}

func TestEnumLiteralIsChecked(t *testing.T) {
	env := enumEnv()
	for src, want := range map[string]string{
		`workspace.role == "reviewer"`:                        `did you mean "review"?`,
		`"reviewer" != workspace.role`:                        `did you mean "review"?`,
		`pr.review.action in ["follow_up"]`:                   `did you mean "follow-up"?`,
		`pr.review.action in ["complete", "follow_up"]`:       `did you mean "follow-up"?`,
		`workspace.role == "author" && workspace.role == "x"`: `workspace.role is "author", "none" or "review", never "x"`,
	} {
		_, err := Compile(src, env)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", src, err, want)
		}
	}
	for _, src := range []string{
		`pr.review.action == "follow-up"`,
		`pr.review.action == ""`,
		`pr.review.action in ["follow-up", "complete"]`,
		`pr.author == "anyone"`,
		`workspace.role != "none"`,
	} {
		if _, err := Compile(src, env); err != nil {
			t.Errorf("%s: %v", src, err)
		}
	}
}

func TestAnEnumErrorPointsAtTheLiteral(t *testing.T) {
	_, err := Compile(`workspace.role == "reviewer"`, enumEnv())
	e, ok := err.(*Error)
	if !ok || e.Col != 19 {
		t.Fatalf("%#v", err)
	}
}

func TestDurationDaysInPredicates(t *testing.T) {
	env := Env{"session.idle_for": Duration}
	p, err := CompileWorkflow(`session.idle_for >= duration("1d") && duration("1d") == duration("24h")`, env)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CompileWorkflow(`session.idle_for > duration("2d")`, env); err != nil {
		t.Fatal(err)
	}
	for idle, want := range map[time.Duration]bool{23 * time.Hour: false, 24 * time.Hour: true, 30 * time.Hour: true} {
		ok, err := p.Match(workflow.Vars{Facts: workflow.Facts{"session.idle_for": idle}})
		if err != nil || ok != want {
			t.Errorf("idle %s: %v %v, want %v", idle, ok, err, want)
		}
	}
}

func TestAnErrorAfterADayLiteralPointsAtTheSource(t *testing.T) {
	_, err := CompileWorkflow(`duration("1d") < duration("2d") && nope`, Env{})
	e, ok := err.(*Error)
	if !ok || e.Col != 36 || !strings.Contains(e.Snippet, `duration("1d")`) {
		t.Fatalf("%#v", err)
	}
}

func TestEnvMetadataIsNoFact(t *testing.T) {
	env := enumEnv()
	for _, n := range env.Names() {
		if strings.ContainsRune(n, 0) {
			t.Errorf("Names lists %q", n)
		}
	}
	if len(env.Names()) != 4 {
		t.Errorf("%v", env.Names())
	}
	if _, err := Compile(`workspace.rol == "author"`, env); err == nil || !strings.Contains(err.Error(), "did you mean \"workspace.role\"?") {
		t.Errorf("%v", err)
	}
}
