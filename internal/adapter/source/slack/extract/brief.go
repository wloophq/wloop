package extract

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/wloophq/wloop/internal/domain/jsonschema"
	"github.com/wloophq/wloop/internal/domain/tag"
)

//go:embed brief.schema.json
var briefSchemaJSON string

// wireBrief is a conversation's or a segment's brief as the schema asks for
// it; Unchanged keeps the brief the unit has.
type wireBrief struct {
	Unchanged     bool                `json:"unchanged"`
	Goal          string              `json:"goal"`
	Context       string              `json:"context"`
	Kind          string              `json:"kind"`
	Risk          string              `json:"risk"`
	RiskWhy       string              `json:"risk_why"`
	OpenQuestions []string            `json:"open_questions"`
	Tags          map[string][]string `json:"tags"`
	Extra         map[string]any      `json:"extra"`
}

// Extra is the brief intent's extra fields as an extraction asks them: the
// flat object schema and each field's version, which the stored brief
// keeps beside the values.
type Extra struct {
	Schema   json.RawMessage
	Versions map[string]string
}

// storedBrief is a brief as brief_runs keeps it, in the fields of a pull
// request's brief: a thread has no size, and its tags are key:value.
type storedBrief struct {
	Goal          string   `json:"goal"`
	Context       string   `json:"context"`
	Kind          string   `json:"kind"`
	Risk          string   `json:"risk"`
	RiskWhy       string   `json:"risk_why"`
	OpenQuestions []string          `json:"open_questions"`
	Tags          []string          `json:"tags"`
	Extra         map[string]any    `json:"extra,omitempty"`
	ExtraVersions map[string]string `json:"extra_versions,omitempty"`
}

// briefJSON is the stored JSON of w: empty when the answer keeps the
// brief the unit has or says nothing.
func briefJSON(w wireBrief, x *Extra) (string, error) {
	if w.Unchanged || w.Goal == "" {
		return "", nil
	}
	if w.OpenQuestions == nil {
		w.OpenQuestions = []string{}
	}
	tags, _ := tag.FromObject(w.Tags)
	if tags == nil {
		tags = []string{}
	}
	sb := storedBrief{Goal: w.Goal, Context: w.Context, Kind: w.Kind, Risk: w.Risk, RiskWhy: w.RiskWhy,
		OpenQuestions: w.OpenQuestions, Tags: tags}
	if x != nil && len(w.Extra) > 0 {
		sb.Extra, sb.ExtraVersions = w.Extra, x.Versions
	}
	b, err := json.Marshal(sb)
	return string(b), err
}

// GoalOf is the goal of a stored brief, empty for none.
func GoalOf(stored string) string {
	if stored == "" {
		return ""
	}
	var b storedBrief
	if err := json.Unmarshal([]byte(stored), &b); err != nil {
		return ""
	}
	return b.Goal
}

var composedFor = struct {
	sync.Mutex
	by map[string]composedSchema
}{by: map[string]composedSchema{}}

type composedSchema struct {
	json   string
	schema *jsonschema.Schema
}

// schemaFor is the extraction's schema with x's fields under every brief's
// extra, required; nil x is the schema without them.
func schemaFor(x *Extra) (string, *jsonschema.Schema, error) {
	if x == nil || len(x.Schema) == 0 {
		return schemaJSON, schema, nil
	}
	composedFor.Lock()
	defer composedFor.Unlock()
	if c, ok := composedFor.by[string(x.Schema)]; ok {
		return c.json, c.schema, nil
	}
	var b map[string]any
	if err := json.Unmarshal([]byte(briefSchemaJSON), &b); err != nil {
		return "", nil, err
	}
	var ex any
	if err := json.Unmarshal(x.Schema, &ex); err != nil {
		return "", nil, fmt.Errorf("extract: the brief's extra schema: %w", err)
	}
	b["properties"].(map[string]any)["extra"] = ex
	b["required"] = append(b["required"].([]any), "extra")
	withExtra, err := json.Marshal(b)
	if err != nil {
		return "", nil, err
	}
	out, s, err := compose(answerSchemaJSON, string(withExtra))
	if err != nil {
		return "", nil, err
	}
	composedFor.by[string(x.Schema)] = composedSchema{out, s}
	return out, s, nil
}

// composeSchema puts the brief's schema under the answer's brief and under
// each segment's.
func composeSchema(answer, brief string) (string, *jsonschema.Schema) {
	out, s, err := compose(answer, brief)
	if err != nil {
		panic(err)
	}
	return out, s
}

func compose(answer, brief string) (string, *jsonschema.Schema, error) {
	var doc, b map[string]any
	if err := json.Unmarshal([]byte(answer), &doc); err != nil {
		return "", nil, fmt.Errorf("extract: schema.json: %w", err)
	}
	if err := json.Unmarshal([]byte(brief), &b); err != nil {
		return "", nil, fmt.Errorf("extract: the brief schema: %w", err)
	}
	props := doc["properties"].(map[string]any)
	props["brief"] = b
	seg := props["segments"].(map[string]any)["items"].(map[string]any)
	seg["properties"].(map[string]any)["brief"] = b
	seg["required"] = append(seg["required"].([]any), "brief")
	out, err := json.MarshalIndent(doc, "", " ")
	if err != nil {
		return "", nil, fmt.Errorf("extract: the composed schema: %w", err)
	}
	compiled, err := jsonschema.Compile(out, jsonschema.Options{})
	if err != nil {
		return "", nil, fmt.Errorf("extract: the composed schema: %w", err)
	}
	return string(out), compiled, nil
}
