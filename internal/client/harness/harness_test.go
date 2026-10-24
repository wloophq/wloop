package harness

import (
	"strings"
	"testing"
)

type session string

func (s session) Handle(getenv func(string) string) string { return getenv(string(s) + "_HANDLE") }
func (s session) Pane(getenv func(string) string) string   { return getenv(string(s) + "_PANE") }
func (s session) WorktreeID(getenv func(string) string) string {
	return getenv(string(s) + "_WORKTREE")
}

type agent string

func (a agent) SessionID(getenv func(string) string) string { return getenv(string(a) + "_SESSION") }
func (a agent) Home(getenv func(string) string) string      { return getenv(string(a) + "_HOME") }
func (agent) SessionStart([]byte) (string, string, string, error) {
	return "", "", "", nil
}
func (agent) Stop([]byte) (string, string, string, error) { return "", "", "", nil }
func (a agent) Block(reason string) []byte                { return []byte(string(a) + ":" + reason) }

func testSet() Set {
	return Set{
		SessionEnvs:  map[string]SessionEnv{"orca": session("ORCA"), "other": session("OTHER"), "zed": session("ZED")},
		Agents:       map[string]Protocol{"claude": agent("CLAUDE"), "codex": agent("CODEX"), "zz": agent("ZZ")},
		DefaultAgent: "claude",
	}
}

func envOf(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

func TestForEmptyIsTheDefault(t *testing.T) {
	p, err := testSet().For("")
	if err != nil || p != agent("CLAUDE") {
		t.Fatalf("got %v %v", p, err)
	}
	if p, err := testSet().For("codex"); err != nil || p != agent("CODEX") {
		t.Fatalf("got %v %v", p, err)
	}
}

func TestForUnknownNamesTheKnownKinds(t *testing.T) {
	_, err := testSet().For("nope")
	if err == nil || !strings.Contains(err.Error(), `"nope"`) || !strings.Contains(err.Error(), "claude, codex, zz") {
		t.Fatalf("err %v", err)
	}
}

func TestDetectPicksTheAgentWhoseSessionIsSet(t *testing.T) {
	s := testSet()
	if got := s.Detect(envOf(map[string]string{"CODEX_SESSION": "x"})); got != agent("CODEX") {
		t.Fatalf("got %v", got)
	}
	if got := s.Detect(envOf(map[string]string{"ZZ_SESSION": "x", "CODEX_SESSION": "y"})); got != agent("CODEX") {
		t.Fatalf("not the first by name: %v", got)
	}
	if got := s.Detect(envOf(nil)); got != agent("CLAUDE") {
		t.Fatalf("no fallback: %v", got)
	}
}

func TestSessionPicksTheManagerWhoseHandleIsSet(t *testing.T) {
	s := testSet()
	if got := s.Session(envOf(map[string]string{"ZED_HANDLE": "x"})); got != session("ZED") {
		t.Fatalf("got %v", got)
	}
	if got := s.Session(envOf(map[string]string{"ZED_HANDLE": "x", "OTHER_HANDLE": "y"})); got != session("OTHER") {
		t.Fatalf("not the first by name: %v", got)
	}
	if name, got := s.Manager(envOf(nil)); got != nil || name != "" {
		t.Fatalf("a session no manager runs got %q %v", name, got)
	}
}

type claiming struct {
	session
	root string
}

func (c claiming) Claims(cwd string) bool { return strings.HasPrefix(cwd, c.root) }

func TestAClaimerTakesOnlyTheSessionsUnderItsRoot(t *testing.T) {
	s := Set{SessionEnvs: map[string]SessionEnv{"desk": claiming{session("DESK"), "/owned/"}}}
	env := envOf(map[string]string{"DESK_HANDLE": "local_1"})
	if name, _ := s.Manager(env); name != "" {
		t.Fatalf("a claimer with no cwd claimed %q", name)
	}
	s.Cwd = func() string { return "/elsewhere/x" }
	if name, _ := s.Manager(env); name != "" {
		t.Fatalf("a session outside the root went to %q", name)
	}
	s.Cwd = func() string { return "/owned/repo/w1" }
	if name, got := s.Manager(env); name != "desk" || got == nil {
		t.Fatalf("a session under the root went to %q", name)
	}
}

func TestHandleIgnoresClaims(t *testing.T) {
	s := Set{SessionEnvs: map[string]SessionEnv{"desk": claiming{session("DESK"), "/owned/"}}, Cwd: func() string { return "/elsewhere" }}
	if h := s.Handle(envOf(map[string]string{"DESK_HANDLE": "local_1"})); h != "local_1" {
		t.Fatalf("handle %q", h)
	}
	if h := s.Handle(envOf(nil)); h != "" {
		t.Fatalf("no manager's variable set, handle %q", h)
	}
}
