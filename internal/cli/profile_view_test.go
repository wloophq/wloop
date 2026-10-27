package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/termfmt"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

var checkFixture = map[string]string{
	"intent=review.md":                             "R",
	"organization=acme/intent=review.md":           "A",
	"organization=acme/repo=api/intent=review.md":  "+++\npaths = [\"**/*.sql\"]\n+++\nS",
	"organization=acme/kind=fix/intent=address.md": "F",
	"user=ana/intent=nudge.md":                     "N",
}

func colorEnv(cols string) func(string) string {
	return func(k string) string { return map[string]string{"COLUMNS": cols}[k] }
}

func TestProfileCheckReadsAsSectionsOnATerminal(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	dir := userProfile(t, te, checkFixture)
	te.Home = dir
	te.Getenv = colorEnv("100")
	if code := te.run("profile", "check", "--color", "always"); code != 0 {
		t.Fatalf("exit %d: %s%s", code, te.out, te.err)
	}
	golden(t, "profile_check_tty.golden", strings.ReplaceAll(bracketed(te.out.String()), dir, "<dir>"))
}

func TestProfileCheckProblemsReadOnATerminal(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	dir := userProfile(t, te, nil)
	brokenIntent(t, dir)
	te.Getenv = colorEnv("100")
	if code := te.run("profile", "check", dir, "--color", "always"); code != 1 {
		t.Fatalf("exit %d: %s%s", code, te.out, te.err)
	}
	errOut := te.err.String()
	at := strings.Index(errOut, "error: ")
	if at < 0 {
		t.Fatalf("no error line:\n%s", errOut)
	}
	golden(t, "profile_check_problems_tty.golden", strings.ReplaceAll(bracketed(errOut[:strings.LastIndex(errOut[:at], "\n")+1]), dir, "<dir>"))
}

func TestProfileCheckPipedKeepsItsLines(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	dir := userProfile(t, te, checkFixture)
	te.Getenv = func(k string) string { return map[string]string{"CLICOLOR_FORCE": "1"}[k] }
	if code := te.run("profile", "check"); code != 0 {
		t.Fatalf("exit %d: %s%s", code, te.out, te.err)
	}
	want := `Intents
  act              bundled   Act on the open items of a unit without touching its code: settle what you can and propose the replies it needs as drafts for you to approve; Address works on the code of a pull request you develop
  address          bundled   Triage the reviews and failing checks of a pull request you develop, in its workspace, changing its code where they call for it; Act only settles open items and drafts replies
  ask-author       ` + filepath.Join(dir, "intents", "ask-author", "intent.toml") + `   Ask the author one question
  brief            bundled   One brief per unit: goal, context, kind, size, risk, open questions and tags
  nudge            bundled   Ask whoever the pull request waits on for the next step, in a comment; guidance is the topic
  probe            bundled   Check that an agent home can read its turn, record it, take an answer and be resumed
  review           bundled   Start or continue a review round on someone else's pull request, in a dedicated workspace
  start            bundled   Start new work from a brief: in a fresh worktree of a repository, or with no checkout

address  organization=acme/kind=fix/intent=address.md   organization acme, kind fix
nudge    user=ana/intent=nudge.md   user ana
review   intent=review.md   every pull request
         organization=acme/intent=review.md   organization acme
         organization=acme/repo=api/intent=review.md   organization acme, repo api, paths **/*.sql
` + dir + `: 5 block(s), no errors
`
	if got := te.out.String(); got != want {
		t.Errorf("piped profile check changed:\n%s", got)
	}
}

func TestProfileCheckPipedProblemsKeepTheirLines(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	dir := userProfile(t, te, nil)
	brokenIntent(t, dir)
	te.Getenv = func(k string) string { return map[string]string{"CLICOLOR_FORCE": "1"}[k] }
	if code := te.run("profile", "check", dir); code != 1 {
		t.Fatalf("exit %d", code)
	}
	manifest := filepath.Join(dir, "intents", "broken", "intent.toml")
	want := manifest + `:1:9: bogus: unknown key
` + manifest + `:16:8: action.when: 13: unknown field pr.mien; did you mean "pr.mine"?
row.open && pr.mien
            ^
2026-10-09 15:00:00 error: ` + dir + `: 2 problem(s)
`
	if got := te.err.String(); got != want {
		t.Errorf("piped problems changed:\n%s", got)
	}
}

func brokenIntent(t *testing.T, dir string) {
	t.Helper()
	src := filepath.Join("..", "intent", "testdata", "bad", "predicate-typo", "intents", "ask-author")
	for _, f := range []string{"intent.toml", "instructions.md.tmpl"} {
		b, err := os.ReadFile(filepath.Join(src, f))
		if err != nil {
			t.Fatal(err)
		}
		if f == "intent.toml" {
			b = append([]byte("bogus = 1\n"), b...)
		}
		if err := os.MkdirAll(filepath.Join(dir, "intents", "broken"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "intents", "broken", f), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// fits fails t when a line of out, its styles dropped, is wider than width.
func fits(t *testing.T, what, out string, width int) {
	t.Helper()
	for _, l := range strings.Split(out, "\n") {
		if n := termfmt.Cells(termfmt.Plain(l)); n > width {
			t.Errorf("%s at %d columns: a line of %d cells:\n%s\n\n%s", what, width, n, l, out)
			return
		}
	}
}

func TestProfileCheckFitsANarrowTerminal(t *testing.T) {
	t.Parallel()
	long := map[string]string{"organization=hooli-co/repo=billing-reconcilers/intent=review.md": "L"}
	for k, v := range checkFixture {
		long[k] = v
	}
	for _, width := range []int{40, 60, 80} {
		te := newTestEnv(t, &fake.Manager{})
		userProfile(t, te, long)
		te.Getenv = colorEnv(strconv.Itoa(width))
		if code := te.run("profile", "check", "--color", "always"); code != 0 {
			t.Fatalf("exit %d: %s%s", code, te.out, te.err)
		}
		out := te.out.String()
		fits(t, "profile check", out, width)
		if !strings.Contains(strings.Join(strings.Fields(termfmt.Plain(out)), ""), "organization=hooli-co/repo=billing-reconcilers/intent=review.md") {
			t.Fatalf("the long block is missing:\n%s", out)
		}
	}
}

func TestProfileCheckNamesTheBundledFilesAProfileOverrides(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	nudge, err := os.ReadFile(filepath.Join("..", "..", "profiles", "default", "intents", "nudge", "instructions.md.tmpl"))
	if err != nil {
		t.Fatal(err)
	}
	wf, err := os.ReadFile(filepath.Join("..", "..", "profiles", "default", "workflows", "review-first-round", "workflow.toml"))
	if err != nil {
		t.Fatal(err)
	}
	userProfile(t, te, map[string]string{
		"intents/nudge/instructions.md.tmpl":         string(nudge) + "\nWrite in Portuguese.\n",
		"workflows/review-first-round/workflow.toml": string(wf),
	})
	if code := te.run("profile", "check"); code != 0 {
		t.Fatalf("exit %d: %s%s", code, te.out, te.err)
	}
	out := te.out.String()
	for _, want := range []string{
		"  nudge            bundled, overridden: instructions.md.tmpl   Ask whoever",
		"Workflows\n  review-first-round   bundled, overridden: workflow.toml\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("no %q in\n%s", want, out)
		}
	}
}
