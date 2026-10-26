package brief

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/jsonschema"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
)

const extraSchema = `{"type":"object","additionalProperties":false,"required":["solution","approach"],"properties":{
	"solution":{"type":"string","maxLength":200,"description":"how the change solves its goal"},
	"approach":{"type":"string","enum":["patch","rewrite","config"]}}}`

func composed(t *testing.T) Composed {
	t.Helper()
	c, err := Compose(Schema, []byte(extraSchema))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestTheComposedSchemaAddsTheExtras(t *testing.T) {
	t.Parallel()
	c := composed(t)
	var doc struct {
		Required   []string                   `json:"required"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal([]byte(c.JSON()), &doc); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(doc.Required, "extra") || !slices.Contains(doc.Required, "goal") || len(doc.Properties) != 9 {
		t.Fatalf("composed %s", c.JSON())
	}
	ex, err := jsonschema.Compile(doc.Properties["extra"], jsonschema.Options{Flat: true})
	if err != nil || len(ex.Properties()) != 2 {
		t.Fatalf("the extra property %s: %v", doc.Properties["extra"], err)
	}
	v := c.FieldVersions()
	if len(v) != 2 || v["solution"] == "" || v["solution"] == v["approach"] {
		t.Fatalf("field versions %v", v)
	}
	changed, err := Compose(Schema, []byte(strings.Replace(extraSchema, `"config"]`, `"config","revert"]`, 1)))
	if err != nil {
		t.Fatal(err)
	}
	if w := changed.FieldVersions(); w["solution"] != v["solution"] || w["approach"] == v["approach"] {
		t.Errorf("a changed enum moved %v to %v", v, w)
	}
	if Version(DefaultTaxonomy) != "b1-8ab3e5d390e0" {
		t.Error("the extras moved the core version")
	}
	for name, bad := range map[string]string{
		"a nested object": `{"type":"object","properties":{"plan":{"type":"object","properties":{}}}}`,
		"oneOf":           `{"type":"object","properties":{"a":{"type":"string","oneOf":[]}}}`,
		"not an object":   `{"type":"string"}`,
	} {
		if _, err := Compose(Schema, []byte(bad)); !errors.Is(err, jsonschema.ErrSchema) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestAModelAnswerOutsideTheComposedSchemaIsRefused(t *testing.T) {
	t.Parallel()
	c := composed(t)
	withExtra := func(extra string) string { return strings.TrimSuffix(goodBrief, "}") + `,"extra":` + extra + `}` }
	run := func(answer string, call Call) (Result, error) {
		call.Schema = c
		f := &fakeAsker{ans: turns.Answer{JSON: []byte(answer)}}
		r, err := AskRunner{Asker: f}.Run(context.Background(), call)
		if f.got.Schema != c.JSON() && len(call.Fields) == 0 {
			t.Errorf("asked with schema %s", f.got.Schema)
		}
		return r, err
	}
	r, err := run(withExtra(`{"solution":"batch the export","approach":"patch"}`), Call{})
	if err != nil || r.Brief.Extra["approach"] != "patch" || r.Brief.ExtraVersions["approach"] != c.FieldVersions()["approach"] {
		t.Fatalf("a fitting answer: %+v %v", r.Brief, err)
	}
	for name, extra := range map[string]string{
		"an enum outside":  `{"solution":"s","approach":"magic"}`,
		"a field missing":  `{"solution":"s"}`,
		"an unknown field": `{"solution":"s","approach":"patch","owner":"x"}`,
		"a value too long": `{"solution":"` + strings.Repeat("x", 201) + `","approach":"patch"}`,
		"not an object":    `"patch"`,
	} {
		r, err := run(withExtra(extra), Call{})
		if err != nil || r.ExtraErr == nil || r.Brief.Goal == "" || r.Brief.Extra != nil {
			t.Errorf("%s: the core is kept and the extras refused: %+v, %v, %v", name, r.Brief, r.ExtraErr, err)
		}
	}
	for name, answer := range map[string]string{
		"a kind outside":      strings.Replace(withExtra(`{"solution":"s","approach":"patch"}`), `"kind":"feature"`, `"kind":"incident"`, 1),
		"an unknown field":    strings.Replace(goodBrief, `"goal"`, `"owner":"x","goal"`, 1),
		"four questions":      strings.Replace(goodBrief, `["What is the batch size?"]`, `["a","b","c","d"]`, 1),
		"a goal not a string": strings.Replace(goodBrief, `"goal":"Export invoices in batches"`, `"goal":3`, 1),
	} {
		if _, err := run(answer, Call{}); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := (AskRunner{Asker: &fakeAsker{ans: turns.Answer{JSON: []byte(withExtra(`{}`))}}}).Run(context.Background(), Call{}); !errors.Is(err, ErrInvalid) {
		t.Errorf("an extra the core schema does not ask for: %v", err)
	}
	if f, err := run(`{"extra":{"approach":"rewrite"}}`, Call{Fields: []string{"approach"}}); err != nil || f.Fields["approach"] != "rewrite" {
		t.Errorf("a fitting fields answer: %+v %v", f, err)
	}
	for name, answer := range map[string]string{
		"a field not asked": `{"extra":{"approach":"rewrite","solution":"s"}}`,
		"outside the enum":  `{"extra":{"approach":"magic"}}`,
		"a core field":      `{"extra":{"approach":"rewrite"},"goal":"g"}`,
	} {
		if _, err := run(answer, Call{Fields: []string{"approach"}}); !errors.Is(err, ErrExtra) {
			t.Errorf("fields, %s: %v", name, err)
		}
	}
}

func TestAMissingFieldIsFilledWithoutACoreRebrief(t *testing.T) {
	r := newRig(t, 1)
	ctx := context.Background()
	ref := github.Ref{Owner: "acme", Repo: "api", Number: 1}
	if _, err := r.q.Run(ctx); err != nil || r.run.calls != 1 {
		t.Fatalf("the core brief: %d calls, %v", r.run.calls, err)
	}
	before, _, _ := r.st.BriefRecord(ctx, ref)
	c := composed(t)
	r.q.d.Intent = func(unit.ProviderID) Intent { return Intent{Schema: c} }
	rd, err := r.q.peekPR(ctx, ref, true)
	if err != nil || rd.State != Ready {
		t.Fatalf("%+v %v", rd, err)
	}
	if m := c.Missing(*rd.Brief); !slices.Equal(m, []string{"approach", "solution"}) {
		t.Fatalf("missing %v", m)
	}
	r.run.fields = map[string]any{"approach": "patch", "solution": "batch the export"}
	if ok, err := r.q.fillPR(ctx, ref, []string{"approach"}); err != nil || !ok {
		t.Fatalf("fill: %v %v", ok, err)
	}
	if r.run.calls != 2 || !slices.Equal(r.run.asked[1], []string{"approach"}) || !strings.Contains(r.run.prompts[1], `"goal":"g"`) {
		t.Fatalf("calls %d asking %v", r.run.calls, r.run.asked)
	}
	after, _, _ := r.st.BriefRecord(ctx, ref)
	b := stored(after)
	if b == nil || b.Goal != "g" || b.Extra["approach"] != "patch" || b.Extra["solution"] != nil {
		t.Fatalf("the filled brief %+v", b)
	}
	if after.Version != before.Version || after.Run == before.Run || after.HeadOid != before.HeadOid || after.ChangedFiles != before.ChangedFiles {
		t.Fatalf("record %+v, before %+v", after, before)
	}
	if m := c.Missing(*b); !slices.Equal(m, []string{"solution"}) {
		t.Fatalf("still missing %v", m)
	}
	runs, err := r.st.BriefRuns(ctx, t0.Add(-time.Hour))
	if err != nil || len(runs) != 2 || runs[1].Trigger != TriggerFields || runs[1].ReusedFrom != before.Run {
		t.Fatalf("runs %+v %v", runs, err)
	}
	if _, err := r.q.Run(ctx); err != nil || r.run.calls != 2 {
		t.Fatalf("the fields run made the core due: %d calls, %v", r.run.calls, err)
	}
	if ok, _ := r.q.fillPR(ctx, ref, nil); !ok || r.run.calls != 3 || !slices.Equal(r.run.asked[2], []string{"solution"}) {
		t.Fatalf("filling the rest: %v, asked %v", ok, r.run.asked)
	}
	if ok, _ := r.q.fillPR(ctx, ref, nil); ok || r.run.calls != 3 {
		t.Fatal("a brief with every field filled was asked again")
	}
}
