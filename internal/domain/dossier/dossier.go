// Package dossier holds the working files the loop keeps per task, or per unit no task holds.
package dossier

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/domain/unit"
)

// Owner is whose dossier it is: a unit (a task is the unit of its provider), or, with Merged set, the dossier
// that a merge of tasks put inside Unit's as merged/<Merged>.
type Owner struct {
	Unit   unit.Unit
	Merged string
}

type Status string // "active", "resolved", "archived", "purged"

const (
	Active   Status = "active"
	Resolved Status = "resolved"
	Archived Status = "archived"
	Purged   Status = "purged"
)

type Dossier struct {
	Owner   Owner
	Path    string // absolute, computed from the state dir and RelPath on read
	RelPath string // stored: relative to <state dir>/dossiers
	Status  Status
	Bytes   int64
	Files   int

	LastWriteAt, ResolvedAt, ArchivedAt, PurgeAfter time.Time
}

type Policy struct {
	ArchiveAfter time.Duration // 30 days after resolution
	PurgeAfter   time.Duration // 400 days after archive
	SoftCap      int64         // 50 MB per dossier
	TotalCap     int64         // 2 GB all dossiers
	MaxFile      int64         // 5 MB; larger files are refused, linked instead
	CompactAt    int64         // log/ bytes that trigger compaction, 256 KB
}

const (
	KB = 1 << 10
	MB = 1 << 20
)

// DefaultPolicy is the policy of a config that sets none.
var DefaultPolicy = Policy{
	ArchiveAfter: 30 * 24 * time.Hour,
	PurgeAfter:   400 * 24 * time.Hour,
	SoftCap:      50 * MB,
	TotalCap:     2048 * MB,
	MaxFile:      5 * MB,
	CompactAt:    256 * KB,
}

const (
	// RootDir is the directory under the state directory that holds every dossier.
	RootDir = "dossiers"
	// ArchiveDir, under RootDir, holds the archived dossiers' tarballs.
	ArchiveDir = ".archive"
	// MergedDir, inside a dossier, holds the dossiers merged into it; no unit provider may take its name.
	MergedDir = "merged"
)

var ErrOwner = errors.New("dossier owner")

// Root is the directory of every dossier under stateDir.
func Root(stateDir string) string { return filepath.Join(stateDir, RootDir) }

// Dir is the dossier directory under stateDir: dossiers/<provider>/<key segments># (key split on
// "/", each url.PathEscape'd with a leading "." as "%2E", "." and ".." refused), with merged/<name>
// after it for a merged dossier.
func Dir(stateDir string, o Owner) (string, error) {
	if !filepath.IsAbs(stateDir) {
		return "", fmt.Errorf("the state directory %q is not absolute", stateDir)
	}
	rel, err := Rel(o)
	if err != nil {
		return "", err
	}
	return filepath.Join(Root(stateDir), rel), nil
}

// Tarball is where the archive of the dossier at rel lives under stateDir.
func Tarball(stateDir, rel string) string {
	return filepath.Join(Root(stateDir), ArchiveDir, rel+".tar.gz")
}

// Leaf ends the last directory of every dossier. An escaped segment never holds a raw "#", so no
// dossier directory is an ancestor of another but for the dossiers merged into it, and no key segment
// names a dossier's own files.
const Leaf = "#"

// Rel is o's directory relative to the dossiers root: each segment escaped, a leading "." as
// "%2E", and Leaf after the last one.
func Rel(o Owner) (string, error) {
	segs, err := o.segments()
	if err != nil {
		return "", err
	}
	for i, s := range segs {
		segs[i] = escape(s)
	}
	segs[len(segs)-1] += Leaf
	if o.Merged != "" {
		segs = append(segs, MergedDir, escape(o.Merged))
	}
	return filepath.Join(segs...), nil
}

func escape(s string) string {
	e := url.PathEscape(s)
	if strings.HasPrefix(e, ".") {
		e = "%2E" + e[1:]
	}
	return e
}

