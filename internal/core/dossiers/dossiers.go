// Package dossiers keeps the lifecycle of the unit and project dossiers.
package dossiers

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"time"

	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/dossier"
)

// Tree is the working side of Files: a dossier's directory and its history.
type Tree interface {
	// Ensure creates the dossier's layout and repository at dir, or completes them; it refuses a
	// dir outside the dossiers root and a state directory inside a repository checkout.
	Ensure(ctx context.Context, dir string) error
	// Usage is the bytes on disk under dir, its repository included, and the count of its files
	// outside the repository; an absent dir is empty.
	Usage(ctx context.Context, dir string) (bytes int64, files int, err error)
	// Commit commits paths with message, every change when no path is named; a path written after the
	// caller judged it stays out unless it is named.
	Commit(ctx context.Context, dir, message string, paths ...string) error
	Changed(ctx context.Context, dir string) ([]Change, error) // against the last commit
	RestoreFile(ctx context.Context, dir, rel string) error    // from the last commit
}

// Archiver is the lifecycle side of Files.
type Archiver interface {
	Archive(ctx context.Context, dir, tarball string) error
	Restore(ctx context.Context, tarball, dir string) error
	Remove(ctx context.Context, path string) error
}

// Files is implemented by internal/adapter/dossier.
type Files interface {
	Tree
	Archiver
}

type Change struct {
	Rel  string
	Kind string // added, modified, deleted
}

// Store keeps the dossiers table and answers whether an owner still has open work.
type Store interface {
	// Put inserts or replaces d's row; Path is not stored.
	Put(ctx context.Context, d dossier.Dossier) error
	// Get is o's row, with Path empty.
	Get(ctx context.Context, o dossier.Owner) (dossier.Dossier, bool, error)
	List(ctx context.Context) ([]dossier.Dossier, error)
	// Open reports whether o has an open item or a live entry; a project, whether its entry is open.
	Open(ctx context.Context, o dossier.Owner) (bool, error)
}

type Service struct {
	Store    Store
	Files    Files
	Policy   dossier.Policy
	StateDir string
	Clock    port.Clock
}

// Open creates or revives o's dossier.
func (s *Service) Open(ctx context.Context, o dossier.Owner) (dossier.Dossier, error) {
	d, ok, err := s.Get(ctx, o)
	if err != nil {
		return dossier.Dossier{}, err
	}
	if !ok {
		if d, err = s.fresh(o); err != nil {
			return dossier.Dossier{}, err
		}
	}
	return s.revive(ctx, d)
}

func (s *Service) Get(ctx context.Context, o dossier.Owner) (dossier.Dossier, bool, error) {
	if _, err := dossier.Rel(o); err != nil {
		return dossier.Dossier{}, false, err
	}
	d, ok, err := s.Store.Get(ctx, o)
	if err != nil || !ok {
		return dossier.Dossier{}, false, err
	}
	d, err = s.located(d)
	return d, err == nil, err
}

// Resolved starts the archive countdown of o's active dossier; it does nothing to an absent one.
func (s *Service) Resolved(ctx context.Context, o dossier.Owner, at time.Time) error {
	d, ok, err := s.Get(ctx, o)
	if err != nil || !ok || d.Status != dossier.Active {
		return err
	}
	d.Status, d.ResolvedAt = dossier.Resolved, at
	return s.Store.Put(ctx, d)
}

// Reopened makes o's dossier active again, restoring its archive; it creates none.
func (s *Service) Reopened(ctx context.Context, o dossier.Owner) error {
	d, ok, err := s.Get(ctx, o)
	if err != nil || !ok || d.Status == dossier.Active {
		return err
	}
	_, err = s.revive(ctx, d)
	return err
}

// Sweep is the lifecycle job: it refreshes each dossier's usage, archives a dossier
// ArchiveAfter its owner resolved and purges an archive PurgeAfter it was made. Before either,
// it asks the store whether the owner is open again, and revives the dossier when it is.
func (s *Service) Sweep(ctx context.Context) (archived, purged int, err error) {
	all, err := s.Store.List(ctx)
	if err != nil {
		return 0, 0, err
	}
	var errs []error
	for _, d := range all {
		if err := ctx.Err(); err != nil {
			return archived, purged, err
		}
		d, err := s.located(d)
		if err == nil {
			var a, p bool
			a, p, err = s.sweep(ctx, d)
			archived, purged = archived+count(a), purged+count(p)
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("dossier %s: %w", d.Owner, err))
		}
	}
	return archived, purged, errors.Join(errs...)
}

func count(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *Service) sweep(ctx context.Context, d dossier.Dossier) (archived, purged bool, err error) {
	now := s.Clock.Now()
	switch d.Status {
	case dossier.Active:
		return false, false, s.refresh(ctx, d, now)
	case dossier.Resolved:
		if now.Before(d.ResolvedAt.Add(s.Policy.ArchiveAfter)) {
			return false, false, s.refresh(ctx, d, now)
		}
		if open, err := s.Store.Open(ctx, d.Owner); err != nil || open {
			if err == nil {
				_, err = s.revive(ctx, d)
			}
			return false, false, err
		}
		return true, false, s.archive(ctx, d, now)
	case dossier.Archived:
		if now.Before(d.PurgeAfter) {
			return false, false, nil
		}
		if open, err := s.Store.Open(ctx, d.Owner); err != nil || open {
			if err == nil {
				_, err = s.revive(ctx, d)
			}
			return false, false, err
		}
		if err := s.Files.Remove(ctx, dossier.Tarball(s.StateDir, d.RelPath)); err != nil {
			return false, false, err
		}
		d.Status, d.Bytes, d.Files = dossier.Purged, 0, 0
		return false, true, s.Store.Put(ctx, d)
	}
	return false, false, nil
}

