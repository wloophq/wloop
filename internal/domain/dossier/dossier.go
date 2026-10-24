// Package dossier holds the working files the loop keeps per unit or project.
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

type Owner struct {
	Unit    unit.Unit // set for a unit dossier
	Project string    // slug, set for a project dossier; exactly one of the two
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
	// ProjectDir, under RootDir, holds the project dossiers; no unit provider may take its name.
	ProjectDir = "project"
)

var ErrOwner = errors.New("dossier owner")

// Root is the directory of every dossier under stateDir.
func Root(stateDir string) string { return filepath.Join(stateDir, RootDir) }

// Dir is the dossier directory under stateDir: dossiers/<provider>/<key segments># (key split on
// "/", each url.PathEscape'd with a leading "." as "%2E", "." and ".." refused) or
// dossiers/project/<slug>#.
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
// dossier directory is an ancestor of another and no key segment names a dossier's own files.
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
	if back, err := Rel(o); err != nil || filepath.ToSlash(back) != filepath.ToSlash(rel) {
		return Owner{}, fmt.Errorf("%w: %q is not a dossier path", ErrOwner, rel)
	}
	return o, nil
}

// String is the owner's key in the dossiers table: "<provider>/<key>" or "project/<slug>".
func (o Owner) String() string {
	if o.Project != "" {
		return ProjectDir + "/" + o.Project
	}
	return string(o.Unit.Provider) + "/" + o.Unit.Key
}

// ParseOwner reads what String wrote.
func ParseOwner(s string) (Owner, error) {
	head, rest, ok := strings.Cut(s, "/")
	if !ok {
		return Owner{}, fmt.Errorf("%w: %q has no provider", ErrOwner, s)
	}
	o := Owner{Unit: unit.Unit{Provider: unit.ProviderID(head), Key: rest}}
	if head == ProjectDir {
		o = Owner{Project: rest}
	}
	if _, err := o.segments(); err != nil {
		return Owner{}, err
	}
	return o, nil
}

func ownerOfParts(parts []string) (Owner, error) {
	if len(parts) < 2 {
		return Owner{}, fmt.Errorf("%w: %q has no key", ErrOwner, strings.Join(parts, "/"))
	}
	if parts[0] == ProjectDir {
		if len(parts) != 2 {
			return Owner{}, fmt.Errorf("%w: a project slug is one segment", ErrOwner)
		}
		return Owner{Project: parts[1]}, nil
	}
	return Owner{Unit: unit.Unit{Provider: unit.ProviderID(parts[0]), Key: strings.Join(parts[1:], "/")}}, nil
}

func (o Owner) segments() ([]string, error) {
	switch {
	case o.Project != "" && !o.Unit.IsZero():
		return nil, fmt.Errorf("%w: both a unit and a project", ErrOwner)
	case o.Project != "":
		if err := segment(o.Project); err != nil || strings.Contains(o.Project, "/") {
			return nil, fmt.Errorf("%w: the project slug %q", ErrOwner, o.Project)
		}
		return []string{ProjectDir, o.Project}, nil
	case o.Unit.IsZero():
		return nil, fmt.Errorf("%w: neither a unit nor a project", ErrOwner)
	}
	p := string(o.Unit.Provider)
	if err := segment(p); err != nil || p == ProjectDir || strings.HasPrefix(p, ".") || strings.Contains(p, "/") {
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
