package factworkspace

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/workspaces"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/situation"
)

// The roles of workspace.role.
const (
	RoleAuthor = "author"
	RoleReview = "review"
	RoleNone   = "none"
)

// The entries of workspace.pendency.
const (
	PendingModified   = "modified files"
	PendingUntracked  = "untracked files"
	PendingUnpushed   = "commits on no remote"
	PendingAsked      = "turn asked unanswered"
	PendingFailed     = "turn failed"
	PendingUnrecorded = "turn unrecorded"
	PendingOpen       = "turn open"
	// PendingUnreadable is a checkout git could not read, such as one whose
	// git directory was pruned: what it holds is unknown.
	PendingUnreadable = "checkout unreadable"
)

type branchKey struct{ owner, repo, branch string }

// index is one snapshot's workspaces matched against the stored pull
// requests.
type index struct {
	snap     *workspace.Snapshot
	cfg      config.Config
	viewer   string
	repos    map[string]workspace.Repo
	byRef    map[pr.Ref]dinv.PR
	adopted  map[unit.Unit]bool
	byBranch map[branchKey][]dinv.PR // open first, then highest number
}

// repoKey is a repository's key in the index: ids are per manager.
func repoKey(manager, id string) string { return manager + "\x00" + id }

func newIndex(snap *workspace.Snapshot, rows []dinv.PR, overrides map[unit.Unit]dinv.Override, viewer string, cfg config.Config) *index {
	ix := &index{snap: snap, cfg: cfg, viewer: viewer, repos: map[string]workspace.Repo{}, byRef: map[pr.Ref]dinv.PR{},
		adopted: map[unit.Unit]bool{}, byBranch: map[branchKey][]dinv.PR{}}
	for u, o := range overrides {
		if o.Adopted {
			ix.adopted[u] = true
		}
	}
	for _, r := range snap.Repos {
		ix.repos[repoKey(r.Manager, r.ID)] = r
	}
	for _, p := range rows {
		ix.byRef[p.Ref] = p
		k := branchKey{strings.ToLower(p.Ref.Owner), strings.ToLower(p.Ref.Repo), p.HeadBranch}
		ix.byBranch[k] = append(ix.byBranch[k], p)
	}
	for _, ps := range ix.byBranch {
		slices.SortFunc(ps, func(a, b dinv.PR) int {
			if ao, bo := a.State == dgh.StateOpen, b.State == dgh.StateOpen; ao != bo {
				if ao {
					return -1
				}
				return 1
			}
			return b.Ref.Number - a.Ref.Number
		})
	}
	return ix
}

func (ix *index) ownerName(w workspace.Workspace) (owner, name string, ok bool) {
	return ix.repos[repoKey(w.Manager, w.RepoID)].OwnerName()
}

// reviewPrefixed tells a review workspace by its name, the sweep's rule.
func (ix *index) reviewPrefixed(w workspace.Workspace) bool {
	if w.IsMain {
		return false
	}
	owner, name, _ := ix.ownerName(w)
	prefix := ix.cfg.For(owner, name).ReviewPrefix
	return prefix != "" && strings.HasPrefix(w.DisplayName, prefix)
}

// reviews tells a workspace of someone else's pull request: by the pull
// request's author when it and the viewer are known, else by the review
// prefix of its name. A taken-over pull request and a main checkout are the
// user's.
func (ix *index) reviews(w workspace.Workspace, p dinv.PR) bool {
	if w.IsMain || ix.adopted[p.Ref.Unit()] {
		return false
	}
	if p.Author != "" && ix.viewer != "" {
		return !strings.EqualFold(p.Author, ix.viewer)
	}
	return ix.reviewPrefixed(w)
}

// branchOf is the head branch w's pull request has: a review workspace's
// name after the review prefix, the sweep's rule.
func (ix *index) branchOf(w workspace.Workspace) string {
	if ix.reviewPrefixed(w) {
		owner, name, _ := ix.ownerName(w)
		return strings.TrimPrefix(w.DisplayName, ix.cfg.For(owner, name).ReviewPrefix)
	}
	return w.PRBranch()
}

