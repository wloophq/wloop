package archtest

import "testing"

func TestR1KnowsEveryWorkspaceManager(t *testing.T) {
	t.Parallel()
	_, f := parse(t, `package cli
import "github.com/wloophq/wloop/internal/workspace/zed"
`)
	if got := ruleR1("internal/cli/round.go", f); len(got) != 1 {
		t.Fatalf("a second manager imported by cli: %q", got)
	}
	for _, file := range []string{"internal/adapter/zed/zed.go", "internal/workspace/zed/zed_more.go", "internal/cli/root.go"} {
		if got := ruleR1(file, f); len(got) != 0 {
			t.Errorf("%s: %q", file, got)
		}
	}
	_, fakeImport := parse(t, `package cli
import "github.com/wloophq/wloop/internal/workspace/fake"
`)
	if got := ruleR1("internal/cli/round.go", fakeImport); len(got) != 0 {
		t.Errorf("the fake manager is no provider: %q", got)
	}
	_, port := parse(t, `package cli
import "github.com/wloophq/wloop/internal/workspace"
`)
	if got := ruleR1("internal/cli/round.go", port); len(got) != 0 {
		t.Errorf("the workspace port is no provider: %q", got)
	}
}

func TestR2FlagsTheManagersName(t *testing.T) {
	t.Parallel()
	fset, f := parse(t, `package cli
const a = "orca"
const b = "open the Orca app"
const c = "views.orca.columns.%s"
const d = "orcas and torcal are fine"
`)
	if got := ruleR2(fset, "internal/cli/x.go", f, nil, map[int]bool{}); len(got) != 3 {
		t.Fatalf("flagged %q", got)
	}
	for _, file := range []string{"internal/workspace/zed/zed.go", "internal/adapter/zed/zed.go", "internal/core/turns/turnstest/sessions.go"} {
		if got := ruleR2(fset, file, f, nil, map[int]bool{}); len(got) != 0 {
			t.Errorf("%s flagged: %q", file, got)
		}
	}
}

func TestR2ExemptsTheFakesByPathNotByName(t *testing.T) {
	t.Parallel()
	fset, f := parse(t, `package latest
const a = "claude"
`)
	if got := ruleR2(fset, "internal/latest/latest.go", f, nil, map[int]bool{}); len(got) != 1 {
		t.Fatalf("a package named latest: %q", got)
	}
	if got := ruleR2(fset, "internal/workspace/fake/fake.go", f, nil, map[int]bool{}); len(got) != 1 {
		t.Fatalf("the fake manager: %q", got)
	}
}

func TestR3FlagsADotImportAndUnmarshalText(t *testing.T) {
	t.Parallel()
	_, dot := parse(t, `package cli
import . "github.com/wloophq/wloop/internal/domain/turn"
var s = ParseAgentState("done")
`)
	if got := ruleR3("internal/cli/watch.go", dot); len(got) != 1 {
		t.Fatalf("dot import: %q", got)
	}
	_, text := parse(t, `package cli
import "github.com/wloophq/wloop/internal/domain/turn"
func f(s *turn.AgentState) error { return s.UnmarshalText([]byte("done")) }
`)
	if got := ruleR3("internal/cli/watch.go", text); len(got) != 1 {
		t.Fatalf("UnmarshalText: %q", got)
	}
	for _, file := range []string{"internal/adapter/store/inventory/store.go", "internal/workspace/zed/zed.go"} {
		if got := ruleR3(file, text); len(got) != 0 {
			t.Errorf("%s: %q", file, got)
		}
	}
}
