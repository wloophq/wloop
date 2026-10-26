package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func TestDoctorRepoListsBlocksAndConditions(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	agentDaemon(t, te)
	user := t.TempDir()
	write := func(rel, body string) string {
		f := filepath.Join(user, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(f), 0o700)
		os.WriteFile(f, []byte(body), 0o600)
		return f
	}
	root := write("intent=review.md", "+++\n[delegates]\nclaude = [\"/code-review high\"]\n+++\nU")
	org := write("organization=acme/intent=review.md", "P")
	other := write("organization=other/intent=review.md", "O")
	ana := write("user=ana/intent=review.md", "A")
	fix := write("kind=fix/intent=review.md", "F")
	sql := write("organization=acme/intent=address.md", "+++\npaths = [\"**/*.sql\"]\n+++\nS")
	te.cfg.Profile = user
	if code := te.run("profile", "show", "acme/api"); code != 0 {
		t.Fatalf("exit %d: %s%s", code, te.out, te.err)
	}
	out := te.out.String()
	for _, want := range []string{
		"review   bundled  embedded intent=review.md   every pull request   applies",
		"user     " + root + "   every pull request   applies",
		"user     " + org + "   organization acme   applies",
		"user     " + other + "   organization other   not in this repository",
		"user     " + ana + "   user ana   depends on the pull request",
		"user     " + fix + "   kind fix   needs the brief",
		"delegates claude: /code-review high   (" + root + ")",
		"user     " + sql + "   organization acme, paths **/*.sql   needs the brief",
		"nudge    bundled  embedded intent=nudge.md   every pull request   applies",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}
}

func TestDoctorRepoNamesARevertedOverride(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	agentDaemon(t, te)
	user := t.TempDir()
	os.MkdirAll(filepath.Join(user, "intents", "nudge"), 0o700)
	os.WriteFile(filepath.Join(user, "intents", "nudge", "instructions.md.tmpl"), []byte("{{\n"), 0o600)
	te.cfg.Profile = user
	if code := te.run("profile", "show", "acme/api"); code != 0 || !strings.Contains(te.out.String(), "nudge    runs as bundled, the override does not load") {
		t.Fatalf("exit %d: %s%s", code, te.out, te.err)
	}
}

func TestDoctorRepoShowsResolverErrors(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	agentDaemon(t, te)
	bad := t.TempDir()
	os.WriteFile(filepath.Join(bad, "intent=review.md"), []byte("+++\nextnd = true\n+++\nU"), 0o600)
	te.cfg.Profile = bad
	if code := te.run("profile", "show", "acme/api"); code == 0 || !strings.Contains(te.out.String(), "extnd") {
		t.Fatalf("exit %d: %s", code, te.out)
	}
	te.out.Reset()
	te.cfg.Profile = "/nope"
	if code := te.run("profile", "show", "https://github.com/acme/api/pull/7"); code == 0 || !strings.Contains(te.out.String(), "/nope not found") {
		t.Fatalf("exit %d: %s", code, te.out)
	}
}

func TestDoctorRepoShow(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	agentDaemon(t, te)
	if code := te.run("profile", "show", "acme/api", "--intent", "nudge", "--show"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if out := te.out.String(); !strings.Contains(out, "Instructions ") || !strings.Contains(out, "# Nudge on acme/api#0, asked for by you.") ||
		!strings.Contains(out, "sources: bundled: embedded intent=nudge.md") || strings.Contains(out, "# First review") {
		t.Fatalf("%s", out)
	}
}

func TestDoctorRepoNamesAChangedBundledGuide(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	agentDaemon(t, te)
	user := t.TempDir()
	os.WriteFile(filepath.Join(user, "intent=review.md"), []byte("+++\noverride = true\n+++\nU"), 0o600)
	te.cfg.Profile = user
	te.Store.SaveTurn(t.Context(), dinv.StoredTurn{ID: "aaaaaaaaaaaa", Kind: "review", Repo: "acme/api", Text: "T", BundledSHA: "an older bundled guide"}, testNow)
	te.run("profile", "show", "acme/api")
	if !strings.Contains(te.out.String(), "applies, replaces the guidance above it (bundled changed since the last round here)") {
		t.Fatalf("%s", te.out)
	}
}

func TestDoctorProfileDirectories(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.cfg.Profile = t.TempDir()
	if got := te.profileChecks(te.cfg); len(got) != 1 || got[0].mark != markOK {
		t.Fatalf("%+v", got)
	}
	te.cfg.Profile = "/nope"
	if got := te.profileChecks(te.cfg); len(got) != 1 || got[0].mark != markMissing || !strings.Contains(got[0].info, "/nope") {
		t.Fatalf("%+v", got)
	}
}
