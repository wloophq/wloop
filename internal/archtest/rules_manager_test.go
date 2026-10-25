package archtest

import "testing"

func TestR1KnowsEveryWorkspaceManager(t *testing.T) {
	t.Parallel()
	_, f := parse(t, `package cli
import "github.com/wloophq/wloop/internal/workspace/zed"
`)
	flagged(t, "a second manager imported by cli", ruleR1("internal/cli/round.go", f), 1, "")
	for _, file := range []string{"internal/adapter/zed/zed.go", "internal/workspace/zed/zed_more.go", "internal/cli/root.go"} {
		flagged(t, file, ruleR1(file, f), 0, "")
	}
	_, fakeImport := parse(t, `package cli
import "github.com/wloophq/wloop/internal/workspace/fake"
`)
	flagged(t, "the fake manager is no provider", ruleR1("internal/cli/round.go", fakeImport), 0, "")
	_, port := parse(t, `package cli
import "github.com/wloophq/wloop/internal/workspace"
`)
	flagged(t, "the workspace port is no provider", ruleR1("internal/cli/round.go", port), 0, "")
}

func TestR2FlagsTheManagersName(t *testing.T) {
	t.Parallel()
	fset, f := parse(t, `package cli
const a = "orca"
const b = "open the Orca app"
const c = "views.orca.columns.%s"
const d = "orcas and torcal are fine"
`)
	flagged(t, "the cli", ruleR2(fset, "internal/cli/x.go", f, nil, map[int]bool{}), 3, "")
	for _, file := range []string{"internal/workspace/zed/zed.go", "internal/adapter/zed/zed.go", "internal/core/turns/turnstest/sessions.go"} {
		flagged(t, file, ruleR2(fset, file, f, nil, map[int]bool{}), 0, "")
	}
}

func TestR2ExemptsTheFakesByPathNotByName(t *testing.T) {
	t.Parallel()
	fset, f := parse(t, `package latest
const a = "claude"
`)
	flagged(t, "a package named latest", ruleR2(fset, "internal/latest/latest.go", f, nil, map[int]bool{}), 1, "")
	flagged(t, "the fake manager", ruleR2(fset, "internal/workspace/fake/fake.go", f, nil, map[int]bool{}), 1, "")
}

func TestR3FlagsADotImportAndUnmarshalText(t *testing.T) {
	t.Parallel()
	_, dot := parse(t, `package cli
import . "github.com/wloophq/wloop/internal/domain/turn"
var s = ParseAgentState("done")
`)
	flagged(t, "dot import", ruleR3("internal/cli/watch.go", dot), 1, "")
	_, text := parse(t, `package cli
import "github.com/wloophq/wloop/internal/domain/turn"
func f(s *turn.AgentState) error { return s.UnmarshalText([]byte("done")) }
`)
	flagged(t, "UnmarshalText", ruleR3("internal/cli/watch.go", text), 1, "")
	for _, file := range []string{"internal/adapter/store/inventory/store.go", "internal/workspace/zed/zed.go"} {
		flagged(t, file, ruleR3(file, text), 0, "")
	}
}
