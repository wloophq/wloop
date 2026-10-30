package dossiers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/dossier"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Notes is the curated file of a dossier: only a compaction or a closing call rewrites it.
const Notes = "notes.md"

// LogDir holds the append-only log of a dossier.
const LogDir = "log"

// WriteDirs are the subdirectories of its dossier a session may write in.
var WriteDirs = []string{LogDir, "sources", "drafts"}

// KindRestored is the loop event of a file a turn wrote against the write rules; Data names the file.
const KindRestored = "dossier_restored"

type EntryKind string

const (
	Missing EntryKind = ""
	File    EntryKind = "file"
	Folder  EntryKind = "dir"
	Symlink EntryKind = "symlink"
	Other   EntryKind = "other"
)

// Entry is a dossier path as it lies on disk, symlinks unfollowed.
type Entry struct {
	Rel   string
	Kind  EntryKind
	Size  int64
	Links uint64 // hard links to a file's inode; 0 unknown
}

// Pages reads and writes single files of a dossier; implemented by internal/adapter/dossier, which
// refuses any path that leaves the dossier.
type Pages interface {
	PageReader
	PageWriter
}

type PageReader interface {
	Stat(ctx context.Context, dir, rel string) (Entry, error)
	// List is the regular files under rel, at any depth, sorted by Rel.
	List(ctx context.Context, dir, rel string) ([]Entry, error)
	Read(ctx context.Context, dir, rel string) ([]byte, error)
	// Committed is rel's content in the last commit; ok false when it holds no such file.
	Committed(ctx context.Context, dir, rel string) (data []byte, ok bool, err error)
}

type PageWriter interface {
	// Write creates or replaces rel as a regular file of mode 0600.
	Write(ctx context.Context, dir, rel string, data []byte) error
	// Discard removes rel and whatever it holds, never following a symlink.
	Discard(ctx context.Context, dir, rel string) error
}

// Turns opens a unit's dossier for its turns and holds the write rules after each one: what broke a
// rule is restored from the last commit, the rest is committed.
type Turns struct {
	Dossiers *Service
	Pages    Pages
	// Events records loop events on the dossier's unit; nil records none.
	Events func(ctx context.Context, ev port.Event) error
	Log    func(format string, args ...any)

	// Serial, when set, is the lock every Turns of one state directory shares; nil uses the Turns' own.
	Serial *sync.Mutex
	// Moves applies on disk the dossier moves the store recorded, such as a unit's dossier a task took when
	// it picked the unit up; nil applies none.
	Moves func(ctx context.Context) error

	// mu serializes the git work on every dossier: a check, a commit and a notes rewrite never interleave.
	mu sync.Mutex
}

func (t *Turns) locker() *sync.Mutex {
	if t.Serial != nil {
		return t.Serial
	}
	return &t.mu
}

// Violation is a file a turn wrote against the write rules, and the rule.
type Violation struct{ Rel, Why string }

// Open opens the dossier a turn on u reads, its task's when a task holds u, first applying the moves the
// store recorded and settling what was written since its last commit.
func (t *Turns) Open(ctx context.Context, u unit.Unit) (dossier.Dossier, error) {
	t.locker().Lock()
	defer t.locker().Unlock()
	if t.Moves != nil {
		if err := t.Moves(ctx); err != nil {
			return dossier.Dossier{}, fmt.Errorf("moving the dossiers: %w", err)
		}
	}
	o, err := t.Dossiers.Store.Owner(ctx, u)
	if err != nil {
		return dossier.Dossier{}, err
	}
	if d, ok, err := t.Dossiers.Get(ctx, o); err != nil {
		return dossier.Dossier{}, err
	} else if ok && d.Status == dossier.Active {
		if _, err := t.settle(ctx, d, "unchecked writes"); err != nil {
			return dossier.Dossier{}, err
		}
	}
	return t.Dossiers.Open(ctx, o)
}

// Check holds the dossier u's turns read to the write rules after a turn and commits it with message; a dossier
// that is absent or neither active nor resolved is left alone.
func (t *Turns) Check(ctx context.Context, u unit.Unit, message string) ([]Violation, error) {
	t.locker().Lock()
	defer t.locker().Unlock()
	d, ok, err := t.live(ctx, u)
	if err != nil || !ok {
		return nil, err
	}
	return t.settle(ctx, d, message)
}

func (t *Turns) live(ctx context.Context, u unit.Unit) (dossier.Dossier, bool, error) {
	d, ok, err := t.Dossiers.Of(ctx, u)
	if err != nil || !ok || (d.Status != dossier.Active && d.Status != dossier.Resolved) {
		return dossier.Dossier{}, false, err
	}
	return d, true, nil
}

// HeadlessLog writes a headless turn's log entry as log/<ts>-headless-<seq>.md and commits it as the turn.
func (t *Turns) HeadlessLog(ctx context.Context, u unit.Unit, seq int64, text string) error {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	t.locker().Lock()
	defer t.locker().Unlock()
	d, ok, err := t.live(ctx, u)
	if err != nil || !ok {
		return err
	}
	message := fmt.Sprintf("turn %d", seq)
	if _, err := t.settle(ctx, d, "unchecked writes"); err != nil {
		return err
	}
	rel := path.Join(LogDir, fmt.Sprintf("%s-headless-%d.md", t.now().UTC().Format(LogTime), seq))
	if err := t.Pages.Write(ctx, d.Path, rel, []byte(strings.TrimRight(text, "\n")+"\n")); err != nil {
		return err
	}
	_, err = t.settle(ctx, d, message)
	return err
}

// LogTime is the UTC time that starts a log file's name.
const LogTime = "20060102T150405Z"

