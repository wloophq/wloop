package sqlite

import (
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/wloophq/wloop/internal/domain/unit"
)

// The dossiers table's owners and paths, as releases before version 15 kept them. Version 7 moves dossier rows
// between owners, and a rekey of a store that still holds the table moves them too; version 15 drops it.

// dossierOwner is whose dossier a row is: a unit (a task is the unit of its provider), or, with merged set,
// the dossier a merge of tasks put inside the unit's as merged/<merged>.
type dossierOwner struct {
	unit   unit.Unit
	merged string
}

const (
	dossierArchived = "archived"
	dossierPurged   = "purged"
	// dossierMergedDir, inside a dossier, holds the dossiers merged into it.
	dossierMergedDir = "merged"
	// dossierLeaf ends the last directory of every dossier.
	dossierLeaf = "#"
)

var errDossierOwner = errors.New("dossier owner")

// rel is o's directory relative to the dossiers root: each segment escaped, a leading "." as "%2E", and
// dossierLeaf after the last one.
func (o dossierOwner) rel() (string, error) {
	segs, err := o.segments()
	if err != nil {
		return "", err
	}
	for i, s := range segs {
		segs[i] = escapeDossierSegment(s)
	}
	segs[len(segs)-1] += dossierLeaf
	if o.merged != "" {
		segs = append(segs, dossierMergedDir, escapeDossierSegment(o.merged))
	}
	return filepath.Join(segs...), nil
}

func escapeDossierSegment(s string) string {
	e := url.PathEscape(s)
	if strings.HasPrefix(e, ".") {
		e = "%2E" + e[1:]
	}
	return e
}

// String is o's key in the dossiers table: "<provider>/<key>", or "merged/<name>/<provider>/<key>" with the
// name path-escaped.
func (o dossierOwner) String() string {
	s := string(o.unit.Provider) + "/" + o.unit.Key
	if o.merged != "" {
		return dossierMergedDir + "/" + url.PathEscape(o.merged) + "/" + s
	}
	return s
}

// parseDossierOwner reads what String wrote.
func parseDossierOwner(s string) (dossierOwner, error) {
	merged := ""
	if rest, ok := strings.CutPrefix(s, dossierMergedDir+"/"); ok {
		name, base, ok := strings.Cut(rest, "/")
		if !ok {
			return dossierOwner{}, fmt.Errorf("%w: %q merges into nothing", errDossierOwner, s)
		}
		n, err := url.PathUnescape(name)
		if err != nil {
			return dossierOwner{}, fmt.Errorf("%w: %q: %v", errDossierOwner, s, err)
		}
		merged, s = n, base
	}
	head, rest, ok := strings.Cut(s, "/")
	if !ok {
		return dossierOwner{}, fmt.Errorf("%w: %q has no provider", errDossierOwner, s)
	}
	o := dossierOwner{unit: unit.Unit{Provider: unit.ProviderID(head), Key: rest}, merged: merged}
	if _, err := o.segments(); err != nil {
		return dossierOwner{}, err
	}
	return o, nil
}

func (o dossierOwner) segments() ([]string, error) {
	if o.unit.IsZero() {
		return nil, fmt.Errorf("%w: no unit", errDossierOwner)
	}
	if o.merged != "" {
		if err := dossierSegment(o.merged); err != nil {
			return nil, fmt.Errorf("%w: the merged name %q holds %v", errDossierOwner, o.merged, err)
		}
	}
	p := string(o.unit.Provider)
	if err := dossierSegment(p); err != nil || p == dossierMergedDir || strings.HasPrefix(p, ".") || strings.Contains(p, "/") {
		return nil, fmt.Errorf("%w: the provider %q cannot name a dossier directory", errDossierOwner, p)
	}
	segs := []string{p}
	for _, s := range strings.Split(o.unit.Key, "/") {
		if err := dossierSegment(s); err != nil {
			return nil, fmt.Errorf("%w: the key %q holds %v", errDossierOwner, o.unit.Key, err)
		}
		segs = append(segs, s)
	}
	return segs, nil
}

func dossierSegment(s string) error {
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
