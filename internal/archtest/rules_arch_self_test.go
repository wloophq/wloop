package archtest

import (
	"testing"
)

func TestR7FlagsAPlantedImport(t *testing.T) {
	t.Parallel()
	ok := []string{"context", "fmt", module + "internal/domain/pr", module + "internal/core/port", module + "internal/meter"}
	flagged(t, "allowed", ruleR7("internal/core/turns", ok), 0, "")
	bad := append(ok, module+"internal/adapter/store/sqlite", module+"internal/github", module+"internal/profile", "github.com/x/y", "os/exec", "database/sql")
	flagged(t, "core", ruleR7("internal/core/turns", bad), 6, "(R7)")
	flagged(t, "meter subpackage", ruleR7("internal/core/turns", []string{module + "internal/meter/metertest"}), 0, "")
	flagged(t, "not the core", ruleR7("internal/daemon", bad), 0, "")
}

func TestR9FlagsAPlantedSpawn(t *testing.T) {
	t.Parallel()
	fset, f := parse(t, `package cli
import "os/exec"
var a = exec.Command("orca", "status")
var b, _ = exec.LookPath("/usr/local/bin/orca")
var c = "/Users/x/Library/Application Support/orca/orca-runtime.json"
var d = "/tmp/orca/runtime.sock"
var e = exec.Command("git", "status")
var g = sink("orca")
var h = "orcas are fine"
var i = exec.CommandContext(ctx, e.selectedManager(cfg).Binary, "ls")
var j = e.Sh(ctx, "sh", "-c", "command -v "+m.Binary)
var k = exec.Command(agent.Binary())
`)
	flagged(t, "cli", ruleR9(fset, "internal/cli/x.go", f), 6, "(R9)")
	flagged(t, "the orca manager", ruleR9(fset, "internal/workspace/orca/x.go", f), 0, "")
	flagged(t, "a sibling", ruleR9(fset, "internal/workspace/orcax/x.go", f), 6, "(R9)")
}

func TestR9FlagsAPlantedDesktopReach(t *testing.T) {
	t.Parallel()
	fset, f := parse(t, `package claude
var a = "claude://resume?session=x"
var b = []string{"open", "-g", "claude://code/continue?session=local_x"}
var c = "/tmp/cc-socks"
var d = "~/.claude/sessions"
var e = "peer.key"
var g = "~/.claude/projects"
var h = "claude --resume"
var i = "a peer review"
`)
	flagged(t, "the claude agent", ruleR9Desktop(fset, "internal/adapter/agent/claude/x.go", f), 5, "(R9)")
	flagged(t, "cli", ruleR9Desktop(fset, "internal/cli/x.go", f), 5, "(R9)")
	flagged(t, "the desktop manager", ruleR9Desktop(fset, "internal/workspace/claudedesktop/x.go", f), 0, "")
	flagged(t, "the desktop adapter", ruleR9Desktop(fset, "internal/adapter/claudedesktop/x.go", f), 0, "")
	flagged(t, "a sibling", ruleR9Desktop(fset, "internal/workspace/claudedesktopx/x.go", f), 5, "(R9)")
}

func TestR11FlagsAPlantedImport(t *testing.T) {
	t.Parallel()
	ok := []string{"time", module + "internal/domain/pr", module + "internal/meter"}
	flagged(t, "allowed", ruleR11("internal/domain/turn", ok), 0, "")
	bad := append(ok, module+"internal/adapter/store/sqlite", module+"internal/workspace/orca", module+"internal/github", module+"internal/github/graphql")
	flagged(t, "domain", ruleR11("internal/domain/turn", bad), 4, "(R11)")
	flagged(t, "not the domain", ruleR11("internal/core/turns", bad), 0, "")
}

func TestR12FlagsAPlantedImport(t *testing.T) {
	t.Parallel()
	ok := []string{"sync", "time", module + "internal/meter/metertest"}
	flagged(t, "allowed", ruleR12("internal/meter", ok), 0, "")
	bad := append(ok, module+"internal/domain/pr", module+"internal/config", "github.com/x/y")
	flagged(t, "meter", ruleR12("internal/meter", bad), 3, "(R12)")
	flagged(t, "not the meter", ruleR12("internal/core/turns", bad), 0, "")
}

func TestR13FlagsAPlantedQuery(t *testing.T) {
	t.Parallel()
	fset, f := parse(t, `package e2e
import "database/sql"
var db, _ = sql.Open("sqlite", "x.db")
var a = "SELECT kind FROM events WHERE number = 1"
var b = "INSERT INTO events(kind) VALUES (?)"
var c = `+"`"+`
UPDATE events SET kind = ?
`+"`"+`
var d = "PRAGMA journal_mode"
var e = "POST, DELETE"
var g = "select the first row from the list"
var h = "insert into events(kind) values (?)"
var i = `+"`SELECT `"+` + cols + " FROM events WHERE a = ?"
var j = "ORDER BY " + col + " DESC"
`)
	flagged(t, "e2e", ruleR13(fset, "internal/e2e/live.go", f, nil, map[int]bool{}), 7, "(R13)")
	flagged(t, "a test file", ruleR13(fset, "internal/e2e/live_test.go", f, nil, map[int]bool{}), 1, "sql.Open")
	flagged(t, "the store", ruleR13(fset, "internal/adapter/store/storetest/e2e.go", f, nil, map[int]bool{}), 1, "sql.Open")
	flagged(t, "the sqlite package", ruleR13(fset, "internal/adapter/store/sqlite/db.go", f, nil, map[int]bool{}), 0, "")

	allow := []allowed{{"internal/e2e/live.go", "SELECT kind", "planted"}, {"internal/e2e/live.go", "gone", "stale"}}
	used := map[int]bool{}
	flagged(t, "allowlisted", ruleR13(fset, "internal/e2e/live.go", f, allow, used), 6, "(R13)")
	flagged(t, "stale", stale(allow, used), 1, `"gone"`)
}