// OwnerOf is the owner whose dossier lives at rel, the inverse of Rel.
func OwnerOf(rel string) (Owner, error) {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	merged := ""
	if n := len(parts); n > 3 && parts[n-2] == MergedDir && strings.HasSuffix(parts[n-3], Leaf) {
		name, err := url.PathUnescape(parts[n-1])
		if err != nil {
			return Owner{}, fmt.Errorf("%w: %q: %v", ErrOwner, rel, err)
		}
		merged, parts = name, parts[:n-2]
	}
	last, ok := strings.CutSuffix(parts[len(parts)-1], Leaf)
	if !ok {
		return Owner{}, fmt.Errorf("%w: %q is not a dossier path", ErrOwner, rel)
	}
	parts[len(parts)-1] = last
	for i, p := range parts {
		s, err := url.PathUnescape(p)
		if err != nil {
			return Owner{}, fmt.Errorf("%w: %q: %v", ErrOwner, rel, err)
		}
		parts[i] = s
	}
	o, err := ownerOfParts(parts)
	if err != nil {
		return Owner{}, err
	}
	o.Merged = merged
	if back, err := Rel(o); err != nil || filepath.ToSlash(back) != filepath.ToSlash(rel) {
		return Owner{}, fmt.Errorf("%w: %q is not a dossier path", ErrOwner, rel)
	}
	return o, nil
}

// String is the owner's key in the dossiers table: "<provider>/<key>", or "merged/<name>/<provider>/<key>"
// with the name path-escaped.
func (o Owner) String() string {
	s := string(o.Unit.Provider) + "/" + o.Unit.Key
	if o.Merged != "" {
		return MergedDir + "/" + url.PathEscape(o.Merged) + "/" + s
	}
	return s
}

// ParseOwner reads what String wrote.
func ParseOwner(s string) (Owner, error) {
	merged := ""
	if rest, ok := strings.CutPrefix(s, MergedDir+"/"); ok {
		name, base, ok := strings.Cut(rest, "/")
		if !ok {
			return Owner{}, fmt.Errorf("%w: %q merges into nothing", ErrOwner, s)
		}
		n, err := url.PathUnescape(name)
		if err != nil {
			return Owner{}, fmt.Errorf("%w: %q: %v", ErrOwner, s, err)
		}
		merged, s = n, base
	}
	head, rest, ok := strings.Cut(s, "/")
	if !ok {
		return Owner{}, fmt.Errorf("%w: %q has no provider", ErrOwner, s)
	}
	o := Owner{Unit: unit.Unit{Provider: unit.ProviderID(head), Key: rest}, Merged: merged}
	if _, err := o.segments(); err != nil {
		return Owner{}, err
	}
	return o, nil
}

func ownerOfParts(parts []string) (Owner, error) {
	if len(parts) < 2 {
		return Owner{}, fmt.Errorf("%w: %q has no key", ErrOwner, strings.Join(parts, "/"))
	}
	return Owner{Unit: unit.Unit{Provider: unit.ProviderID(parts[0]), Key: strings.Join(parts[1:], "/")}}, nil
}

func (o Owner) segments() ([]string, error) {
	if o.Unit.IsZero() {
		return nil, fmt.Errorf("%w: no unit", ErrOwner)
	}
	if o.Merged != "" {
		if err := segment(o.Merged); err != nil {
			return nil, fmt.Errorf("%w: the merged name %q holds %v", ErrOwner, o.Merged, err)
		}
	}
	p := string(o.Unit.Provider)
	if err := segment(p); err != nil || p == MergedDir || strings.HasPrefix(p, ".") || strings.Contains(p, "/") {
		return nil, fmt.Errorf("%w: the provider %q cannot name a dossier directory", ErrOwner, p)
	}
	segs := []string{p}
	for _, s := range strings.Split(o.Unit.Key, "/") {
		if err := segment(s); err != nil {
			return nil, fmt.Errorf("%w: the key %q holds %v", ErrOwner, o.Unit.Key, err)
		}
		segs = append(segs, s)
	}
	return segs, nil
}

func segment(s string) error {
	switch {
	case s == "":
		return errors.New("an empty segment")
	case s == "." || s == "..":
		return fmt.Errorf("the segment %q", s)
	case strings.ContainsRune(s, 0):
		return errors.New("a NUL byte")
	}
	return nil
}
