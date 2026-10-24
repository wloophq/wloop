package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/wloophq/wloop/internal/config"
)

// jsonDoc is a JSON object that keeps its keys in the order the file has them.
type jsonDoc struct {
	keys []string
	vals map[string]json.RawMessage
}

func parseJSONDoc(raw []byte) (*jsonDoc, error) {
	d := &jsonDoc{vals: map[string]json.RawMessage{}}
	if len(bytes.TrimSpace(raw)) == 0 {
		return d, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, fmt.Errorf("not a JSON object")
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := tok.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		d.put(key, v)
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *jsonDoc) put(key string, v json.RawMessage) {
	if _, ok := d.vals[key]; !ok {
		d.keys = append(d.keys, key)
	}
	d.vals[key] = v
}

func (d *jsonDoc) has(key string) bool {
	_, ok := d.vals[key]
	return ok
}

func (d *jsonDoc) child(key string) (*jsonDoc, error) {
	raw, ok := d.vals[key]
	if !ok || string(bytes.TrimSpace(raw)) == "null" {
		return &jsonDoc{vals: map[string]json.RawMessage{}}, nil
	}
	c, err := parseJSONDoc(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", key, err)
	}
	return c, nil
}

// set writes v under path. A value that marshals to the same JSON as the
// file's is left as written; an empty one is not added where the file has
// no key.
func (d *jsonDoc) set(path []string, v any) error {
	enc, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return d.setRaw(path, enc)
}

func (d *jsonDoc) setRaw(path []string, enc json.RawMessage) error {
	key := path[0]
	if len(path) > 1 {
		c, err := d.child(key)
		if err != nil {
			return err
		}
		had := d.has(key)
		if err := c.setRaw(path[1:], enc); err != nil {
			return err
		}
		if !had && len(c.keys) == 0 {
			return nil
		}
		d.put(key, c.marshal())
		return nil
	}
	if old, ok := d.vals[key]; ok && sameJSON(old, enc) {
		return nil
	}
	if !d.has(key) && emptyJSON(enc) {
		return nil
	}
	d.put(key, enc)
	return nil
}

func sameJSON(a, b []byte) bool {
	var x, y bytes.Buffer
	if json.Compact(&x, a) != nil || json.Compact(&y, b) != nil {
		return false
	}
	return bytes.Equal(x.Bytes(), y.Bytes())
}

func emptyJSON(enc []byte) bool {
	return slices.Contains([]string{`""`, `[]`, `{}`, `null`}, string(enc))
}

func (d *jsonDoc) marshal() json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range d.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		key, _ := json.Marshal(k)
		b.Write(key)
		b.WriteByte(':')
		b.Write(d.vals[k])
	}
	b.WriteByte('}')
	return b.Bytes()
}

// text is the document as the wizard writes it: two-space indented, one
// trailing newline.
func (d *jsonDoc) text() []byte { return indentJSON(d.marshal()) }

func indentJSON(raw []byte) []byte {
	var b bytes.Buffer
	if err := json.Indent(&b, raw, "", "  "); err != nil {
		return raw
	}
	b.WriteByte('\n')
	return b.Bytes()
}

// mergeInitConfig is the config file raw with the wizard's answers written
// in, every other key kept where it was; a path the file spells another way,
// such as with ~, stays as written.
func mergeInitConfig(raw []byte, a initAnswers) ([]byte, error) {
	d, err := parseJSONDoc(raw)
	if err != nil {
		return nil, err
	}
	for _, p := range []struct {
		key, v string
		read   func(string) string
	}{
		{"projects_dir", a.ProjectsDir, config.ExpandHome},
		{"profile", a.Profile, config.ResolveProfile},
	} {
		var old string
		if json.Unmarshal(d.vals[p.key], &old) == nil && p.read(old) == p.v {
			continue
		}
		if err := d.set([]string{p.key}, p.v); err != nil {
			return nil, err
		}
	}
	sets := []struct {
		path []string
		v    any
	}{
		{[]string{"agent"}, a.Agent},
		{[]string{"scope", "repos"}, nonNil(a.Repos)},
		{[]string{"scope", "teams"}, nonNil(a.Teams)},
		{[]string{"scope", "people"}, nonNil(a.People)},
		{[]string{"slack", "client_id"}, a.SlackClientID},
	}
	for _, s := range sets {
		if err := d.set(s.path, s.v); err != nil {
			return nil, err
		}
	}
	return d.text(), nil
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}
