package cli

import (
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func TestTUINeedsTheDaemon(t *testing.T) {
	te := newTestEnv(t, &fake.Manager{})
	te.noDaemon(t)
	if code := te.run("tui"); code != 1 || !strings.Contains(te.err.String(), brand.Command+" serve") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if code := te.run("tui", "extra"); code != 1 || !strings.Contains(te.err.String(), "usage: "+brand.Command+" tui") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
}

func TestTheTUIFollowsTheColorDecision(t *testing.T) {
	for _, tc := range []struct {
		env  map[string]string
		want bool
	}{
		{map[string]string{"GH_FORCE_TTY": "1"}, true},
		{map[string]string{"GH_FORCE_TTY": "1", "NO_COLOR": "1"}, false},
		{map[string]string{"GH_FORCE_TTY": "1", "TERM": "dumb"}, false},
	} {
		e := &Env{Getenv: func(k string) string { return tc.env[k] }}
		if got := e.tuiColor(); got != tc.want {
			t.Errorf("%v: color %v", tc.env, got)
		}
	}
}
