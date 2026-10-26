package turn

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"text/template"

	"github.com/wloophq/wloop/internal/brand"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/profile"
	"github.com/wloophq/wloop/profiles"
)

// partials are the Go-owned templates every intent's instructions may call.
var partials = []string{"history.md.tmpl", "resume.md.tmpl", "record.md.tmpl", "brief.md.tmpl", "judgment.md.tmpl", "why.md.tmpl", "items.md.tmpl", "unit.md.tmpl", "dossier.md.tmpl"}

type Rendered struct {
	ID   string
	Text string
	// Sources are the guide files the text was rendered from; they never
	// reach the agent.
	Sources []string
	// Records says the instructions end the turn with a record.
	Records bool
}

var funcs = template.FuncMap{"name": func() string { return brand.Name }, "trim": strings.TrimSpace, "heading": heading, "line": line, "quote": quote, "has": has, "artifactDoc": artifactDoc, "contents": contents}

// artifactDocs are the record step's sentence per artifact kind.
var artifactDocs = map[string]string{
	"review-draft": "`artifact` (asked, when the user must read something to decide): the held\n  draft, `kind: review-draft`, `body` and one `items` entry per inline comment.",
	"triage":       "`artifact` (asked, when the user must read something to decide): the\n  verdict per item, `kind: triage`.",
}

func artifactDoc(kind string) (string, error) {
	doc, ok := artifactDocs[kind]
	if !ok {
		return "", fmt.Errorf("no record sentence for the artifact kind %s", kind)
	}
	return doc, nil
}

func has(list []string, s string) bool { return slices.Contains(list, s) }

// quote sets text an agent or the user wrote apart from the loop's: cut to limit
// characters, every line indented by indent spaces, so that no fence or
// heading in it opens anything (four at the top level, six in a list item).
func quote(s string, limit, indent int) string {
	s = lineBreaks.Replace(s)
	if r := []rune(s); len(r) > limit {
		s = string(r[:limit])
	}
	pad := strings.Repeat(" ", indent)
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = pad + l
	}
	return strings.Join(lines, "\n")
}

// lineBreaks are the separators a reader may take for a line end, as "\n".
var lineBreaks = strings.NewReplacer("\r\n", "\n", "\r", "\n", "\u2028", "\n", "\u2029", "\n", "\u0085", "\n", "\v", "\n", "\f", "\n")

func heading(level int) string { return strings.Repeat("#", level) }

// line keeps a model's text on one line, so it opens no heading or list.
func line(s string) string { return strings.Join(strings.Fields(s), " ") }

