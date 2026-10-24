package expr

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/wloophq/wloop/internal/domain/workflow"
)

type zeroFacts struct{ env Env }

func (z zeroFacts) Get(field string) any {
	switch z.env[field] {
	case Bool:
		return false
	case Int:
		return 0
	case String:
		return ""
	case List:
		return []string(nil)
	}
	return nil
}

func FuzzCompile(f *testing.F) {
	seeds := []string{
		"", "   ", "pr.mine", "!pr.mine", "pr.mine && (pr.adopted", "&& x", "pr.mine ||",
		`pr.reviews == "two"`, `"x" in ["a","x"]`, `"x" in pr.labels`, "pr.reviews >= 2",
		`pr.state == "open" && !pr.draft || pr.adopted`, "(((", `"`, "[", "]", "1 2", "pr.",
		`session.idle_for > duration("2h")`, `now - session.idle_since > config.cleanup.idle`, `duration("2x") > session.idle_for`,
		`timestamp("2026-10-21T09:00:00Z") < now`, `session.pendency.exists(p, p == "turn open")`, `"a" in session.pendency`,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	quoted := regexp.MustCompile(`"((?:[^"\\\n]|\\.)*)"|'([^'\n]*)'`)
	files, _ := filepath.Glob("../../../profiles/default/intents/*/intent.toml")
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		for _, m := range quoted.FindAllStringSubmatch(string(data), -1) {
			f.Add(m[1] + m[2])
		}
	}
	f.Fuzz(func(t *testing.T, src string) {
		p, err := Compile(src, testEnv)
		if err != nil {
			var e *Error
			if !errors.As(err, &e) || e.Col < 1 || e.Col > utf8.RuneCountInString(src)+1 {
				t.Fatalf("Compile(%q) returned %v", src, err)
			}
			if strings.Count(e.Snippet, "\n") != 1 && e.Snippet != "" {
				t.Fatalf("Compile(%q) snippet %q", src, e.Snippet)
			}
			return
		}
		p.Eval(zeroFacts{testEnv})
		p.Eval(mapFacts{})
	})
}

func FuzzCompileWorkflow(f *testing.F) {
	for _, s := range []string{
		`session.idle_for > duration("2h")`, `now - session.idle_since > config.cleanup.idle`, `duration("2x") > session.idle_for`,
		`timestamp("2026-10-21T09:00:00Z") < now`, `session.pendency.exists(p, p == "turn open")`, `trigger.kind == "sweep"`,
		`session.seq / 0 > 1`, `session.idle_for + duration("1h") > duration("2h")`, "now",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		p, err := CompileWorkflow(src, sessionEnv)
		if err != nil {
			var e *Error
			if !errors.As(err, &e) || e.Col < 1 || e.Col > utf8.RuneCountInString(src)+1 {
				t.Fatalf("CompileWorkflow(%q) returned %v", src, err)
			}
			return
		}
		p.Match(workflow.Vars{})
		p.Match(workflow.Vars{Facts: workflow.Facts{"session.idle_for": time.Hour}, Now: time.Now()})
	})
}
