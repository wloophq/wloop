package dossier_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	adossier "github.com/wloophq/wloop/internal/adapter/dossier"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/dossiers"
	"github.com/wloophq/wloop/internal/domain/dossier"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }

type env struct {
	home, state string
	db          *sqlite.DB
	files       adossier.Files
	svc         *dossiers.Service
	clock       *clock
}

var (
	pull   = unit.Unit{Provider: "github", Key: "owner/repo#12"}
	thread = unit.Unit{Provider: "slack", Key: "T000/C000/1700.1"}
)

func newEnv(t *testing.T) *env {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	state := filepath.Join(home, ".local", "state", brand.Name)
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sqlite.Open(filepath.Join(state, "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	c := &clock{now: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)}
	files := adossier.Files{StateDir: state, Home: home, MaxFile: dossier.DefaultPolicy.MaxFile, Now: c.Now}
	return &env{home: home, state: state, db: db, files: files, clock: c,
		svc: &dossiers.Service{Store: db.Dossiers(), Files: files, Policy: dossier.DefaultPolicy, StateDir: state, Clock: c}}
}

func (e *env) open(t *testing.T, u unit.Unit) dossier.Dossier {
	t.Helper()
	d, err := e.svc.Open(context.Background(), dossier.Owner{Unit: u})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, "GIT_") }),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestOpenCreatesTheLayout(t *testing.T) {
	e := newEnv(t)
	d := e.open(t, pull)
	want := filepath.Join(e.state, "dossiers", "github", "owner", "repo%2312#")
	if d.Path != want || d.Status != dossier.Active {
		t.Fatalf("opened %+v, want an active dossier at %s", d, want)
	}
	for _, name := range []string{"notes.md", "sources", "drafts", "log", ".git"} {
		if _, err := os.Stat(filepath.Join(d.Path, name)); err != nil {
			t.Error(err)
		}
	}
	raw, err := os.ReadFile(filepath.Join(d.Path, "notes.md"))
	if err != nil {
		t.Fatal(err)
	}
	if s := string(raw); !strings.Contains(s, `owner: "github/owner/repo#12"`) || !strings.Contains(s, "updated: 2026-10-22T12:00:00Z") {
		t.Errorf("notes.md frontmatter:\n%s", s)
	}
	if git(t, d.Path, "log", "--format=%s") == "" {
		t.Error("no commit")
	}
	assertPrivate(t, filepath.Join(e.state, "dossiers"))
}