func load(name, devDir string) (string, error) {
	if devDir != "" {
		raw, err := os.ReadFile(filepath.Join(devDir, name))
		if err == nil {
			return string(raw), nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
	}
	raw, err := fs.ReadFile(profiles.Default, path.Join("default", profile.PartialsDir, name))
	return string(raw), err
}

// Base is the set every intent's instructions are parsed on: the Go-owned
// partials and the template functions, with missingkey=error.
func Base() (*template.Template, error) { return base("") }

func base(devDir string) (*template.Template, error) {
	t := template.New("base").Funcs(funcs).Option("missingkey=error")
	for _, name := range partials {
		src, err := load(name, devDir)
		if err != nil {
			return nil, err
		}
		if _, err := t.New(name).Parse(src); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
	}
	return t, nil
}

// BundledFS is the bundled profile, with intents/ at its root.
func BundledFS() fs.FS {
	sub, err := fs.Sub(profiles.Default, "default")
	if err != nil {
		panic(err)
	}
	return sub
}

// NewCache is the registry cache over the bundled intents and those of layers.
func NewCache(layers func() []profile.Layer, opts intentload.CacheOptions) (*intentload.Cache, error) {
	b, err := Base()
	if err != nil {
		return nil, err
	}
	return intentload.NewCache(layers, BundledFS(), b, CheckTemplate, opts), nil
}

// Bundled is the bundled intents alone, loaded once; callers never change it.
var Bundled = sync.OnceValues(func() (*intentload.Loaded, error) {
	b, err := Base()
	if err != nil {
		return nil, err
	}
	l, problems := intentload.Load(nil, BundledFS(), b, CheckTemplate)
	if len(problems) > 0 {
		return nil, problems
	}
	return l, nil
})

// Render writes the instructions of a completed turn. devDir, when set,
// holds partials, and intents/<name>/instructions.md.tmpl of bundled
// intents, that replace the loaded ones.
func Render(c Context, l *intentload.Loaded, devDir string) (Rendered, error) {
	if c.Def.Name == "" || string(c.Def.Name) != string(c.Intent) {
		return Rendered{}, fmt.Errorf("%s: the turn is not completed", c.Intent)
	}
	for _, in := range c.Def.Inputs {
		if _, ok := c.Inputs[in.Name]; !ok {
			return Rendered{}, fmt.Errorf("%s: inputs.%s is not set", c.Intent, in.Name)
		}
	}
	t, err := instructionsOf(c.Def, l, devDir)
	if err != nil {
		return Rendered{}, err
	}
	var b bytes.Buffer
	if err := t.Execute(&b, c); err != nil {
		return Rendered{}, err
	}
	body := collapseBlankLines(b.String())
	where := c.where()
	h := sha256.Sum256([]byte(string(c.Intent) + "\x00" + where + "\x00" + body))
	id := hex.EncodeToString(h[:])[:12]
	return Rendered{
		ID:      id,
		Text:    fmt.Sprintf("Instructions %s for %s in %s.\n\n%s", id, c.Intent, where, body),
		Sources: sourcesOf(c),
		Records: c.Records,
	}, nil
}

// where is what the instructions' first line says the turn is in: the
// repository of a pull request, else the unit's reference.
func (c Context) where() string {
	if p := c.PR(); p != nil {
		return p.Owner + "/" + p.Repo
	}
	return c.Wording.Ref
}

func instructionsOf(d dintent.Def, l *intentload.Loaded, devDir string) (*template.Template, error) {
	if devDir == "" {
		if l == nil || l.Templates[d.Name] == nil {
			return nil, fmt.Errorf("%s: no instructions are loaded", d.Name)
		}
		return l.Templates[d.Name], nil
	}
	set, err := base(devDir)
	if err != nil {
		return nil, err
	}
	rel := path.Join("intents", string(d.Name), "instructions.md.tmpl")
	var raw []byte
	switch {
	case d.Bundled && !slices.Contains(d.Source.Overrides, "instructions.md.tmpl"):
		if raw, err = os.ReadFile(filepath.Join(devDir, filepath.FromSlash(rel))); errors.Is(err, fs.ErrNotExist) {
			raw, err = fs.ReadFile(BundledFS(), rel)
		}
	default:
		raw, err = os.ReadFile(filepath.Join(d.Source.Dir, "instructions.md.tmpl"))
	}
	if err != nil {
		return nil, err
	}
	t, err := set.New(rel).Parse(string(raw))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	return t, nil
}

// sourcesOf lists the main guide's parts, then each also_profiles guide's
// under its prefix, then the blocks left out of the text and why.
func sourcesOf(c Context) []string {
	out := sources(c.Guide)
	for _, a := range alsoGuides(c) {
		for _, s := range sources(a.guide) {
			out = append(out, a.prefix+s)
		}
	}
	return out
}

// Trimmed are the blocks the turn's guides left out to stay within the
// judgment limit: the main guide's, then each also_profiles guide's under its prefix.
func Trimmed(c Context) []string {
	out := slices.Clone(c.Guide.Dropped)
	for _, a := range alsoGuides(c) {
		for _, d := range a.guide.Dropped {
			out = append(out, a.prefix+d)
		}
	}
	return out
}

type alsoGuide struct {
	prefix string
	guide  profile.Guide
}

// alsoGuides are the composed also_profiles guides other than the main one.
func alsoGuides(c Context) []alsoGuide {
	var out []alsoGuide
	for _, a := range c.Def.AlsoProfiles {
		g, ok := c.Guides[a.Profile]
		if !ok || a.Profile == c.Def.Profile {
			continue
		}
		out = append(out, alsoGuide{a.SourcePrefix, g})
	}
	return out
}

func sources(g profile.Guide) []string {
	out := g.Sources()
	for _, s := range g.Skipped {
		out = append(out, "skipped: needs the brief: "+s)
	}
	for _, s := range g.Dropped {
		out = append(out, "dropped: over the judgment limit: "+s)
	}
	return out
}

func collapseBlankLines(s string) string {
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(s) + "\n"
}

// Prompt is the one line typed into the agent's session: the turn in words,
// and the command that prints its instructions.
func Prompt(c Context, id string) string {
	return c.Headline + " Run `" + c.Command + " instructions " + string(c.Intent) + " --turn " + id + " " + c.Payload.Arg() + "` and follow what it prints."
}
