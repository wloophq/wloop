package termfmt

import (
	"strings"
	"testing"
)

func env(kv ...string) func(string) string {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return func(k string) string { return m[k] }
}

func TestTheColorDecision(t *testing.T) {
	for _, tc := range []struct {
		name       string
		terminal   bool
		getenv     func(string) string
		mode       Mode
		goos       string
		tty, color bool
	}{
		{"terminal", true, env(), Auto, "darwin", true, true},
		{"NO_COLOR on a terminal", true, env("NO_COLOR", "1"), Auto, "darwin", true, false},
		{"TERM=dumb", true, env("TERM", "dumb"), Auto, "darwin", true, false},
		{"CLICOLOR=0", true, env("CLICOLOR", "0"), Auto, "linux", true, false},
		{"piped", false, env(), Auto, "darwin", false, false},
		{"CLICOLOR_FORCE piped colors, keeps the layout", false, env("CLICOLOR_FORCE", "1"), Auto, "darwin", false, true},
		{"CLICOLOR_FORCE=0 piped", false, env("CLICOLOR_FORCE", "0"), Auto, "darwin", false, false},
		{"GH_FORCE_TTY piped", false, env("GH_FORCE_TTY", "1"), Auto, "darwin", true, true},
		{"GH_FORCE_TTY with NO_COLOR", false, env("GH_FORCE_TTY", "1", "NO_COLOR", "1"), Auto, "darwin", true, false},
		{"NO_COLOR beats CLICOLOR_FORCE", false, env("CLICOLOR_FORCE", "1", "NO_COLOR", "1"), Auto, "darwin", false, false},
		{"--color never on a terminal", true, env("CLICOLOR_FORCE", "1"), Never, "darwin", true, false},
		{"--color always piped", false, env("NO_COLOR", "1", "TERM", "dumb"), Always, "darwin", true, true},
		{"a Windows console", true, env(), Auto, "windows", true, false},
		{"a Windows console forced", true, env("CLICOLOR_FORCE", "1"), Auto, "windows", true, true},
	} {
		got := decide(tc.terminal, tc.getenv, tc.mode, tc.goos)
		if got.TTY != tc.tty || got.Color != tc.color {
			t.Errorf("%s: tty %v color %v, want %v %v", tc.name, got.TTY, got.Color, tc.tty, tc.color)
		}
	}
}

func TestTheWidthOffATerminal(t *testing.T) {
	for _, tc := range []struct {
		getenv func(string) string
		want   int
	}{
		{env(), 80},
		{env("COLUMNS", "132"), 132},
		{env("GH_FORCE_TTY", "120", "COLUMNS", "90"), 120},
		{env("GH_FORCE_TTY", "50%", "COLUMNS", "90"), 90},
		{env("COLUMNS", "nonsense"), 80},
	} {
		if got := decide(false, tc.getenv, Auto, "linux").Width; got != tc.want {
			t.Errorf("width %d, want %d", got, tc.want)
		}
	}
}

func TestParseMode(t *testing.T) {
	var m Mode
	for s, want := range map[string]Mode{"auto": Auto, "always": Always, "never": Never} {
		if err := m.Set(s); err != nil || m != want || m.String() != s {
			t.Errorf("%q: %v %v", s, m, err)
		}
	}
	if err := m.Set("sometimes"); err == nil {
		t.Error("an unknown mode is accepted")
	}
}

func TestStylesAreTheIdentityWithoutColor(t *testing.T) {
	off, on := Term{}, Term{Color: true}
	for _, style := range []func(Term, string) string{Term.Bold, Term.Dim, Term.Red, Term.Green, Term.Yellow, Term.Cyan, Term.Reverse} {
		if got := style(off, "x"); got != "x" {
			t.Errorf("off: %q", got)
		}
		if got := style(on, "x"); !strings.HasPrefix(got, "\x1b[") || !strings.Contains(got, "x") || Plain(got) != "x" {
			t.Errorf("on: %q", got)
		}
	}
	if got := off.Link("https://github.com/a/b/pull/1", "a/b#1"); got != "a/b#1" {
		t.Errorf("link off: %q", got)
	}
	if got := on.Link("https://x/\x1b]0;evil\x07", "a/b#1"); got != "a/b#1" {
		t.Errorf("a link with a control character: %q", got)
	}
	got := on.Link("https://github.com/a/b/pull/1", "a/b#1")
	if got != "\x1b]8;;https://github.com/a/b/pull/1\x1b\\a/b#1\x1b]8;;\x1b\\" || Plain(got) != "a/b#1" {
		t.Errorf("link on: %q", got)
	}
}
