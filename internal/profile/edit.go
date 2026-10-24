package profile

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/tag"
)

// Addition is a rule to put in the block of each of Intents at Path.
type Addition struct {
	Intents []Kind
	Path    BlockPath
	// Section names the "## " heading the text goes under; "" appends at the end.
	Section  string
	Title    string
	Override bool
	Paths    []string
	Text     string
}

type Change struct {
	File, Rel     string
	Before, After []byte
	// From is the old relative path of a rename, "" otherwise.
	From string
}

// NormalizeTag reads a key=value tag as its one spelling, key:value.
func NormalizeTag(kv string) (string, error) {
	k, v, ok := strings.Cut(kv, "=")
	if !ok {
		return "", fmt.Errorf("tag %s: write it as <key>=<value>", kv)
	}
	if !slices.ContainsFunc(tag.Keys, func(t tag.Key) bool { return t.Name == k }) {
		return "", fmt.Errorf("unknown tag key %s; the tag keys are %s", k, tagKeyNames())
	}
	n, valid := tag.Normalize(k, v)
	if !valid {
		return "", fmt.Errorf("tag %s=%s is not a valid value; %s", k, v, tagFix(k))
	}
	return k + ":" + n, nil
}

func frontMatterOf(a Addition) string {
	var lines []string
	if a.Override {
		lines = append(lines, "override = true")
	}
	if a.Title != "" {
		lines = append(lines, "title = "+tomlString(a.Title))
	}
	if len(a.Paths) > 0 {
		quoted := make([]string, len(a.Paths))
		for i, p := range a.Paths {
			quoted[i] = tomlString(p)
		}
		lines = append(lines, "paths = ["+strings.Join(quoted, ", ")+"]")
	}
	if len(lines) == 0 {
		return ""
	}
	return "+++\n" + strings.Join(lines, "\n") + "\n+++\n"
}

// tomlString is s as a TOML basic string; JSON escapes are a subset of TOML's.
func tomlString(s string) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}

// underSection appends text at the end of the "## section" part of body,
// adding the heading at the end when body has none.
func underSection(body, section, text string) string {
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	start := -1
	var open *fence
	for i, l := range lines {
		if open != nil {
			if closes(*open, l) {
				open = nil
			}
			continue
		}
		if f, ok := fenceOf(l); ok {
			open = &f
			continue
		}
		lv := headingLevel(l)
		if start < 0 && lv == 2 && strings.EqualFold(strings.TrimSpace(strings.TrimLeft(l, " #")), section) {
			start = i
			continue
		}
		if start >= 0 && lv > 0 && lv <= 2 {
			head := strings.TrimRight(strings.Join(lines[:i], "\n"), "\n")
			return head + "\n\n" + text + "\n\n" + strings.Join(lines[i:], "\n") + "\n"
		}
	}
	if start >= 0 {
		return strings.TrimRight(body, "\n") + "\n\n" + text + "\n"
	}
	if strings.TrimSpace(body) == "" {
		return "## " + section + "\n\n" + text + "\n"
	}
	return strings.TrimRight(body, "\n") + "\n\n## " + section + "\n\n" + text + "\n"
}