// prOf is w's pull request: Orca's link, else the stored one on its branch,
// open first. The inventory's own link is not read: it outlives the pull
// request it was made for. A main checkout on the default branch
// has none, and a main checkout takes no fork's.
func (ix *index) prOf(w workspace.Workspace) (dinv.PR, bool) {
	repo := ix.repos[repoKey(w.Manager, w.RepoID)]
	if workspace.MainOnDefault(w, repo) {
		return dinv.PR{}, false
	}
	owner, name, ok := repo.OwnerName()
	if ok && w.LinkedPR != 0 && !w.IsMain {
		ref := pr.Ref{Owner: owner, Repo: name, Number: w.LinkedPR}
		if p, found := ix.byRef[ref]; found {
			return p, true
		}
		return dinv.PR{Ref: ref}, true
	}
	if !ok {
		return dinv.PR{}, false
	}
	for _, p := range ix.byBranch[branchKey{strings.ToLower(owner), strings.ToLower(name), ix.branchOf(w)}] {
		if !w.IsMain || !p.IsFork {
			return p, true
		}
	}
	return dinv.PR{}, false
}

// duplicate tells a workspace of ref that is not the one Orca links to it
// while another is.
func (ix *index) duplicate(w workspace.Workspace, ref pr.Ref) bool {
	if w.IsMain || w.LinkedPR == ref.Number {
		return false
	}
	for _, o := range ix.snap.Workspaces {
		if o.Path == w.Path || o.IsMain || o.LinkedPR != ref.Number {
			continue
		}
		if owner, name, ok := ix.ownerName(o); ok && strings.EqualFold(owner, ref.Owner) && strings.EqualFold(name, ref.Repo) {
			return true
		}
	}
	return false
}

// local are the facts git reads of a checkout, all without a fetch.
// Unreadable is set when git could not read it.
type local struct {
	head                       string
	dirty, untracked, unpushed int
	unreadable                 bool
}

// local reads w's checkout, or reuses the last read while its HEAD, index,
// HEAD reflog and agent activity are unchanged and the read is younger than
// sweep.slow. A git failure is an unreadable checkout, not an error, and so
// is a directory without .git, where git would read an enclosing repository.
func (p *Provider) local(ctx context.Context, w workspace.Workspace, activity time.Time, cfg config.Config) (local, error) {
	if !p.d.Disk.Exists(filepath.Join(w.Path, ".git")) {
		return local{unreadable: true}, nil
	}
	head, err := p.d.Git.Head(ctx, w.Path)
	if ctx.Err() != nil {
		return local{}, ctx.Err()
	}
	if err != nil {
		return local{unreadable: true}, nil
	}
	key := localKey{head: head, index: p.d.Disk.Index(w.Path), touched: p.d.Disk.Touched(w.Path), activity: activity}
	now := p.d.Clock.Now()
	p.c.mu.Lock()
	c, ok := p.c.local[w.Path]
	p.c.mu.Unlock()
	if ok && c.key == key && now.Sub(c.at) < time.Duration(cfg.Sweep.Slow) {
		return c.read, nil
	}
	l, err := p.readLocal(ctx, w.Path, cfg.Cleanup.Disposable)
	if ctx.Err() != nil {
		return local{}, ctx.Err()
	}
	if err != nil {
		return local{unreadable: true}, nil
	}
	l.head = head
	p.c.mu.Lock()
	p.c.local[w.Path] = cachedLocal{key, now, l}
	p.c.mu.Unlock()
	return l, nil
}

func (p *Provider) readLocal(ctx context.Context, dir string, disposable []string) (local, error) {
	var l local
	var unpushedErr error
	unpushed := make(chan struct{})
	go func() {
		defer close(unpushed)
		l.unpushed, unpushedErr = p.d.Git.Unpushed(ctx, dir)
	}()
	dirty, untracked, err := p.d.Git.Status(ctx, dir)
	<-unpushed
	if err != nil {
		return l, err
	}
	l.dirty = dirty
	for _, u := range untracked {
		if !workspaces.Disposable(disposable, strings.TrimSuffix(path.Clean(u), "/")) {
			l.untracked++
		}
	}
	return l, unpushedErr
}

