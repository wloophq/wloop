// Package workspacestest holds the fakes of the workspaces ports, the
// checkouts they stand for, and the contracts every implementation passes.
package workspacestest

import (
	"context"
	"errors"
	"path"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/workspaces"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workspace"
)

// Checkout describes a workspace's checkout on branch Branch, pushed to
// origin, and the main checkout of its repository.
type Checkout struct {
	// Unpushed commits come after the last push.
	Unpushed int
	// BranchGone deletes the branch from origin after the push;
	// SimilarBranch pushes "someone/<Branch>" to origin first.
	BranchGone    bool
	SimilarBranch bool
	// PRHoldsHead points pull request 1's head at HEAD.
	PRHoldsHead bool
	// Modified are tracked files changed in the workspace.
	Modified []string
	// Untracked and Ignored are files of the workspace by slash path and
	// content; an ignored path is ignored by its first part.
	Untracked map[string]string
	Ignored   map[string]string
	// Main are the main checkout's ignored files.
	Main map[string]string
	// Links are ignored symbolic links at the workspace's top level.
	Links []string
	// Nested are repositories of their own inside the workspace by name,
	// ignored.
	Nested map[string]Nested
}

// Nested is a repository with one commit pushed to its own origin.
type Nested struct {
	// Under is the workspace directory it sits in, "" for the top level.
	Under string
	// Modified leaves an untracked file in it; Unpushed commits follow the
	// push on HEAD, OtherBranch one on another branch.
	Modified    bool
	Unpushed    int
	OtherBranch bool
	Stash       bool
	// Detached detaches HEAD and commits on it; Tagged commits on a detached
	// HEAD, tags the commit and goes back to its branch.
	Detached bool
	Tagged   bool
	NoRemote bool
	// Ignored are files its own .gitignore ignores.
	Ignored map[string]string
}

// Kept is whether n holds commits or a stash no remote has, or has no
// remote.
func (n Nested) Kept() bool {
	return n.Unpushed > 0 || n.OtherBranch || n.Stash || n.Detached || n.Tagged || n.NoRemote
}

// NestedPath is the slash path of the nested repository name in c.
func (c Checkout) NestedPath(name string) string { return path.Join(c.Nested[name].Under, name) }

// Branch is the branch every Checkout is on.
const Branch = "feat"

// TrackedFile is the file every Checkout tracks besides .gitignore.
const TrackedFile = "README.md"

// IgnoredUntracked is what Tree.Ignored lists for c, sorted: every
// untracked file, and every ignored path as the entry its .gitignore line
// matches, a directory as one entry whatever it holds.
func (c Checkout) IgnoredUntracked() (untracked, ignored []string) {
	for p := range c.Untracked {
		untracked = append(untracked, p)
	}
	for _, l := range c.IgnoreLines() {
		if !slices.Contains(c.Links, l) {
			ignored = append(ignored, l)
		}
	}
	ignored = append(ignored, c.Links...)
	slices.Sort(untracked)
	slices.Sort(ignored)
	return untracked, slices.DeleteFunc(ignored, func(p string) bool { return c.mainOnly(p) })
}

// mainOnly is whether the ignore line p names only main checkout files.
func (c Checkout) mainOnly(p string) bool {
	match := func(k string) bool { return k == p || strings.HasPrefix(k, p) }
	if slices.ContainsFunc(keys(c.Ignored), match) || slices.Contains(c.Links, p) {
		return false
	}
	for d := range c.Nested {
		if match(c.NestedPath(d) + "/") {
			return false
		}
	}
	return true
}

// IgnoreLines are the .gitignore lines c needs.
func (c Checkout) IgnoreLines() []string {
	seen := map[string]bool{}
	var lines []string
	add := func(l string) {
		if !seen[l] {
			seen[l] = true
			lines = append(lines, l)
		}
	}
	for _, p := range slices.Concat(keys(c.Ignored), keys(c.Main)) {
		first, _, dir := strings.Cut(p, "/")
		if dir {
			first += "/"
		}
		add(first)
	}
	for _, l := range c.Links {
		add(l)
	}
	for d := range c.Nested {
		first, _, _ := strings.Cut(c.NestedPath(d), "/")
		add(first + "/")
	}
	slices.Sort(lines)
	return lines
}

