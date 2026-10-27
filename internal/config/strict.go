package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/wloophq/wloop/internal/intent/builtin"
)

// UnknownKeysError names the keys of a config file that no setting reads.
type UnknownKeysError struct {
	File string
	Keys []string // paths such as "sweep.fsat" or "projects[0].agent_hom"
	// Near are the grammar's guesses, by key: "sweep.fsat" did you mean "fast".
	Near map[string]string
}

func (e *UnknownKeysError) Error() string {
	noun := "key"
	if len(e.Keys) > 1 {
		noun = "keys"
	}
	msg := fmt.Sprintf("%s: unknown %s %s", e.File, noun, strings.Join(e.Keys, ", "))
	for _, k := range e.Keys {
		if n := e.Near[k]; n != "" {
			msg += fmt.Sprintf("; %s: did you mean %q?", k, n)
		}
	}
	return msg
}

// decodeStrict decodes raw, a TOML document, into c after the grammar
// checked it: a key no setting reads is an UnknownKeysError, and a value of
// the wrong kind or outside its set names its key.
func decodeStrict(file string, raw []byte, c *Config) error {
	var tree map[string]any
	if err := toml.Unmarshal(raw, &tree); err != nil {
		return fmt.Errorf("parsing %s: %w", file, err)
	}
	unknown := &UnknownKeysError{File: file, Near: map[string]string{}}
	var bad []error
	for _, p := range builtin.Check(builtin.SurfaceConfig, tree) {
		if !p.Unknown {
			bad = append(bad, fmt.Errorf("%s: %s: %s", file, p.Path, p.Msg))
			continue
		}
		unknown.Keys = append(unknown.Keys, p.Path)
		if p.Near != "" {
			unknown.Near[p.Path] = p.Near
		}
	}
	if len(unknown.Keys) > 0 {
		slices.Sort(unknown.Keys)
		return unknown
	}
	if len(bad) > 0 {
		return errors.Join(bad...)
	}
	// Config's keys are its json tags, which config get prints too.
	asJSON, err := json.Marshal(tree)
	if err != nil {
		return fmt.Errorf("parsing %s: %w", file, err)
	}
	dec := json.NewDecoder(bytes.NewReader(asJSON))
	dec.DisallowUnknownFields()
	if err := dec.Decode(c); err != nil {
		return fmt.Errorf("parsing %s: %w", file, err)
	}
	return nil
}

// jsonName is f's key in the file, "" when its tag names none.
func jsonName(f reflect.StructField) string {
	name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
	return name
}