func (p *Provider) facts(ctx context.Context, snap *workspace.Snapshot, ix *index, w workspace.Workspace, cfg config.Config) (workflow.Facts, []time.Time, error) {
	now := p.d.Clock.Now()
	exists := w.Local() && p.d.Disk.Exists(w.Path)
	agents := agentActivity(snap, w)
	var l local
	if exists {
		var err error
		if l, err = p.local(ctx, w, agents, cfg); err != nil {
			return nil, nil, err
		}
	}
	owner, name, _ := ix.ownerName(w)
	repo := ""
	if owner != "" {
		repo = owner + "/" + name
	}
	role, prKey, prState := RoleNone, "", ""
	var finishedFor time.Duration
	var ended time.Time
	var book turn.Book
	duplicate := false
	linked, hasPR := ix.prOf(w)
	if hasPR {
		u := linked.Ref.Unit()
		prKey, prState = u.Key, strings.ToLower(linked.State)
		role = RoleAuthor
		if ix.reviews(w, linked) {
			role = RoleReview
		}
		if repo == "" {
			repo = linked.Ref.Owner + "/" + linked.Ref.Repo
		}
		if linked.State == dgh.StateMerged || linked.State == dgh.StateClosed {
			ended = latest(linked.MergedAt, linked.ClosedAt)
			if !ended.IsZero() {
				finishedFor = situation.WorkingDuration(ended, now)
			}
		}
		duplicate = ix.duplicate(w, linked.Ref)
		books, err := p.d.Books.Books(ctx, u)
		if err != nil {
			return nil, nil, err
		}
		book = books[u]
	}
	newestState := ""
	if t, ok := book.Newest(); ok {
		newestState = string(t.State)
	}
	activity := lastActivity(latest(agents, p.d.Disk.Touched(w.Path)), book)
	sessions, err := p.sessionsIn(ctx, w, now, time.Duration(cfg.Cleanup.Idle))
	if err != nil {
		return nil, nil, err
	}
	quiet := time.Duration(0)
	if !activity.IsZero() {
		quiet = max(now.Sub(activity), 0)
	}
	f := workflow.Facts{
		"workspace.role":              role,
		"workspace.main":              w.IsMain,
		"workspace.pr":                prKey,
		"workspace.pr_state":          prState,
		"workspace.pr_finished_for":   finishedFor,
		"workspace.path_exists":       exists,
		"workspace.temporary":         Temporary(w.Path),
		"workspace.duplicate":         duplicate,
		"workspace.dirty":             l.dirty,
		"workspace.untracked":         l.untracked,
		"workspace.unpushed":          l.unpushed,
		"workspace.newest_turn_state": newestState,
		"workspace.open_turn":         slices.ContainsFunc(book.Turns, func(t turn.Turn) bool { return t.State.Open() }),
		"workspace.pendency":          pendency(l, book),
		"workspace.sessions":          sessions,
		"workspace.manager":           w.Manager,
		"workspace.quiet_for":         quiet,
		"workspace.fingerprint":       fingerprint(w, l.head),
		"workspace.repo":              repo,
	}
	if !activity.IsZero() {
		f["workspace.last_activity"] = activity
	}
	var deadlines []time.Time
	if !ended.IsZero() {
		deadlines = append(deadlines, workingEnd(ended, time.Duration(cfg.Cleanup.WorkspaceGrace)))
	}
	if !w.IsMain && !hasPR && !activity.IsZero() {
		deadlines = append(deadlines, activity.Add(time.Duration(cfg.Cleanup.StaleWorkspace)))
	}
	return f, deadlines, nil
}

func fingerprint(w workspace.Workspace, head string) string { return w.Path + ":" + head }