// NestedIgnoreLines are the .gitignore lines of a nested repository.
func (n Nested) NestedIgnoreLines() []string {
	return Checkout{Ignored: n.Ignored}.IgnoreLines()
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// World is what an implementation builds for a Checkout: the ports over it,
// the workspace's directory and its main checkout's, and HEAD's commit.
type World struct {
	Remote    workspaces.Remote
	Tree      workspaces.Tree
	Files     workspaces.Files
	Dir, Main string
	Head      string
}

// Build realizes a Checkout.
type Build func(t *testing.T, c Checkout) World

// RunTree checks a workspaces.Tree.
func RunTree(t *testing.T, build Build) {
	ctx := context.Background()
	c := Checkout{
		Modified:  []string{TrackedFile},
		Untracked: map[string]string{"threads.txt": "notes"},
		Ignored:   map[string]string{"credential.json": "{}", "cache/a": "1", "cache/d/b": "2"},
		Nested:    map[string]Nested{"op-web": {}},
	}
	w := build(t, c)
	if n, err := w.Tree.DirtyTracked(ctx, w.Dir); err != nil || n != 1 {
		t.Errorf("DirtyTracked = %d, %v; want 1", n, err)
	}
	if n, err := w.Tree.Changed(ctx, w.Dir); err != nil || n != 2 {
		t.Errorf("Changed = %d, %v; want the modified and the untracked file", n, err)
	}
	untracked, ignored, err := w.Tree.Ignored(ctx, w.Dir)
	wantU, wantI := c.IgnoredUntracked()
	slices.Sort(untracked)
	slices.Sort(ignored)
	if err != nil || !slices.Equal(untracked, wantU) || !slices.Equal(ignored, wantI) {
		t.Errorf("Ignored = %v %v, %v; want %v %v", untracked, ignored, err, wantU, wantI)
	}
	if head, err := w.Tree.Head(ctx, w.Dir); err != nil || head != w.Head || head == "" {
		t.Errorf("Head = %q, %v; want %q", head, err, w.Head)
	}
	if n, err := w.Tree.Changed(ctx, w.Dir+"/op-web"); err != nil || n != 0 {
		t.Errorf("a clean nested repository: Changed = %d, %v", n, err)
	}
	if kept, err := w.Tree.Kept(ctx, w.Dir+"/op-web"); err != nil || kept != "" {
		t.Errorf("a clean nested repository: Kept = %q, %v", kept, err)
	}
	t.Run("kept", func(t *testing.T) {
		c := Checkout{Nested: map[string]Nested{"clean": {Ignored: map[string]string{".env": "K=1"}}, "head": {Unpushed: 1}, "branch": {OtherBranch: true},
			"stash": {Stash: true}, "detached": {Detached: true}, "tagged": {Tagged: true}, "lonely": {NoRemote: true}}}
		w := build(t, c)
		for name, n := range c.Nested {
			kept, err := w.Tree.Kept(ctx, w.Dir+"/"+name)
			if err != nil || (kept != "") != n.Kept() {
				t.Errorf("Kept(%s) = %q, %v", name, kept, err)
			}
		}
		_, ignored, err := w.Tree.Ignored(ctx, w.Dir+"/clean")
		if err != nil || !slices.Equal(ignored, []string{".env"}) {
			t.Errorf("a nested repository's ignored files: %v, %v", ignored, err)
		}
	})
}

// RunRemote checks a workspaces.Remote.
func RunRemote(t *testing.T, build Build) {
	ctx := context.Background()
	t.Run("unpushed", func(t *testing.T) {
		w := build(t, Checkout{Unpushed: 2})
		if remote, branch, err := w.Remote.Tracking(ctx, w.Dir); err != nil || remote != "origin" || branch != Branch {
			t.Errorf("Tracking = %q %q, %v", remote, branch, err)
		}
		if n, err := w.Remote.Unpushed(ctx, w.Dir); err != nil || n != 2 {
			t.Errorf("Unpushed = %d, %v; want 2", n, err)
		}
		if has, err := w.Remote.RemoteHasBranch(ctx, w.Dir, "origin", Branch); err != nil || !has {
			t.Errorf("RemoteHasBranch = %v, %v", has, err)
		}
		if held, err := w.Remote.PRHolds(ctx, w.Dir, ""); err != nil || held {
			t.Errorf("no pull request head holds HEAD: %v, %v", held, err)
		}
		if err := w.Remote.FetchTracking(ctx, w.Dir, 0); err != nil {
			t.Fatal(err)
		}
		if n, err := w.Remote.Unpushed(ctx, w.Dir); err != nil || n != 2 {
			t.Errorf("Unpushed after a fetch = %d, %v; want 2", n, err)
		}
	})
	t.Run("branch gone", func(t *testing.T) {
		w := build(t, Checkout{BranchGone: true})
		if has, err := w.Remote.RemoteHasBranch(ctx, w.Dir, "origin", Branch); err != nil || !has {
			t.Errorf("before a fetch the branch shows as it was: %v, %v", has, err)
		}
		if err := w.Remote.FetchTracking(ctx, w.Dir, 0); err != nil {
			t.Fatal(err)
		}
		if has, err := w.Remote.RemoteHasBranch(ctx, w.Dir, "origin", Branch); err != nil || has {
			t.Errorf("after a fetch the branch is gone: %v, %v", has, err)
		}
		if remote, branch, err := w.Remote.Tracking(ctx, w.Dir); err != nil || remote != "origin" || branch != Branch {
			t.Errorf("Tracking of a gone branch = %q %q, %v", remote, branch, err)
		}
	})
	t.Run("pull request holds", func(t *testing.T) {
		w := build(t, Checkout{Unpushed: 1, PRHoldsHead: true})
		if err := w.Remote.FetchTracking(ctx, w.Dir, 1); err != nil {
			t.Fatal(err)
		}
		if held, err := w.Remote.PRHolds(ctx, w.Dir, w.Head); err != nil || !held {
			t.Errorf("PRHolds(HEAD) = %v, %v", held, err)
		}
		if held, err := w.Remote.PRHolds(ctx, w.Dir, strings.Repeat("ab", 20)); err != nil || held {
			t.Errorf("an unknown head holds nothing: %v, %v", held, err)
		}
	})
}

// RunFiles checks a workspaces.Files over the files of a Checkout.
func RunFiles(t *testing.T, build Build) {
	w := build(t, Checkout{
		Ignored: map[string]string{"same.json": "x", "differs.json": "x", "only-here.json": "x", "dir/a": "1", "dir/b/c": "2", "odd/a": "1", "odd/extra": "3"},
		Main:    map[string]string{"same.json": "x", "differs.json": "y", "dir/a": "1", "dir/b/c": "2", "odd/a": "1"},
		Links:   []string{".env"},
	})
	at := func(root, p string) string { return root + "/" + p }
	for p, want := range map[string]bool{"same.json": true, "differs.json": false, "only-here.json": false, "dir": true, "dir/": true, "odd": false} {
		if got, err := w.Files.Same(at(w.Dir, p), at(w.Main, p)); err != nil || got != want {
			t.Errorf("Same(%s) = %v, %v; want %v", p, got, err, want)
		}
	}
	if !w.Files.Exists(at(w.Dir, "dir/b")) || w.Files.Exists(at(w.Dir, "none")) {
		t.Error("Exists")
	}
	if !w.Files.Link(at(w.Dir, ".env")) || w.Files.Link(at(w.Dir, "same.json")) || w.Files.Link(at(w.Dir, "none")) {
		t.Error("Link")
	}
	t.Run("repos", func(t *testing.T) {
		w := build(t, Checkout{Ignored: map[string]string{"node_modules/x.js": ""}, Nested: map[string]Nested{
			"a": {Under: "node_modules"}, "b": {Under: "node_modules/deep/er"}, "c": {Under: "build"},
		}})
		repos, err := w.Files.Repos(at(w.Dir, "node_modules"))
		slices.Sort(repos)
		if err != nil || !slices.Equal(repos, []string{"a", "deep/er/b"}) {
			t.Errorf("Repos = %v, %v", repos, err)
		}
		if repos, err := w.Files.Repos(at(w.Dir, "node_modules/a")); err != nil || !slices.Equal(repos, []string{"."}) {
			t.Errorf("Repos of a repository = %v, %v; want itself", repos, err)
		}
	})
}

// Removal is a checkout and what its removal must do: refuse with Want in
// the reason, or remove when Want is empty.
type Removal struct {
	Name     string
	Checkout Checkout
	Want     string
	// Backup is set when the refusal carries the backup command.
	Backup bool
}

// Removals are the checkouts removal must refuse or allow, with Disposable
// and a user login of "user".
var Removals = []Removal{
	{Name: "clean", Checkout: Checkout{}},
	{Name: "unpushed commits", Checkout: Checkout{Unpushed: 2}, Want: "2 commits not pushed", Backup: true},
	{Name: "unpushed commits the pull request holds", Checkout: Checkout{Unpushed: 1, PRHoldsHead: true}},
	{Name: "a modified tracked file", Checkout: Checkout{Modified: []string{TrackedFile}}, Want: "1 modified tracked file"},
	{Name: "an untracked file outside the list", Checkout: Checkout{Untracked: map[string]string{"threads.txt": "notes"}}, Want: "outside the disposable list: threads.txt"},
	{Name: "an ignored file identical to the main checkout", Checkout: Checkout{Ignored: map[string]string{"credential.json": "{}"}, Main: map[string]string{"credential.json": "{}"}}},
	{Name: "an ignored file that differs from the main checkout", Checkout: Checkout{Ignored: map[string]string{"credential.json": "{}"}, Main: map[string]string{"credential.json": "{\"k\":1}"}}, Want: "outside the disposable list: credential.json"},
	{Name: "an ignored file the main checkout lacks", Checkout: Checkout{Ignored: map[string]string{".env": "K=1"}}, Want: "outside the disposable list: .env"},
	{Name: "an ignored directory identical to the main checkout", Checkout: Checkout{Ignored: map[string]string{"cache/a": "1", "cache/d/b": "2"}, Main: map[string]string{"cache/a": "1", "cache/d/b": "2"}}},
	{Name: "an ignored directory that differs from the main checkout", Checkout: Checkout{Ignored: map[string]string{"cache/a": "1", "cache/d/b": "2"}, Main: map[string]string{"cache/a": "1"}}, Want: "outside the disposable list: cache/"},
	{Name: "disposable names", Checkout: Checkout{Ignored: map[string]string{"node_modules/x/y.js": "", turn.ScratchDir + "/notes.md": "", "x.pyc": "", ".DS_Store": "", "build/out": ""}}},
	{Name: "an ignored symbolic link", Checkout: Checkout{Links: []string{".env"}}},
	{Name: "a clean nested repository", Checkout: Checkout{Nested: map[string]Nested{"op-web": {}}}},
	{Name: "a nested repository with changes", Checkout: Checkout{Nested: map[string]Nested{"op-web": {Modified: true}}}, Want: "outside the disposable list: op-web/"},
	{Name: "a nested repository with unpushed commits", Checkout: Checkout{Nested: map[string]Nested{"op-web": {Unpushed: 1}}}, Want: "outside the disposable list: op-web/"},
	{Name: "an ignored file in a nested repository", Checkout: Checkout{Nested: map[string]Nested{"op-web": {Ignored: map[string]string{".env": "K=1"}}}}, Want: "outside the disposable list: op-web/"},
	{Name: "disposable ignored files in a nested repository", Checkout: Checkout{Nested: map[string]Nested{"op-web": {Ignored: map[string]string{"node_modules/x.js": ""}}}}},
	{Name: "a commit on another branch of a nested repository", Checkout: Checkout{Nested: map[string]Nested{"op-web": {OtherBranch: true}}}, Want: "outside the disposable list: op-web/"},
	{Name: "a stash in a nested repository", Checkout: Checkout{Nested: map[string]Nested{"op-web": {Stash: true}}}, Want: "outside the disposable list: op-web/"},
	{Name: "a detached nested repository with a commit on no remote", Checkout: Checkout{Nested: map[string]Nested{"op-web": {Detached: true}}}, Want: "outside the disposable list: op-web/"},
	{Name: "a commit only a tag holds in a nested repository", Checkout: Checkout{Nested: map[string]Nested{"op-web": {Tagged: true}}}, Want: "outside the disposable list: op-web/"},
	{Name: "a nested repository with no remote", Checkout: Checkout{Nested: map[string]Nested{"op-web": {NoRemote: true}}}, Want: "outside the disposable list: op-web/"},
	{Name: "a nested repository under a disposable name", Checkout: Checkout{Nested: map[string]Nested{"vendor-repo": {Under: "build", Modified: true}}}, Want: "outside the disposable list: build/vendor-repo/"},
	{Name: "a clean nested repository under a disposable name", Checkout: Checkout{Nested: map[string]Nested{"vendor-repo": {Under: "build"}}}},
	{Name: "a nested repository deep in a disposable directory", Checkout: Checkout{Ignored: map[string]string{"node_modules/x.js": ""}, Nested: map[string]Nested{"pkg": {Under: "node_modules/@scope", Unpushed: 1}}}, Want: "outside the disposable list: node_modules/@scope/pkg/"},
	{Name: "a clean nested repository deep in a disposable directory", Checkout: Checkout{Ignored: map[string]string{"node_modules/x.js": ""}, Nested: map[string]Nested{"pkg": {Under: "node_modules/@scope"}}}},
	{Name: "a deleted remote branch with local commits", Checkout: Checkout{BranchGone: true}, Want: "its branch feat is gone from origin", Backup: true},
	{Name: "a deleted remote branch another branch name ends with", Checkout: Checkout{BranchGone: true, SimilarBranch: true}, Want: "its branch feat is gone from origin", Backup: true},
	{Name: "a squash merged branch the pull request holds", Checkout: Checkout{BranchGone: true, PRHoldsHead: true}},
}

// RunRemovals checks that Removals hold on build's worlds: RemoveRefusal
// after a fetch and Remove agree, and only an allowed removal reaches the
// manager.
func RunRemovals(t *testing.T, build Build) {
	for _, r := range Removals {
		t.Run(r.Name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			w := build(t, r.Checkout)
			ws := workspace.Workspace{ID: "ws-1", RepoID: "r1", Path: w.Dir, Branch: Branch, HostID: workspace.LocalHost}
			s, m := Service(w)
			pr := workspaces.PRInfo{Unit: unit.Unit{Provider: unit.GitHub, Key: "acme/api#1"}, Number: 1}
			if r.Checkout.PRHoldsHead {
				pr.Head = w.Head
			}
			if err := s.Fetch(ctx, ws, pr); err != nil {
				t.Fatal(err)
			}
			refusal, backup, err := s.RemoveRefusal(ctx, ws, pr)
			if err != nil {
				t.Fatal(err)
			}
			if !matches(refusal, r.Want) || (backup != "") != r.Backup {
				t.Fatalf("RemoveRefusal = %q, backup %q; want %q, backup %v", refusal, backup, r.Want, r.Backup)
			}
			if r.Backup && backup != "git push origin HEAD:refs/heads/user/backup-"+Branch {
				t.Errorf("backup %q", backup)
			}
			err = s.Remove(ctx, ws, pr, false)
			var refused workspaces.Refusal
			switch {
			case r.Want == "" && err != nil:
				t.Fatalf("Remove: %v", err)
			case r.Want != "" && (!errors.As(err, &refused) || !matches(refused.Reason, r.Want)):
				t.Fatalf("Remove = %v; want a refusal with %q", err, r.Want)
			}
			if removed := m.Removed(); (r.Want == "") != slices.Equal(removed, []string{"ws-1"}) {
				t.Fatalf("removed %v", removed)
			}
		})
	}
}

func matches(reason, want string) bool {
	if want == "" {
		return reason == ""
	}
	return strings.Contains(reason, want)
}

// Service is a workspaces.Service over w with fakes for the rest:
// Disposable, the viewer "user", a fixed clock and every session closable.
// m records what reaches the manager.
func Service(w World) (*workspaces.Service, *Manager) {
	m := &Manager{RepoList: []workspace.Repo{{ID: "r1", Path: w.Main}}}
	return &workspaces.Service{
		Manager: m, Remote: w.Remote, Tree: w.Tree, Files: w.Files,
		Sessions: &Sessions{}, Rounds: &Rounds{}, Fetched: &FetchLog{},
		Disposable: func() []string { return slices.Clone(Disposable) },
		Viewer:     func() string { return "user" },
		Pub:        &Publisher{}, Clock: Clock{At: time.Date(2026, 10, 21, 15, 0, 0, 0, time.UTC)},
	}, m
}
