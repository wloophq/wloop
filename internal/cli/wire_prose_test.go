package cli

import (
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/core/headless"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func TestTheHeadlessWrapperResolvesThroughTheProfile(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	userProfile(t, te, map[string]string{headless.WrapperFile: `{{define "before"}}MINE on {{.Unit}}
{{end}}{{define "after"}}MY RULES
{{end}}`})
	w, err := te.Env.headlessWrapper(t.Logf)()
	if err != nil {
		t.Fatal(err)
	}
	p, err := w.Prompt(headless.Request{Unit: unit.Unit{Provider: "slack", Key: "C1/1.2"}}, "", "", "act")
	if err != nil || !strings.HasPrefix(p, "MINE on ") || !strings.Contains(p, "MY RULES\n"+headless.Schema()) {
		t.Fatalf("%v\n%s", err, p)
	}
}

func TestABrokenUserHeadlessWrapperFallsBackToTheBundledOne(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	userProfile(t, te, map[string]string{headless.WrapperFile: `{{define "before"}}MINE{{end}}`})
	var logged []string
	wrapper := te.Env.headlessWrapper(func(f string, a ...any) { logged = append(logged, f) })
	for range 2 {
		w, err := wrapper()
		if err != nil {
			t.Fatal(err)
		}
		p, err := w.Prompt(headless.Request{Unit: unit.Unit{Provider: "slack", Key: "C1/1.2"}}, "", "", "act")
		if err != nil || !strings.HasPrefix(p, "This is a headless turn of ") {
			t.Fatalf("%v\n%s", err, p)
		}
	}
	if len(logged) != 1 {
		t.Fatalf("logged %v", logged)
	}
}
