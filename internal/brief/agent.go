package brief

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/tag"
	"github.com/wloophq/wloop/internal/domain/usage"
)

// maxCostUSD caps one call, well above a measured fast-tier brief ($0.008)
// and still enough for the balanced tier ($0.10).
const maxCostUSD = 0.25

type Call struct {
	Tier turns.Tier
	// Home is the agent's configuration directory, "" for its default.
	Home   string
	Prompt string
	// Schema is what the answer is asked for and validated against; the
	// zero Composed is the core schema.
	Schema Composed
	// Fields, when set, asks only these extra fields of a brief that has
	// its core: the missing-fields call.
	Fields []string
}

type Result struct {
	Brief    Brief
	Model    string
	CostUSD  *float64
	Duration time.Duration
	Tokens   *usage.Tokens
	// DroppedTags are the key:value spellings normalization refused.
	DroppedTags []string
	// Fields are a missing-fields call's values, by name.
	Fields map[string]any
	// ExtraErr is why the extra fields of an otherwise fitting brief were
	// refused; the brief holds none of them.
	ExtraErr error
}

// AskRunner makes a brief with one headless call of the agent.
type AskRunner struct{ Asker turns.Asker }

// Run returns, with a failure, what the call cost when the agent said.
func (a AskRunner) Run(ctx context.Context, c Call) (Result, error) {
	schema := c.Schema.JSON()
	if len(c.Fields) > 0 {
		schema = c.Schema.FieldsJSON(c.Fields)
	}
	ans, err := a.Asker.Ask(ctx, turns.Ask{Home: c.Home, Tier: c.Tier, Prompt: c.Prompt, Schema: schema, MaxCostUSD: maxCostUSD})
	res := Result{Model: ans.Model, CostUSD: ans.CostUSD, Duration: ans.Duration, Tokens: ans.Tokens}
	if err != nil {
		return res, err
	}
	if len(c.Fields) > 0 {
		res.Fields, err = c.Schema.DecodeFields(ans.JSON, c.Fields)
		return res, err
	}
	res.Brief, res.DroppedTags, err = c.Schema.decode(ans.JSON)
	if errors.Is(err, ErrExtra) {
		res.ExtraErr, err = err, nil
	}
	return res, err
}

// answer is the brief as the schema asks for it, with the tags an object.
type answer struct {
	Goal          string              `json:"goal"`
	Context       string              `json:"context"`
	Kind          string              `json:"kind"`
	Size          string              `json:"size"`
	Risk          string              `json:"risk"`
	RiskWhy       string              `json:"risk_why"`
	OpenQuestions []string            `json:"open_questions"`
	Tags          map[string][]string `json:"tags"`
}

// decode reads a brief that has every field of the schema and nothing else;
// a tag that does not normalize is dropped alone.
func decode(raw []byte) (Brief, []string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var a answer
	if err := dec.Decode(&a); err != nil {
		return Brief{}, nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if dec.More() {
		return Brief{}, nil, fmt.Errorf("%w: data after the brief", ErrInvalid)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return Brief{}, nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	for _, k := range []string{"goal", "context", "kind", "size", "risk", "risk_why", "open_questions", "tags"} {
		if _, ok := fields[k]; !ok {
			return Brief{}, nil, fmt.Errorf("%w: no %s", ErrInvalid, k)
		}
	}
	if a.Tags == nil {
		return Brief{}, nil, fmt.Errorf("%w: tags is not an object", ErrInvalid)
	}
	b := Brief{Goal: a.Goal, Context: a.Context, Kind: a.Kind, Size: a.Size, Risk: a.Risk, RiskWhy: a.RiskWhy, OpenQuestions: a.OpenQuestions}
	if err := b.validate(); err != nil {
		return Brief{}, nil, err
	}
	var dropped []string
	b.Tags, dropped = tag.FromObject(a.Tags)
	return b, dropped, nil
}