func latest(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

// pendency is what holds work in a checkout and its pull request's book.
func pendency(l local, book turn.Book) []string {
	out := []string{}
	if l.unreadable {
		out = append(out, PendingUnreadable)
	}
	if l.dirty > 0 {
		out = append(out, PendingModified)
	}
	if l.untracked > 0 {
		out = append(out, PendingUntracked)
	}
	if l.unpushed > 0 {
		out = append(out, PendingUnpushed)
	}
	newest, ok := book.Newest()
	asked := ok && newest.State == turn.Asked && !answered(book, *newest)
	if asked {
		out = append(out, PendingAsked)
	}
	if ok && newest.State == turn.Failed {
		out = append(out, PendingFailed)
	}
	if ok && newest.State == turn.Unrecorded {
		out = append(out, PendingUnrecorded)
	}
	if slices.ContainsFunc(book.Turns, func(t turn.Turn) bool { return t.State.Open() && !(asked && t.Seq == newest.Seq) }) {
		out = append(out, PendingOpen)
	}
	return out
}

// answered tells an asked turn the user answered after its last record.
func answered(book turn.Book, t turn.Turn) bool {
	var since time.Time
	if r, ok := book.LastRecord(t.Seq); ok {
		since = r.At
	}
	return slices.ContainsFunc(book.Answers, func(a turn.Answer) bool {
		return a.TurnSeq == t.Seq && a.Route != turn.Refused && a.At.After(since)
	})
}

// agentActivity is the newest output of w's terminals and state change of
// its agents.
func agentActivity(snap *workspace.Snapshot, w workspace.Workspace) time.Time {
	var at time.Time
	for _, t := range snap.Terminals {
		if t.Manager == w.Manager && t.WorkspaceID == w.ID {
			at = latest(at, t.LastOutput)
		}
	}
	for _, p := range snap.Panes {
		if p.Manager == w.Manager && p.WorkspaceID == w.ID {
			at = latest(at, p.StateSince)
		}
	}
	return at
}

// sessionsIn counts w's open agent sessions: the active ones, and the dormant
// ones active within cleanup.idle, which the next turn may still wake. A
// dormant session older than that keeps the workspace no more than a closed one.
func (p *Provider) sessionsIn(ctx context.Context, w workspace.Workspace, now time.Time, idle time.Duration) (int, error) {
	if p.d.Sessions == nil {
		return 0, nil
	}
	all, err := p.d.Sessions.Sessions(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, s := range all {
		if !(s.Manager == w.Manager && s.WorkspaceID == w.ID || s.Worktree != "" && s.Worktree == w.Path) {
			continue
		}
		switch s.State() {
		case turn.SessionActive:
			n++
		case turn.SessionDormant:
			if now.Sub(lastActive(s)) < idle {
				n++
			}
		}
	}
	return n, nil
}

func lastActive(s turn.Session) time.Time {
	at := s.StartedAt
	for _, t := range []time.Time{s.TypedAt, s.PromptSeenAt, s.IdleSince, s.MCPAt} {
		if t.After(at) {
			at = t
		}
	}
	return at
}

// lastActivity is the newest of at and the times of the pull request's
// turns, records and answers; zero when none is known.
func lastActivity(at time.Time, book turn.Book) time.Time {
	for _, t := range book.Turns {
		at = latest(at, latest(t.SentAt, latest(t.StartedAt, t.EndedAt)))
	}
	for _, r := range book.Records {
		at = latest(at, r.At)
	}
	for _, a := range book.Answers {
		at = latest(at, a.At)
	}
	return at
}

// Temporary tells a path under the system's temporary directories or an
// agent's worktrees directory.
func Temporary(p string) bool {
	p = filepath.ToSlash(filepath.Clean(p))
	for _, root := range []string{filepath.ToSlash(os.TempDir()), "/private/tmp", "/tmp"} {
		root = strings.TrimSuffix(path.Clean(root), "/")
		if root != "" && root != "/" && strings.HasPrefix(p, root+"/") {
			return true
		}
	}
	return strings.Contains(p+"/", "/.claude/worktrees/")
}

// workingEnd is when d of working time, weekends left out, has passed since
// from, in UTC as situation.WorkingDuration counts it.
func workingEnd(from time.Time, d time.Duration) time.Time {
	cur := from.UTC()
	for d > 0 {
		next := time.Date(cur.Year(), cur.Month(), cur.Day()+1, 0, 0, 0, 0, time.UTC)
		if wd := cur.Weekday(); wd != time.Saturday && wd != time.Sunday {
			step := next.Sub(cur)
			if step >= d {
				return cur.Add(d)
			}
			d -= step
		}
		cur = next
	}
	return cur
}
