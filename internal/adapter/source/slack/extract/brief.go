package extract

import (
	_ "embed"
	"encoding/json"
	"fmt"

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
}

// storedBrief is a brief as brief_runs keeps it, in the fields of a pull
// request's brief: a thread has no size, and its tags are key:value.
type storedBrief struct {
	Goal          string   `json:"goal"`
	Context       string   `json:"context"`
	Kind          string   `json:"kind"`
	Risk          string   `json:"risk"`
	RiskWhy       string   `json:"risk_why"`
	OpenQuestions []string `json:"open_questions"`
	Tags          []string `json:"tags"`
}

// briefJSON is the stored JSON of w: empty when the answer keeps the
// brief the unit has or says nothing.
func briefJSON(w wireBrief) (string, error) {
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
	b, err := json.Marshal(storedBrief{Goal: w.Goal, Context: w.Context, Kind: w.Kind, Risk: w.Risk, RiskWhy: w.RiskWhy,
		OpenQuestions: w.OpenQuestions, Tags: tags})
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

// composeSchema puts the brief's schema under the answer's brief and under
// each segment's.
func composeSchema(answer, brief string) (string, *jsonschema.Schema) {
	var doc, b map[string]any
	if err := json.Unmarshal([]byte(answer), &doc); err != nil {
		panic("extract: schema.json: " + err.Error())
	}
	if err := json.Unmarshal([]byte(brief), &b); err != nil {
		panic("extract: brief.schema.json: " + err.Error())
	}
	props := doc["properties"].(map[string]any)
	props["brief"] = b
	seg := props["segments"].(map[string]any)["items"].(map[string]any)
	seg["properties"].(map[string]any)["brief"] = b
	seg["required"] = append(seg["required"].([]any), "brief")
	out, err := json.MarshalIndent(doc, "", " ")
	if err != nil {
		panic(fmt.Sprintf("extract: the composed schema: %v", err))
	}
	compiled, err := jsonschema.Compile(out, jsonschema.Options{})
	if err != nil {
		panic(fmt.Sprintf("extract: the composed schema: %v", err))
	}
	return string(out), compiled
}