func assertPrivate(t *testing.T, root string) {
	t.Helper()
	if fi, err := os.Stat(root); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("%s: %v %v, want 0700", root, fi.Mode(), err)
	}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Errorf("%s has mode %v", p, info.Mode().Perm())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDirRefusesEscapesThroughSymlinks(t *testing.T) {
	e := newEnv(t)
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(e.state, "dossiers"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(e.state, "dossiers", "github")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Open(context.Background(), dossier.Owner{Unit: pull}); err == nil || !strings.Contains(err.Error(), "outside the state directory") {
		t.Fatalf("Open through a symlink out of the state dir: %v", err)
	}
	if ents, _ := os.ReadDir(outside); len(ents) != 0 {
		t.Errorf("wrote %d entries outside the state dir", len(ents))
	}
	for _, p := range []string{outside, filepath.Join(e.state, "store.db"), filepath.Join(e.state, "dossiers")} {
		if err := e.files.Remove(context.Background(), p); err == nil {
			t.Errorf("Remove(%s) was accepted", p)
		}
	}
	if _, err := e.svc.Open(context.Background(), dossier.Owner{Unit: unit.Unit{Provider: "github", Key: "a/../../x"}}); err == nil {
		t.Error("Open accepted a key with ..")
	}
}

func TestOpenRefusesInsideACheckout(t *testing.T) {
	e := newEnv(t)
	if err := os.Mkdir(filepath.Join(e.home, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	e.open(t, pull)

	for _, rel := range []string{".local", filepath.Join(".local", "state", brand.Name)} {
		e := newEnv(t)
		repo := filepath.Join(e.home, rel)
		if err := os.Mkdir(filepath.Join(repo, ".git"), 0o700); err != nil {
			t.Fatal(err)
		}
		_, err := e.svc.Open(context.Background(), dossier.Owner{Unit: pull})
		if err == nil || !strings.Contains(err.Error(), repo) {
			t.Errorf(".git in %s: Open = %v, want a refusal naming it", rel, err)
		}
		if _, err := os.Stat(filepath.Join(e.state, "dossiers")); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf(".git in %s: dossiers/ was created", rel)
		}
	}
}

func TestCommitIgnoresGlobalHooks(t *testing.T) {
	e := newEnv(t)
	marker := filepath.Join(t.TempDir(), "marker")
	hooks := filepath.Join(e.home, "hooks")
	tpl := filepath.Join(e.home, "tpl")
	script := "#!/bin/sh\necho ran >> " + marker + "\n"
	write(t, filepath.Join(hooks, "pre-commit"), 0)
	write(t, filepath.Join(tpl, "hooks", "post-commit"), 0)
	for _, p := range []string{filepath.Join(hooks, "pre-commit"), filepath.Join(tpl, "hooks", "post-commit")} {
		if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	gitconfig := "[core]\n\thooksPath = " + hooks + "\n[commit]\n\tgpgsign = true\n[init]\n\ttemplateDir = " + tpl +
		"\n[user]\n\tname = Someone Else\n\temail = someone@example.com\n"
	if err := os.WriteFile(filepath.Join(e.home, ".gitconfig"), []byte(gitconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", e.home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(e.home, ".config"))
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(e.home, ".gitconfig"))
	t.Setenv("GIT_AUTHOR_NAME", "Env Author")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.hooksPath")
	t.Setenv("GIT_CONFIG_VALUE_0", hooks)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(e.home, "stray-index"))
	t.Setenv("GIT_DIR", filepath.Join(e.home, "stray.git"))

	control := t.TempDir()
	plain := exec.Command("sh", "-c", "unset GIT_DIR GIT_INDEX_FILE; git init -q && git -c commit.gpgsign=false commit -q --allow-empty -m x")
	plain.Dir = control
	if out, err := plain.CombinedOutput(); err != nil {
		t.Fatalf("control commit: %v: %s", err, out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("the control repository ran no hook: the planted config is not in effect")
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}

	d := e.open(t, pull)
	write(t, filepath.Join(d.Path, "log", "turn.md"), 10)
	if err := e.files.Commit(context.Background(), d.Path, "turn 1"); err != nil {
		t.Fatal(err)
	}
	if got := git(t, d.Path, "rev-list", "--count", "HEAD"); got != "2" {
		t.Errorf("%s commits, want 2", got)
	}
	want := brand.Name + " <" + brand.Name + "@localhost>"
	if got := git(t, d.Path, "log", "--format=%an <%ae>|%cn <%ce>"); strings.Count(got, want) != 4 {
		t.Errorf("authors %q, want %s", got, want)
	}
	if body := git(t, d.Path, "cat-file", "commit", "HEAD"); strings.Contains(body, "gpgsig") {
		t.Error("the commit is signed")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("a user hook ran")
	}
	for _, stray := range []string{"stray-index", "stray.git"} {
		if _, err := os.Stat(filepath.Join(e.home, stray)); err == nil {
			t.Errorf("git wrote the caller's %s", stray)
		}
	}
	if ents, _ := os.ReadDir(filepath.Join(d.Path, ".git", "hooks")); len(ents) != 0 {
		t.Errorf("the template's hooks were copied: %d", len(ents))
	}
}

func TestDossierRepoHasNoRemote(t *testing.T) {
	e := newEnv(t)
	d := e.open(t, thread)
	write(t, filepath.Join(d.Path, "sources", "a.md"), 3)
	if err := e.files.Commit(context.Background(), d.Path, "turn 1"); err != nil {
		t.Fatal(err)
	}
	if got := git(t, d.Path, "remote"); got != "" {
		t.Errorf("remotes %q", got)
	}
}

func TestLargeFileRefused(t *testing.T) {
	e := newEnv(t)
	d := e.open(t, pull)
	write(t, filepath.Join(d.Path, "sources", "dump.bin"), int(e.files.MaxFile)+1)
	write(t, filepath.Join(d.Path, "sources", "ok.md"), 10)
	err := e.files.Commit(context.Background(), d.Path, "turn 1")
	if !errors.Is(err, adossier.ErrTooLarge) || !strings.Contains(err.Error(), "link it instead") || !strings.Contains(err.Error(), "sources/dump.bin") {
		t.Fatalf("Commit = %v, want a refusal naming the file", err)
	}
	if got := git(t, d.Path, "rev-list", "--count", "HEAD"); got != "1" {
		t.Errorf("%s commits, want only the opening one", got)
	}
	if err := e.files.RestoreFile(context.Background(), d.Path, "sources/dump.bin"); err != nil {
		t.Fatal(err)
	}
	if err := e.files.Commit(context.Background(), d.Path, "turn 1"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(d.Path, "sources", "dump.bin")); !errors.Is(err, fs.ErrNotExist) {
		t.Error("the restored large file is still there")
	}
}

func TestChangedAndRestoreFile(t *testing.T) {
	e := newEnv(t)
	d := e.open(t, pull)
	ctx := context.Background()
	notes := filepath.Join(d.Path, "notes.md")
	before, _ := os.ReadFile(notes)
	if err := os.WriteFile(notes, []byte("rewritten"), 0o600); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(d.Path, "drafts", "new.md"), 1)
	got, err := e.files.Changed(ctx, d.Path)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"notes.md": "modified", "drafts/new.md": "added"}
	if len(got) != len(want) {
		t.Fatalf("changed %+v", got)
	}
	for _, c := range got {
		if want[c.Rel] != c.Kind {
			t.Errorf("changed %+v", c)
		}
	}
	if err := e.files.RestoreFile(ctx, d.Path, "notes.md"); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(notes); string(after) != string(before) {
		t.Errorf("notes.md not restored: %q", after)
	}
	for _, bad := range []string{"../x", "/etc/passwd", ".git/config"} {
		if err := e.files.RestoreFile(ctx, d.Path, bad); err == nil {
			t.Errorf("RestoreFile(%s) accepted", bad)
		}
	}
}

func TestOpenRefusesAStateDirLinkedIntoACheckout(t *testing.T) {
	e := newEnv(t)
	repo := filepath.Join(e.home, "src", "dotfiles")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "state"), 0o700); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(e.home, ".local", "state", "linked")
	if err := os.Symlink(filepath.Join(repo, "state"), linked); err != nil {
		t.Fatal(err)
	}
	if co := adossier.Checkout(linked, e.home); co == "" {
		t.Error("Checkout missed the repository behind the symlink")
	}
	files := adossier.Files{StateDir: linked, Home: e.home, Now: e.clock.Now}
	svc := &dossiers.Service{Store: e.db.Dossiers(), Files: files, Policy: dossier.DefaultPolicy, StateDir: linked, Clock: e.clock}
	if _, err := svc.Open(context.Background(), dossier.Owner{Unit: pull}); err == nil || !strings.Contains(err.Error(), "repository checkout") {
		t.Fatalf("Open = %v, want a refusal", err)
	}
	if exists(filepath.Join(repo, "state", "dossiers")) {
		t.Error("dossiers/ was created inside the repository")
	}
}

