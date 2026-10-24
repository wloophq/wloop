package orca

import "testing"

func TestSessionEnvReadsOrcasVariables(t *testing.T) {
	env := map[string]string{"ORCA_TERMINAL_HANDLE": "term_1", "ORCA_PANE_KEY": "tab:leaf", "ORCA_WORKTREE_ID": "w1"}
	getenv := func(k string) string { return env[k] }
	s := SessionEnv{}
	if s.Handle(getenv) != "term_1" || s.Pane(getenv) != "tab:leaf" || s.WorktreeID(getenv) != "w1" {
		t.Fatalf("got %q %q %q", s.Handle(getenv), s.Pane(getenv), s.WorktreeID(getenv))
	}
}