// settle restores what broke a rule, commits the rest with message and refreshes the row. The caller holds mu.
func (t *Turns) settle(ctx context.Context, d dossier.Dossier, message string) ([]Violation, error) {
	changes, err := t.Dossiers.Files.Changed(ctx, d.Path)
	if err != nil || len(changes) == 0 {
		return nil, err
	}
	var broken []Violation
	var restore []Change
	var kept []string
	for _, c := range changes {
		if merged(c.Rel) {
			continue
		}
		e, err := t.Pages.Stat(ctx, d.Path, strings.TrimSuffix(c.Rel, "/"))
		if err != nil {
			return nil, err
		}
		why := t.judge(c, e)
		if why == appendOnly {
			if why, err = t.appended(ctx, d.Path, c.Rel); err != nil {
				return nil, err
			}
		}
		if why != "" {
			broken = append(broken, Violation{Rel: c.Rel, Why: why})
			restore = append(restore, c)
			continue
		}
		kept = append(kept, c.Rel)
	}
	// Added paths go first: a symlink that took a folder's place is gone before the folder's files return.
	slices.SortStableFunc(restore, func(a, b Change) int { return order(a) - order(b) })
	for _, c := range restore {
		rel := strings.TrimSuffix(c.Rel, "/")
		if err := t.Pages.Discard(ctx, d.Path, rel); err != nil {
			return broken, err
		}
		if c.Kind != "added" {
			if err := t.Dossiers.Files.RestoreFile(ctx, d.Path, rel); err != nil {
				return broken, err
			}
		}
	}
	for _, v := range broken {
		t.event(ctx, d.Owner, message, v)
	}
	if len(kept) > 0 {
		if err := t.Dossiers.Files.Commit(ctx, d.Path, message, kept...); err != nil {
			return broken, err
		}
	}
	return broken, t.Dossiers.refresh(ctx, d, t.now())
}

// merged says rel lies in a dossier merged into this one, whose files are its own and stay as the merge
// left them.
func merged(rel string) bool {
	top, _, _ := strings.Cut(rel, "/")
	return top == dossier.MergedDir
}

func order(c Change) int {
	if c.Kind == "added" {
		return 0
	}
	return 1
}

// judge is the rule c breaks, "" for none.
func (t *Turns) judge(c Change, e Entry) string {
	rel := strings.TrimSuffix(c.Rel, "/")
	parts := strings.Split(rel, "/")
	top := parts[0]
	switch {
	case strings.HasSuffix(c.Rel, "/"):
		return "a nested repository"
	case slices.ContainsFunc(parts, func(p string) bool { return strings.HasPrefix(p, ".git") }):
		return "a git control file"
	case rel == Notes:
		return "only a compaction or a closing call rewrites notes.md"
	case !slices.Contains(WriteDirs, top) || len(parts) == 1:
		return "outside log/, sources/ and drafts/"
	case c.Kind == "deleted":
		return "a turn adds files and never deletes one"
	case e.Kind == Symlink:
		return "a symlink"
	case e.Kind != File:
		return "not a regular file"
	case e.Links > 1:
		return "a hard link"
	case t.Dossiers.Policy.MaxFile > 0 && e.Size > t.Dossiers.Policy.MaxFile:
		return fmt.Sprintf("over the %s file limit: link it instead", sizeOf(t.Dossiers.Policy.MaxFile))
	case top == LogDir && c.Kind != "added":
		return appendOnly
	}
	return ""
}

const appendOnly = "log/ is append-only: an existing log file was rewritten"

// appended is "" when rel's committed content starts its current content, else the append-only rule.
func (t *Turns) appended(ctx context.Context, dir, rel string) (string, error) {
	was, ok, err := t.Pages.Committed(ctx, dir, rel)
	if err != nil || !ok {
		return appendOnly, err
	}
	now, err := t.Pages.Read(ctx, dir, rel)
	if err != nil {
		return "", err
	}
	if !bytes.HasPrefix(now, was) {
		return appendOnly, nil
	}
	return "", nil
}

func sizeOf(n int64) string {
	if n >= dossier.MB {
		return fmt.Sprintf("%d MB", n/dossier.MB)
	}
	return fmt.Sprintf("%d bytes", n)
}

func (t *Turns) event(ctx context.Context, o dossier.Owner, message string, v Violation) {
	t.logf("dossier %s: %s restored after %s: %s", o, v.Rel, message, v.Why)
	if t.Events == nil || o.Unit.IsZero() {
		return
	}
	ev := port.Event{Unit: o.Unit, Kind: KindRestored, Key: message + ":" + v.Rel, At: t.now(), Data: v.Rel + ": " + v.Why}
	if err := t.Events(context.WithoutCancel(ctx), ev); err != nil {
		t.logf("dossier %s: recording %s: %v", o, KindRestored, err)
	}
}

func (t *Turns) now() time.Time { return t.Dossiers.Clock.Now() }

func (t *Turns) logf(format string, args ...any) {
	if t.Log != nil {
		t.Log(format, args...)
	}
}

// Opened is when d was first opened: its notes' opened time, else their first updated time.
func (t *Turns) Opened(ctx context.Context, d dossier.Dossier) (time.Time, error) {
	raw, err := t.Pages.Read(ctx, d.Path, Notes)
	if err != nil {
		return time.Time{}, err
	}
	fm, _ := splitNotes(string(raw))
	v := fm.get("opened")
	if v == "" {
		v = fm.get("updated")
	}
	if v == "" {
		return time.Time{}, errors.New("notes.md has no opened or updated time")
	}
	return time.Parse(time.RFC3339, v)
}