func TestNestedOwnersKeepTheirOwnDossiers(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	outer := e.open(t, unit.Unit{Provider: "fake", Key: "p/obj"})
	inner := e.open(t, unit.Unit{Provider: "fake", Key: "p/obj/seg"})
	dotgit := e.open(t, unit.Unit{Provider: "fake", Key: "p/obj/.git"})
	write(t, filepath.Join(inner.Path, "sources", "precious"), 7)
	if err := e.files.Commit(ctx, outer.Path, "turn"); err != nil {
		t.Fatal(err)
	}
	if got := git(t, outer.Path, "ls-files"); got != "notes.md" {
		t.Errorf("the outer dossier tracks %q", got)
	}
	tarball := dossier.Tarball(e.state, outer.RelPath)
	if err := e.files.Archive(ctx, outer.Path, tarball); err != nil {
		t.Fatal(err)
	}
	if err := e.files.Remove(ctx, outer.Path); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(inner.Path, "sources", "precious")) || !exists(filepath.Join(dotgit.Path, ".git")) {
		t.Error("removing the outer dossier removed another one")
	}
	if err := e.files.Remove(ctx, dotgit.Path); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(inner.Path, ".git", "HEAD")) {
		t.Error("removing the .git-keyed dossier reached another repository")
	}
}
