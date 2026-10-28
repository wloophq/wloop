// Package manifest reads and validates a plugin's plugin.toml: its id, the
// kinds its binary serves, the protocol version it speaks and one table per
// kind, whose contents the kind's SDK package defines and decodes.
package manifest

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"

	"github.com/pelletier/go-toml/v2"

	"github.com/wloophq/wloop/pkg/plugin"
)

// File is the manifest's name in a plugin's directory.
const File = "plugin.toml"

// idRe is the host's rule for a unit provider id, which a source plugin's id is.
var idRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

var kindRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

var reserved = []string{"id", "kinds", "protocol"}

// Manifest is a validated plugin.toml.
type Manifest struct {
	// ID is the plugin's id; a source plugin's units carry it as provider.
	ID string
	// Kinds are the kinds the plugin's binary serves.
	Kinds []plugin.Kind
	// Protocol is the plugin protocol version the binary speaks.
	Protocol int
	// Dir is the directory Load read the manifest from, empty after Parse.
	Dir string

	tables map[plugin.Kind]map[string]any
}

// Load reads and validates dir's plugin.toml.
func Load(dir string) (*Manifest, error) {
	path := filepath.Join(dir, File)
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m, err := Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	m.Dir = dir
	return m, nil
}

// Parse validates a plugin.toml's content. Every top-level key is id, kinds,
// protocol or the table of a declared kind; every declared kind has a table.
func Parse(raw []byte) (*Manifest, error) {
	var doc map[string]any
	if err := toml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", File, err)
	}
	var head struct {
		ID       string        `toml:"id"`
		Kinds    []plugin.Kind `toml:"kinds"`
		Protocol int           `toml:"protocol"`
	}
	if err := toml.Unmarshal(raw, &head); err != nil {
		return nil, fmt.Errorf("%s: %w", File, err)
	}
	m := &Manifest{ID: head.ID, Kinds: head.Kinds, Protocol: head.Protocol, tables: map[plugin.Kind]map[string]any{}}
	var errs []error
	if !idRe.MatchString(m.ID) {
		errs = append(errs, fmt.Errorf("id %q is not a lowercase name of at most 32 letters, digits, - and _", m.ID))
	}
	if _, ok := plugin.VersionedPlugins(nil)[m.Protocol]; !ok {
		errs = append(errs, fmt.Errorf("protocol %d is not one this host speaks (%v)", m.Protocol, slices.Sorted(maps.Keys(plugin.VersionedPlugins(nil)))))
	}
	if len(m.Kinds) == 0 {
		errs = append(errs, errors.New("kinds names no kind"))
	}
	for i, k := range m.Kinds {
		switch {
		case !kindRe.MatchString(string(k)):
			errs = append(errs, fmt.Errorf("kind %q is not a lowercase name", k))
		case slices.Contains(m.Kinds[:i], k):
			errs = append(errs, fmt.Errorf("kind %q is listed twice", k))
		}
		table, ok := doc[string(k)].(map[string]any)
		if !ok {
			errs = append(errs, fmt.Errorf("kind %q has no [%s] table", k, k))
			continue
		}
		m.tables[k] = table
	}
	for _, key := range slices.Sorted(maps.Keys(doc)) {
		if slices.Contains(reserved, key) || slices.Contains(m.Kinds, plugin.Kind(key)) {
			continue
		}
		if _, ok := doc[key].(map[string]any); ok {
			errs = append(errs, fmt.Errorf("table [%s] is not a kind listed in kinds", key))
			continue
		}
		errs = append(errs, fmt.Errorf("unknown key %q", key))
	}
	if err := errors.Join(errs...); err != nil {
		return nil, fmt.Errorf("%s: %w", File, err)
	}
	return m, nil
}

// Decode decodes the table of kind into v, refusing a key v does not read.
func (m *Manifest) Decode(kind plugin.Kind, v any) error {
	table, ok := m.tables[kind]
	if !ok {
		return fmt.Errorf("%s of %s declares no kind %q", File, m.ID, kind)
	}
	raw, err := toml.Marshal(table)
	if err != nil {
		return err
	}
	dec := toml.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var strict *toml.StrictMissingError
		if errors.As(err, &strict) {
			return fmt.Errorf("%s of %s, [%s]: %s", File, m.ID, kind, strict.String())
		}
		return fmt.Errorf("%s of %s, [%s]: %w", File, m.ID, kind, err)
	}
	return nil
}
