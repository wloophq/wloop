// Package workspaces removes and unregisters workspaces, refusing whatever
// would lose work.
package workspaces

import (
	"cmp"
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/shell"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workspace"
)

// NotFetchedToday is the refusal of a workspace whose remote was not fetched
// on the clock's current day; Fetch lifts it.
const NotFetchedToday = "not fetched today"

// NotACheckout is the refusal of a workspace path without a .git entry, where
// git would read an enclosing repository instead.
const NotACheckout = "not a git checkout"

// PRInfo is the pull request a workspace belongs to; zero when none.
type PRInfo struct {
	Unit   unit.Unit
	Number int
	Head   string
	Open   bool
}

// Refusal is why a removal does not run. Backup is the command that saves
// the commits it would lose, Paths the files it would lose.
type Refusal struct {
	Reason string
	Backup string
	Paths  []string
}

func (r Refusal) Error() string {
	if r.Backup != "" {
		return r.Reason + "; back them up with: " + r.Backup
	}
	return r.Reason
}

// Service removes and unregisters workspaces behind the guards.
type Service struct {
	Manager  Manager
	Remote   Remote
	Tree     Tree
	Sessions SessionGuard
	Files    Files
	Rounds   Rounds
	Fetched  FetchLog
	// Disposable names the files and directories removal may lose.
	Disposable func() []string
	// Viewer is the user's login, the prefix of a backup branch.
	Viewer func() string
	Pub    port.Publisher
	Clock  port.Clock
}

// Fetch fetches w's remote and records the day; the guards read what it
// fetched.
func (s *Service) Fetch(ctx context.Context, w workspace.Workspace, pr PRInfo) error {
	if r := s.checkout(w); r.Reason != "" {
		return r
	}
	if err := s.Remote.FetchTracking(ctx, w.Path, pr.Number); err != nil {
		return err
	}
	return s.Fetched.Mark(ctx, w.ID, s.Clock.Now())
}

// RemoveRefusal is why removing w would lose work, "" when nothing would be
// lost, with the command that backs up its commits when they are the
// reason. It fetches nothing and writes nothing.
func (s *Service) RemoveRefusal(ctx context.Context, w workspace.Workspace, pr PRInfo) (refusal, backup string, err error) {
	r, _, err := s.check(ctx, w, pr, true)
	return r.Reason, r.Backup, err
}

// RemoveRefusalAsFetched is RemoveRefusal on what the last fetch left,
// however old.
func (s *Service) RemoveRefusalAsFetched(ctx context.Context, w workspace.Workspace, pr PRInfo) (refusal, backup string, err error) {
	r, _, err := s.check(ctx, w, pr, false)
	return r.Reason, r.Backup, err
}

// Remove closes w's sessions and removes it, recording its branch and head.
// Without force it fetches and checks again, then, with the sessions
// closed, looks a last time right before the removal, returning a Refusal
// when anything would be lost. Removing an open pull request's workspace
// opts the pull request out of its automatic first round.
func (s *Service) Remove(ctx context.Context, w workspace.Workspace, pr PRInfo, force bool) error {
	if r := s.checkout(w); r.Reason != "" {
		return r
	}
	var seen look
	if !force {
		if err := s.Remote.FetchTracking(ctx, w.Path, pr.Number); err != nil {
			return Refusal{Reason: "could not fetch: " + err.Error()}
		}
		r, l, err := s.check(ctx, w, pr, false)
		if err != nil {
			return err
		}
		if r.Reason != "" {
			return r
		}
		seen = l
	}
	if err := s.Sessions.CloseAll(ctx, w.ID, "its workspace is removed"); err != nil {
		return err
	}
	head, err := s.Tree.Head(ctx, w.Path)
	if !force {
		if err := s.unchanged(ctx, w, seen, head, err); err != nil {
			return err
		}
	}
	if err != nil || head == "" {
		head = "unknown"
	}
	if err := s.Manager.Remove(ctx, w.ID, force); err != nil {
		return err
	}
	s.publish(w, KindRemoved, "branch "+w.Branch+" head "+head)
	if pr.Open && !pr.Unit.IsZero() {
		return s.Rounds.SkipFirstRound(ctx, pr.Unit)
	}
	return nil
}

// Unregister drops w from the manager once its checkout is gone.
func (s *Service) Unregister(ctx context.Context, w workspace.Workspace) error {
	if r := placement(w); r.Reason != "" {
		return r
	}
	if s.Files.Exists(w.Path) {
		return Refusal{Reason: "its path still exists"}
	}
	if err := s.Manager.Unregister(ctx, w.ID); err != nil {
		return err
	}
	s.publish(w, KindUnregistered, "branch "+w.Branch+" head unknown")
	return nil
}

// placement refuses the main checkout and a workspace not on this machine
// at an absolute path, whose checks would read nothing.
func placement(w workspace.Workspace) Refusal {
	switch {
	case w.IsMain:
		return Refusal{Reason: "the main checkout"}
	case !w.Local():
		return Refusal{Reason: "not a local workspace"}
	}
	return Refusal{}
}

// checkout is placement, then a refusal of a path without a .git entry,
// before any git command runs there.
func (s *Service) checkout(w workspace.Workspace) Refusal {
	if r := placement(w); r.Reason != "" {
		return r
	}
	if !s.Files.Exists(filepath.Join(w.Path, ".git")) {
		return Refusal{Reason: NotACheckout}
	}
	return Refusal{}
}