// Plan gives the change to each intent's block in the layer at dir; nothing
// is written.
func (a Addition) Plan(dir string, intents *dintent.Registry) ([]Change, error) {
	text := strings.TrimSpace(strings.ReplaceAll(a.Text, "\r\n", "\n"))
	switch {
	case len(a.Intents) == 0:
		return nil, errors.New("name at least one intent")
	case text == "":
		return nil, errors.New("no text to add")
	case strings.HasPrefix(text, "+++") || strings.HasPrefix(text, "---"):
		return nil, errors.New("the text starts with a front matter fence; use --title, --override and --paths instead")
	}
	for _, g := range a.Paths {
		if !validGlob(g) {
			return nil, fmt.Errorf("--paths %s is not a valid glob", g)
		}
	}
	var out []Change
	for _, k := range a.Intents {
		p := a.Path
		p.Intent = k
		rel, err := PathOf(p, intents)
		if err != nil {
			return nil, err
		}
		file := filepath.Join(dir, filepath.FromSlash(rel))
		before, err := os.ReadFile(file)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			before = nil
		case err != nil:
			return nil, err
		}
		var after string
		if before == nil {
			body := text + "\n"
			if a.Section != "" {
				body = underSection("", a.Section, text)
			}
			after = frontMatterOf(a) + body
		} else {
			if frontMatterOf(a) != "" {
				return nil, fmt.Errorf("%s exists, and --title, --override and --paths set front matter, which applies to every rule in a block; "+
					"write the files the rule is about into its text, or add it with a narrower condition", file)
			}
			old := strings.ReplaceAll(string(before), "\r\n", "\n")
			if a.Section != "" {
				fm, body := splitHead(old)
				after = fm + underSection(body, a.Section, text)
			} else {
				after = strings.TrimRight(old, "\n") + "\n\n" + text + "\n"
			}
		}
		out = append(out, Change{File: file, Rel: rel, Before: before, After: []byte(after)})
	}
	return out, nil
}

func splitHead(raw string) (head, body string) {
	if !strings.HasPrefix(raw, "+++\n") {
		return "", raw
	}
	if strings.HasPrefix(raw[4:], "+++\n") {
		return raw[:8], raw[8:]
	}
	if i := strings.Index(raw[4:], "\n+++\n"); i >= 0 {
		return raw[:4+i+5], raw[4+i+5:]
	}
	return "", raw
}

// Diff is c as a unified diff, from /dev/null for a new file.
func Diff(c Change) string {
	from := "a/" + c.Rel
	if c.From != "" {
		from = "a/" + c.From
	} else if c.Before == nil {
		from = "/dev/null"
	}
	d := unified(from, "b/"+c.Rel, string(c.Before), string(c.After))
	if d == "" && c.From != "" {
		return "--- " + from + "\n+++ b/" + c.Rel + "\nrenamed; the text does not change\n"
	}
	return d
}

func writeFile(file string, raw []byte) error {
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	tmp := file + ".loop-tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, file)
}

func removeEmpty(dir, file string) {
	os.Remove(file)
	for d := filepath.Dir(file); d != dir && strings.HasPrefix(d, dir); d = filepath.Dir(d) {
		if os.Remove(d) != nil {
			return
		}
	}
}

// Apply writes changes to the layer at dir, then loads it; when it no longer
// loads, every file gets its previous bytes back and the load error is
// returned. backup, when not nil, keeps a copy of each file it overwrites or
// renames.
func Apply(dir string, changes []Change, backup func(path string, raw []byte) (string, error), intents *dintent.Registry, broken []dintent.Name) error {
	for _, c := range changes {
		old := c.File
		if c.From != "" {
			old = filepath.Join(dir, filepath.FromSlash(c.From))
		}
		if backup != nil && c.Before != nil {
			if _, err := backup(old, c.Before); err != nil {
				return fmt.Errorf("backing up %s: %w", old, err)
			}
		}
	}
	var done []Change
	restore := func() error {
		var errs []error
		for i := len(done) - 1; i >= 0; i-- {
			c := done[i]
			switch {
			case c.From != "":
				errs = append(errs, writeFile(filepath.Join(dir, filepath.FromSlash(c.From)), c.Before))
				removeEmpty(dir, c.File)
			case c.Before == nil:
				removeEmpty(dir, c.File)
			default:
				errs = append(errs, writeFile(c.File, c.Before))
			}
		}
		return errors.Join(errs...)
	}
	for _, c := range changes {
		if err := writeFile(c.File, c.After); err != nil {
			return errors.Join(err, restore())
		}
		if c.From != "" {
			if err := os.Remove(filepath.Join(dir, filepath.FromSlash(c.From))); err != nil {
				done = append(done, Change{File: c.File, Before: nil})
				return errors.Join(err, restore())
			}
		}
		done = append(done, c)
	}
	if _, err := Load([]Layer{{Name: "user", Source: dir, Dir: dir}}, intents, broken); err != nil {
		return errors.Join(fmt.Errorf("the profile would no longer load, so nothing changed: %w", err), restore())
	}
	return nil
}
