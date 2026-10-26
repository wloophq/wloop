// Package skills ships the agent skills the loop installs: a thin skill per
// user-facing intent that fetches a turn's instructions from the CLI, the
// operator skill and the one that edits the review profile.
package skills

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/wloophq/wloop/internal/brand"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
)

//go:embed turn/SKILL.md.tmpl
var turnTemplate string

//go:embed operator/SKILL.md
var operator []byte

//go:embed profile/SKILL.md
var profile []byte

type Skill struct {
	Name string
	Body []byte
}

var (
	Operator = Skill{Name: brand.Name, Body: operator}
	Profile  = Skill{Name: brand.SkillName("profile"), Body: profile}
)

// Bundled lists every skill install writes: the turn skill of each intent
// of defs whose manifest asks for one, then the operator and profile skills.
func Bundled(defs []dintent.Def) []Skill {
	var out []Skill
	for _, d := range defs {
		if d.Skill {
			out = append(out, Turn(d))
		}
	}
	return append(out, Operator, Profile)
}

// Turn is the skill that runs a turn of d by hand, filled from its manifest:
// its name, its summary, and the flags a user passes.
func Turn(d dintent.Def) Skill {
	intent := string(d.Name)
	description := "Use when the prompt is `/{{name}}-" + intent + " <url>` or `${{name}}-" + intent + " <url>`, or to " +
		lowerFirst(d.Summary) + ". Runs the " + intent + " turn {{name}} defines for that repository."
	body := strings.NewReplacer("{{intent}}", intent, "{{description}}", quote(description), "{{flags}}", flagsLine(d)).Replace(turnTemplate)
	return Skill{Name: brand.SkillName(intent), Body: []byte(body)}
}

func flagsLine(d dintent.Def) string {
	var flags []string
	for _, in := range d.Inputs {
		if in.Type == "flag" && in.Flag != "" && !in.Hidden {
			flags = append(flags, "  - `"+in.Flag+"` "+in.Help+"\n")
		}
	}
	if len(flags) == 0 {
		return ""
	}
	return "- Add a flag before `--` only when the prompt asks for what it does:\n" + strings.Join(flags, "")
}

// quote is s as a YAML double-quoted scalar, which a JSON string is.
func quote(s string) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}

func lowerFirst(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	return string(unicode.ToLower(r)) + s[n:]
}

const (
	frontMatter = "---\n"
	markerKey   = "  managed-by: "
	versionKey  = "  version: "
	digestKey   = "  sha256: "
)

// Render fills the {{command}}, {{name}} and {{statedir}} tokens of the body
// and marks it in the front matter's metadata: the loop that manages it, the
// version that wrote it, and the sha256 of the file without that digest
// line, which tells an edited file from one install wrote.
func (s Skill) Render(command, version string) []byte {
	body := strings.NewReplacer("{{command}}", command, "{{name}}", brand.Name, "{{statedir}}", brand.StateDir()).Replace(string(s.Body))
	head, rest, ok := splitFrontMatter(body)
	if !ok {
		panic("skill " + s.Name + " has no front matter")
	}
	meta := "metadata:\n" + markerKey + brand.Name + "\n" + versionKey + quote(version) + "\n"
	sum := sha256.Sum256([]byte(head + meta + frontMatter + rest))
	return []byte(head + meta + digestKey + hex.EncodeToString(sum[:]) + "\n" + frontMatter + rest)
}

// Mark is what a skill file's marker says.
type Mark struct {
	Version string
	// Unedited says the file is as the version that wrote it rendered it.
	Unedited bool
}

// ReadMark reads the marker Render writes; ok is false for a file the loop
// did not write.
func ReadMark(raw []byte) (m Mark, ok bool) {
	head, rest, found := splitFrontMatter(string(raw))
	if !found {
		return Mark{}, false
	}
	lines := strings.SplitAfter(head, "\n")
	digest, at := "", -1
	for i, l := range lines {
		line := strings.TrimSuffix(l, "\n")
		switch {
		case line == markerKey+brand.Name:
			ok = true
		case strings.HasPrefix(line, versionKey):
			_ = json.Unmarshal([]byte(strings.TrimPrefix(line, versionKey)), &m.Version)
		case strings.HasPrefix(line, digestKey):
			digest, at = strings.TrimPrefix(line, digestKey), i
		}
	}
	if !ok || at < 0 {
		return m, ok
	}
	unsigned := strings.Join(append(lines[:at:at], lines[at+1:]...), "") + frontMatter + rest
	sum := sha256.Sum256([]byte(unsigned))
	m.Unedited = digest == hex.EncodeToString(sum[:])
	return m, true
}

// splitFrontMatter splits body into its front matter, from the opening
// delimiter to the line before the closing one, and what follows the
// closing delimiter.
func splitFrontMatter(body string) (head, rest string, ok bool) {
	if !strings.HasPrefix(body, frontMatter) {
		return "", "", false
	}
	end := strings.Index(body[len(frontMatter):], "\n"+frontMatter)
	if end < 0 {
		return "", "", false
	}
	cut := len(frontMatter) + end + 1
	return body[:cut], body[cut+len(frontMatter):], true
}
