package manifest

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

const defaultField = `[^/?#\s]+`

var fieldRe = regexp.MustCompile(`\{([a-z][a-z0-9_]*)\}`)

// Compiled is a [Codec] ready to read keys and addresses, which the host
// uses for a plugin's units whether or not the plugin runs.
type Compiled struct {
	keys     []template
	urls     []template
	label    string
	foldCase bool
	targets  *regexp.Regexp
}

type template struct {
	text   string
	re     *regexp.Regexp
	fields []string
}

// Compile checks c's templates and patterns.
func (c Codec) Compile() (*Compiled, error) {
	if len(c.Keys) == 0 {
		return nil, errors.New("keys names no template")
	}
	fields := map[string]string{}
	for name, p := range c.Fields {
		if _, err := regexp.Compile(p); err != nil {
			return nil, fmt.Errorf("field %q: %w", name, err)
		}
		fields[name] = p
	}
	out := &Compiled{label: c.Label, foldCase: c.FoldCase}
	for _, k := range c.Keys {
		t, err := compile(k, fields, "^", "$")
		if err != nil {
			return nil, fmt.Errorf("key %q: %w", k, err)
		}
		out.keys = append(out.keys, t)
	}
	for _, u := range c.URLs {
		rest, ok := strings.CutPrefix(u, "https://")
		if !ok {
			return nil, fmt.Errorf("url %q does not start with https://", u)
		}
		t, err := compile(rest, fields, `^https?://`, `(?:[/?#].*)?$`)
		if err != nil {
			return nil, fmt.Errorf("url %q: %w", u, err)
		}
		t.text = u
		if !slices.ContainsFunc(out.keys, func(k template) bool { return sameFields(k, t) }) {
			return nil, fmt.Errorf("url %q: no key template has its fields %v", u, t.fields)
		}
		out.urls = append(out.urls, t)
	}
	if c.Label != "" {
		if _, err := compile(c.Label, fields, "^", "$"); err != nil {
			return nil, fmt.Errorf("label %q: %w", c.Label, err)
		}
	}
	if c.TargetsRe != "" {
		re, err := regexp.Compile(c.TargetsRe)
		if err != nil {
			return nil, fmt.Errorf("targets: %w", err)
		}
		out.targets = re
	}
	for _, name := range slices.Sorted(maps.Keys(fields)) {
		used := false
		for _, t := range out.keys {
			used = used || slices.Contains(t.fields, name)
		}
		if !used {
			return nil, fmt.Errorf("field %q is in no key template", name)
		}
	}
	return out, nil
}

func compile(text string, fields map[string]string, head, tail string) (template, error) {
	t := template{text: text}
	var re strings.Builder
	re.WriteString(head)
	last := 0
	for _, m := range fieldRe.FindAllStringSubmatchIndex(text, -1) {
		name := text[m[2]:m[3]]
		if slices.Contains(t.fields, name) {
			return template{}, fmt.Errorf("field %q appears twice", name)
		}
		t.fields = append(t.fields, name)
		if strings.ContainsAny(text[last:m[0]], "{}") {
			return template{}, errors.New("a brace is not a {field}")
		}
		re.WriteString(regexp.QuoteMeta(text[last:m[0]]))
		p, ok := fields[name]
		if !ok {
			p = defaultField
		}
		re.WriteString("(?P<" + name + ">" + p + ")")
		last = m[1]
	}
	lit := text[last:]
	if strings.ContainsAny(lit, "{}") {
		return template{}, errors.New("a brace is not a {field}")
	}
	re.WriteString(regexp.QuoteMeta(lit))
	re.WriteString(tail)
	var err error
	if t.re, err = regexp.Compile(re.String()); err != nil {
		return template{}, err
	}
	return t, nil
}

func sameFields(a, b template) bool {
	return slices.Equal(slices.Sorted(slices.Values(a.fields)), slices.Sorted(slices.Values(b.fields)))
}

func (t template) match(s string) (map[string]string, bool) {
	m := t.re.FindStringSubmatch(s)
	if m == nil {
		return nil, false
	}
	out := map[string]string{}
	for i, name := range t.re.SubexpNames() {
		if name != "" {
			out[name] = m[i]
		}
	}
	return out, true
}

func (t template) fill(values map[string]string) (string, bool) {
	ok := true
	s := fieldRe.ReplaceAllStringFunc(t.text, func(f string) string {
		v, has := values[f[1:len(f)-1]]
		ok = ok && has
		return v
	})
	return s, ok
}

func (c *Compiled) fieldsOf(key string) (template, map[string]string, bool) {
	for _, t := range c.keys {
		if v, ok := t.match(key); ok {
			return t, v, true
		}
	}
	return template{}, nil, false
}

// Valid says why key is not a unit key of the codec.
func (c *Compiled) Valid(key string) error {
	if _, _, ok := c.fieldsOf(key); !ok {
		return fmt.Errorf("not a unit key: %q", key)
	}
	return nil
}

// URL is the web address of key; "" for a key with none.
func (c *Compiled) URL(key string) string {
	k, v, ok := c.fieldsOf(key)
	if !ok {
		return ""
	}
	for _, u := range c.urls {
		if sameFields(k, u) {
			s, _ := u.fill(v)
			return s
		}
	}
	return ""
}

// KeyOf is the key an address names; false for an address of no unit.
func (c *Compiled) KeyOf(url string) (string, bool) {
	url = strings.TrimSpace(url)
	for _, u := range c.urls {
		v, ok := u.match(url)
		if !ok {
			continue
		}
		for _, k := range c.keys {
			if sameFields(k, u) {
				key, _ := k.fill(v)
				if c.Valid(key) == nil {
					return key, true
				}
			}
		}
	}
	return "", false
}

// Label is key as a person reads it.
func (c *Compiled) Label(key string) string {
	_, v, ok := c.fieldsOf(key)
	if !ok || c.label == "" {
		return key
	}
	l := template{text: c.label}
	s, ok := l.fill(v)
	if !ok {
		return key
	}
	return s
}

// Equal says whether keys a and b name the same unit.
func (c *Compiled) Equal(a, b string) bool {
	if c.foldCase {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// Takes says whether the plugin posts drafts to target.
func (c *Compiled) Takes(target string) bool {
	return c.targets != nil && c.targets.MatchString(target)
}