// refresh stores d's current usage; a change in it counts as a write at now. Sweep never
// commits: what changed since the last commit is the turn's to judge.
func (s *Service) refresh(ctx context.Context, d dossier.Dossier, now time.Time) error {
	bytes, files, err := s.Files.Usage(ctx, d.Path)
	if err != nil {
		return err
	}
	if bytes == d.Bytes && files == d.Files {
		return nil
	}
	d.Bytes, d.Files, d.LastWriteAt = bytes, files, now
	return s.Store.Put(ctx, d)
}

func (s *Service) archive(ctx context.Context, d dossier.Dossier, now time.Time) error {
	tarball := dossier.Tarball(s.StateDir, d.RelPath)
	if err := s.Files.Archive(ctx, d.Path, tarball); err != nil {
		return err
	}
	bytes, _, err := s.Files.Usage(ctx, tarball)
	if err != nil {
		return err
	}
	d.Status, d.ArchivedAt, d.PurgeAfter, d.Bytes = dossier.Archived, now, now.Add(s.Policy.PurgeAfter), bytes
	if err := s.Store.Put(ctx, d); err != nil {
		return err
	}
	return s.Files.Remove(ctx, d.Path)
}

// revive makes d active on disk and in the store: an archive is restored, a purged or
// missing dossier, or one whose tarball is gone, starts empty.
func (s *Service) revive(ctx context.Context, d dossier.Dossier) (dossier.Dossier, error) {
	tarball := dossier.Tarball(s.StateDir, d.RelPath)
	if d.Status == dossier.Archived {
		if err := s.Files.Restore(ctx, tarball, d.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return dossier.Dossier{}, err
		}
	}
	if err := s.Files.Ensure(ctx, d.Path); err != nil {
		return dossier.Dossier{}, err
	}
	bytes, files, err := s.Files.Usage(ctx, d.Path)
	if err != nil {
		return dossier.Dossier{}, err
	}
	prev := d
	d.Status, d.ResolvedAt, d.ArchivedAt, d.PurgeAfter = dossier.Active, time.Time{}, time.Time{}, time.Time{}
	if bytes != d.Bytes || files != d.Files || d.LastWriteAt.IsZero() {
		d.Bytes, d.Files, d.LastWriteAt = bytes, files, s.Clock.Now()
	}
	if d != prev {
		if err := s.Store.Put(ctx, d); err != nil {
			return dossier.Dossier{}, err
		}
	}
	if prev.Status == dossier.Archived {
		return d, s.Files.Remove(ctx, tarball)
	}
	return d, nil
}

func (s *Service) fresh(o dossier.Owner) (dossier.Dossier, error) {
	rel, err := dossier.Rel(o)
	if err != nil {
		return dossier.Dossier{}, err
	}
	return s.located(dossier.Dossier{Owner: o, RelPath: rel, Status: dossier.Purged})
}

// located sets d.Path under the service's state directory, refusing a stored path that
// leaves the dossiers root.
func (s *Service) located(d dossier.Dossier) (dossier.Dossier, error) {
	rel, err := dossier.Rel(d.Owner)
	if err != nil {
		return dossier.Dossier{}, err
	}
	if filepath.Clean(d.RelPath) != rel {
		return dossier.Dossier{}, fmt.Errorf("dossier %s: the stored path %q is not %q", d.Owner, d.RelPath, rel)
	}
	if d.Path, err = dossier.Dir(s.StateDir, d.Owner); err != nil {
		return dossier.Dossier{}, err
	}
	return d, nil
}

// Report sums the dossiers on disk and their archives, and names each dossier over the soft
// cap. Transcripts are the caller's: the service does not know them.
func (s *Service) Report(ctx context.Context) (Report, error) {
	all, err := s.Store.List(ctx)
	if err != nil {
		return Report{}, err
	}
	var r Report
	for _, d := range all {
		if d.Status != dossier.Active && d.Status != dossier.Resolved {
			continue
		}
		d, err := s.located(d)
		if err != nil {
			return Report{}, err
		}
		bytes, _, err := s.Files.Usage(ctx, d.Path)
		if err != nil {
			return Report{}, err
		}
		r.Dossiers.Bytes += bytes
		r.Dossiers.Count++
		if s.Policy.SoftCap > 0 && bytes > s.Policy.SoftCap {
			r.OverCap = append(r.OverCap, d.Owner)
		}
	}
	bytes, files, err := s.Files.Usage(ctx, filepath.Join(dossier.Root(s.StateDir), dossier.ArchiveDir))
	if err != nil {
		return Report{}, err
	}
	r.Archives = Usage{Bytes: bytes, Count: files}
	return r, nil
}

// OverTotal reports whether the dossiers and their archives pass the policy's total cap.
func (r Report) OverTotal(p dossier.Policy) bool {
	return p.TotalCap > 0 && r.Dossiers.Bytes+r.Archives.Bytes > p.TotalCap
}

type Report struct {
	Dossiers, Archives, Transcripts Usage
	OverCap                         []dossier.Owner
}

type Usage struct {
	Bytes int64
	Count int
}
