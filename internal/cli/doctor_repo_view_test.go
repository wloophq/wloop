package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

// repoProfile is a profile whose blocks cover every state profile show
// names in acme/api.
func repoProfile(t *testing.T, te *testEnv) string {
	t.Helper()
	te.cfg.Profile = filepath.Join(te.Home, "profile")
	for rel, body := range map[string]string{
		"intent=review.md":                           "+++\noverride = true\n[delegates]\nclaude = [\"/code-review high\"]\n+++\nU",
		"organization=acme/intent=review.md":         "P",
		"organization=other/intent=review.md":        "O",
		"user=ana/intent=review.md":                  "A",
		"kind=fix/intent=review.md":                  "F",
		"organization=acme/intent=address.md":        "+++\npaths = [\"**/*.sql\"]\n+++\nS",
		"organization=acme/repo=web/intent=nudge.md": "W",
	} {
		f := filepath.Join(te.cfg.Profile, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(f), 0o700)
		os.WriteFile(f, []byte(body), 0o600)
	}
	te.Store.SaveTurn(t.Context(), dinv.StoredTurn{ID: "aaaaaaaaaaaa", Kind: "review", Repo: "acme/api", Text: "T", BundledSHA: "an older bundled guide"}, testNow)
	return te.cfg.Profile
}

func TestDoctorRepoReadsAsSectionsOnATerminal(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	agentDaemon(t, te)
	dir := repoProfile(t, te)
	te.Getenv = colorEnv("100")
	if code := te.run("profile", "show", "acme/api", "--color", "always"); code != 0 {
		t.Fatalf("exit %d: %s%s", code, te.out, te.err)
	}
	golden(t, "doctor_repo_tty.golden", strings.ReplaceAll(bracketed(te.out.String()), dir, "<dir>"))
}

func TestDoctorRepoPipedKeepsItsLines(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	agentDaemon(t, te)
	dir := repoProfile(t, te)
	te.Getenv = func(k string) string { return map[string]string{"CLICOLOR_FORCE": "1"}[k] }
	if code := te.run("profile", "show", "acme/api"); code != 0 {
		t.Fatalf("exit %d: %s%s", code, te.out, te.err)
	}
	want := strings.ReplaceAll(`address  bundled  embedded intent=address.md   every pull request   applies
         user     <dir>/organization=acme/intent=address.md   organization acme, paths **/*.sql   needs the brief
nudge    bundled  embedded intent=nudge.md   every pull request   applies
         user     <dir>/organization=acme/repo=web/intent=nudge.md   organization acme, repo web   not in this repository
review   bundled  embedded intent=review.md   every pull request   replaced by an override
         user     <dir>/intent=review.md   every pull request   applies, replaces the guidance above it (bundled changed since the last round here)
         user     <dir>/kind=fix/intent=review.md   kind fix   needs the brief
         user     <dir>/organization=acme/intent=review.md   organization acme   applies
         user     <dir>/organization=other/intent=review.md   organization other   not in this repository
         user     <dir>/user=ana/intent=review.md   user ana   depends on the pull request
         delegates claude: /code-review high   (<dir>/intent=review.md)
`, "<dir>", dir)
	if got := te.out.String(); got != want {
		t.Errorf("piped profile show changed:\n%s", got)
	}
}

func TestDoctorRepoShowPrintsTheInstructionsUnstyled(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	agentDaemon(t, te)
	if code := te.run("profile", "show", "acme/api", "--kind", "nudge", "--show", "--color", "always"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	out := te.out.String()
	at := strings.Index(out, "Instructions ")
	if at < 0 || !strings.Contains(out[:at], "\x1b[1mnudge\x1b[22m") || !strings.Contains(out[:at], "\x1b[2msources: bundled: embedded intent=nudge.md") {
		t.Fatalf("no styled section before the instructions:\n%s", out)
	}
	if strings.Contains(out[at:], "\x1b") || !strings.Contains(out[at:], "# Nudge on acme/api#0, asked for by you.") {
		t.Fatalf("instructions styled or missing:\n%s", out[at:])
	}
}

func TestDoctorRepoFitsANarrowTerminal(t *testing.T) {
	t.Parallel()
	for _, width := range []int{40, 60, 80} {
		te := newTestEnv(t, &fake.Manager{})
		agentDaemon(t, te)
		repoProfile(t, te)
		long := filepath.Join(te.cfg.Profile, "organization=hooli-co", "repo=billing-reconcilers", "intent=review.md")
		os.MkdirAll(filepath.Dir(long), 0o700)
		os.WriteFile(long, []byte("L"), 0o600)
		te.Getenv = colorEnv(strconv.Itoa(width))
		if code := te.run("profile", "show", "acme/api", "--color", "always"); code != 0 {
			t.Fatalf("exit %d: %s%s", code, te.out, te.err)
		}
		fits(t, "profile show", te.out.String(), width)
	}
}
