// Package brief previews each pull request with one headless call of the
// agent: goal, context, kind, size, risk and open questions.
package brief

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/domain/facet"
	"github.com/wloophq/wloop/profiles"
)

// PromptFile and TaxonomyFile are the bundled profile's brief prompt and
// taxonomy, relative to the profile's root.
const (
	PromptFile       = "prompts/brief.md"
	FieldsPromptFile = "prompts/brief-fields.md"
	TaxonomyFile     = "prompts/brief-taxonomy.md"
)

var promptTemplate = bundled(PromptFile)

//go:embed schema.json
var Schema string

// DefaultTaxonomy is the bundled profile's taxonomy.
var DefaultTaxonomy = bundled(TaxonomyFile)

func bundled(rel string) string {
	b, err := fs.ReadFile(profiles.Default, path.Join("default", rel))
	if err != nil {
		panic("brief: the bundled profile has no " + rel + ": " + err.Error())
	}
	return string(b)
}

var ErrInvalid = errors.New("the brief does not fit the schema")

type Brief struct {
	Goal          string   `json:"goal"`
	Context       string   `json:"context"`
	Kind          string   `json:"kind"`
	Size          string   `json:"size"`
	Risk          string   `json:"risk"`
	RiskWhy       string   `json:"risk_why"`
	OpenQuestions []string `json:"open_questions"`
	// Tags are the normalized key:value tags.
	Tags []string `json:"tags"`
	// Extra are the brief intent's extra fields, by name, and the version
	// of each field the brief holds.
	Extra         map[string]any    `json:"extra,omitempty"`
	ExtraVersions map[string]string `json:"extra_versions,omitempty"`
}

func (b Brief) validate() error {
	switch {
	case strings.TrimSpace(b.Goal) == "":
		return fmt.Errorf("%w: no goal", ErrInvalid)
	case !slices.Contains(facet.Kinds, b.Kind):
		return fmt.Errorf("%w: kind %q", ErrInvalid, b.Kind)
	case !slices.Contains(facet.Sizes, b.Size):
		return fmt.Errorf("%w: size %q", ErrInvalid, b.Size)
	case !slices.Contains(facet.Risks, b.Risk):
		return fmt.Errorf("%w: risk %q", ErrInvalid, b.Risk)
	case len(b.OpenQuestions) > 3:
		return fmt.Errorf("%w: %d open questions", ErrInvalid, len(b.OpenQuestions))
	}
	return nil
}

// Version keys the cache: a new prompt, schema or taxonomy makes every
// brief stale.
func Version(taxonomy string) string {
	h := sha256.Sum256([]byte(promptTemplate + "\x00" + Schema + "\x00" + taxonomy))
	return "b1-" + hex.EncodeToString(h[:6])
}

type Input struct {
	Title, Body, Issue string
	Files              []string
	Diff               string
	// DiffNote says why the diff is cut or missing, "" when it is whole.
	DiffNote string
	// Paths are the changed files, at most filesMax.
	Paths []string
	// DiffBytes is the whole diff's size, 0 when gh did not return it.
	DiffBytes int
	DiffCut   bool
}

// Prompt fills the template in one pass, so a "{{...}}" inside the pull
// request's own text stays as it is. The data markers carry a hash of the
// data, so the pull request cannot close its own block. The vocabulary is
// not part of Version, so a new tag value makes no brief stale.
func Prompt(taxonomy, vocabulary string, in Input) string {
	issue := in.Issue
	if issue == "" {
		issue = "(none)"
	}
	files := strings.Join(in.Files, "\n")
	h := sha256.Sum256([]byte(strings.Join([]string{in.Title, in.Body, issue, files, in.DiffNote, in.Diff}, "\x00")))
	return strings.NewReplacer(
		"{{marker}}", hex.EncodeToString(h[:8]),
		"{{taxonomy}}", strings.TrimSpace(taxonomy),
		"{{vocabulary}}", strings.TrimSpace(vocabulary),
		"{{title}}", in.Title,
		"{{body}}", in.Body,
		"{{issue}}", issue,
		"{{files}}", files,
		"{{diff_note}}", in.DiffNote,
		"{{diff}}", in.Diff,
	).Replace(promptTemplate)
}