// look is what the checks saw that an agent may change before the removal.
type look struct {
	unpushed int
	head     string
}

// check refuses in this order: the main checkout, a workspace elsewhere or
// a path that is no checkout, a session the guard keeps,
// a stale fetch (when fetchDay is set), a branch gone from its remote with
// commits the pull request does not hold, commits on no remote, modified
// tracked files, then files outside the disposable list. It returns the
// unpushed count and HEAD it read, for the last look before a removal.
func (s *Service) check(ctx context.Context, w workspace.Workspace, pr PRInfo, fetchDay bool) (Refusal, look, error) {
	if r := s.checkout(w); r.Reason != "" {
		return r, look{}, nil
	}
	if why, err := s.Sessions.Refusal(ctx, w.ID); err != nil || why != "" {
		return Refusal{Reason: why}, look{}, err
	}
	if fetchDay {
		if ok, err := s.fetchedToday(ctx, w.ID); err != nil || !ok {
			return Refusal{Reason: NotFetchedToday}, look{}, err
		}
	}
	remote, branch, err := s.Remote.Tracking(ctx, w.Path)
	if err != nil {
		return Refusal{}, look{}, err
	}
	unpushed, err := s.Remote.Unpushed(ctx, w.Path)
	if err != nil {
		return Refusal{}, look{}, err
	}
	gone := false
	if branch != "" {
		has, err := s.Remote.RemoteHasBranch(ctx, w.Path, remote, branch)
		if err != nil {
			return Refusal{}, look{}, err
		}
		gone = !has
	}
	if gone || unpushed > 0 {
		held, err := s.Remote.PRHolds(ctx, w.Path, pr.Head)
		if err != nil {
			return Refusal{}, look{}, err
		}
		switch {
		case !held && gone:
			return Refusal{Reason: "its branch " + branch + " is gone from " + remote + " and the pull request does not hold its HEAD, in " + w.Path, Backup: s.backup(remote, w)}, look{}, nil
		case !held:
			return Refusal{Reason: plural(unpushed, "commit", "commits") + " not pushed in " + w.Path, Backup: s.backup(remote, w)}, look{}, nil
		}
	}
	modified, err := s.Tree.DirtyTracked(ctx, w.Path)
	if err != nil {
		return Refusal{}, look{}, err
	}
	if modified > 0 {
		return Refusal{Reason: plural(modified, "modified tracked file", "modified tracked files") + " in " + w.Path}, look{}, nil
	}
	lost, err := s.Loose(ctx, w)
	if err != nil {
		return Refusal{}, look{}, err
	}
	if len(lost) > 0 {
		return Refusal{Reason: "untracked or ignored files outside the disposable list: " + firstTen(lost), Paths: lost}, look{}, nil
	}
	head, err := s.Tree.Head(ctx, w.Path)
	return Refusal{}, look{unpushed, head}, err
}

// unchanged looks again at what an agent or the user may have changed since
// the checks: the sessions, the commits and HEAD, read at head (headErr),
// and the files.
func (s *Service) unchanged(ctx context.Context, w workspace.Workspace, seen look, head string, headErr error) error {
	if why, err := s.Sessions.Refusal(ctx, w.ID); err != nil || why != "" {
		if err != nil {
			return err
		}
		return Refusal{Reason: why}
	}
	if headErr != nil {
		return headErr
	}
	now, err := s.Remote.Unpushed(ctx, w.Path)
	if err != nil {
		return err
	}
	changed := Refusal{Reason: w.Path + " changed while it was checked; try again"}
	if now != seen.unpushed || head != seen.head {
		return changed
	}
	modified, err := s.Tree.DirtyTracked(ctx, w.Path)
	if err != nil {
		return err
	}
	lost, err := s.Loose(ctx, w)
	if err != nil {
		return err
	}
	if modified > 0 || len(lost) > 0 {
		changed.Paths = lost
		return changed
	}
	return nil
}

func (s *Service) fetchedToday(ctx context.Context, id string) (bool, error) {
	if s.Fetched == nil {
		return false, nil
	}
	last, err := s.Fetched.Last(ctx, id)
	if err != nil || last.IsZero() {
		return false, err
	}
	now := s.Clock.Now()
	y, m, d := now.Date()
	ly, lm, ld := last.In(now.Location()).Date()
	return y == ly && m == lm && d == ld, nil
}

func (s *Service) backup(remote string, w workspace.Workspace) string {
	name := "backup-" + cmp.Or(w.Branch, w.ID)
	if s.Viewer != nil {
		if v := s.Viewer(); v != "" {
			name = v + "/" + name
		}
	}
	return "git push " + shell.Word(remote) + " " + shell.Word("HEAD:refs/heads/"+name)
}

func (s *Service) publish(w workspace.Workspace, kind, data string) {
	if s.Pub == nil {
		return
	}
	s.Pub.Publish(port.Event{Unit: workspace.UnitOf(w.Path), Kind: kind, Key: w.ID, Data: data, At: s.Clock.Now()})
}

func firstTen(paths []string) string {
	shown := strings.Join(paths[:min(len(paths), 10)], ", ")
	if more := len(paths) - 10; more > 0 {
		shown += fmt.Sprintf(", and %d more", more)
	}
	return shown
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}
