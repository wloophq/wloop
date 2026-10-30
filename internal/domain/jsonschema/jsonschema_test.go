package jsonschema

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompileRefusesAnUnsupportedKeywordNamingTheField(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{`{"type":"object","properties":{"approach":{"type":"string","oneOf":[]}}}`, `$.approach: keyword "oneOf"`},
		{`{"type":"object","properties":{"a":{"type":"array","items":{"type":"string","format":"uri"}}}}`, `$.a[]: keyword "format"`},
		{`{"type":"object","$schema":"x"}`, `$: keyword "$schema"`},
		{`{"type":["string","null"]}`, `$: type ["string","null"]`},
		{`{"type":"array"}`, `$: an array without items`},
		{`{"type":"object","required":["a"]}`, `required "a" has no property`},
		{`{"type":"string","pattern":"(?<=x)"}`, `$: pattern`},
		{`{"type":"object","additionalProperties":{"type":"string"}}`, `additionalProperties: only true or false`},
	} {
		_, err := Compile([]byte(c.src), Options{})
		if !errors.Is(err, ErrSchema) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.src, err, c.want)
		}
	}
	nested := `{"type":"object","properties":{"plan":{"type":"object","properties":{}}}}`
	if _, err := Compile([]byte(nested), Options{}); err != nil {
		t.Errorf("a nested object outside a flat schema: %v", err)
	}
	for _, src := range []string{nested, `{"type":"object","properties":{"l":{"type":"array","items":{"type":"object"}}}}`} {
		if _, err := Compile([]byte(src), Options{Flat: true}); !errors.Is(err, ErrSchema) || !strings.Contains(err.Error(), "a nested object") {
			t.Errorf("flat %s: %v", src, err)
		}
	}
	if _, err := Compile([]byte(`{"type":"string"}`), Options{Flat: true}); err == nil {
		t.Error("a flat schema that is not an object compiled")
	}
}

func TestValidateChecksTheSubset(t *testing.T) {
	s, err := Compile([]byte(`{"type":"object","additionalProperties":false,"required":["goal","n"],"description":"d","properties":{
		"goal":{"type":"string","minLength":1,"maxLength":5},
		"code":{"type":"string","pattern":"^[a-z]+$"},
		"n":{"type":"integer"},
		"x":{"type":"number"},
		"ok":{"type":"boolean"},
		"risk":{"type":"string","enum":["low","high"]},
		"tags":{"type":"array","minItems":1,"maxItems":2,"items":{"type":"string","enum":["a","b"]}},
		"open":{"type":"object","properties":{"k":{"type":"string"}}}
	}}`), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ValidateJSON([]byte(`{"goal":"ship","n":3,"x":1.5,"ok":true,"risk":"low","tags":["a"],"code":"ab","open":{"k":"v","other":1}}`)); err != nil {
		t.Fatalf("a fitting answer: %v", err)
	}
	for answer, want := range map[string]string{
		`{"n":1}`:                                 "$: goal is missing",
		`{"goal":"","n":1}`:                       "$.goal: 0 characters, under 1",
		`{"goal":"shipping","n":1}`:               "$.goal: 8 characters, over 5",
		`{"goal":"a","n":1.5}`:                    "$.n: not an integer",
		`{"goal":"a","n":"1"}`:                    "$.n: not a number",
		`{"goal":"a","n":1,"x":"y"}`:              "$.x: not a number",
		`{"goal":"a","n":1,"ok":1}`:               "$.ok: not a boolean",
		`{"goal":"a","n":1,"risk":"mid"}`:         "$.risk: mid is not one of",
		`{"goal":"a","n":1,"tags":[]}`:            "$.tags: 0 entries, under 1",
		`{"goal":"a","n":1,"tags":["a","b","a"]}`: "$.tags: 3 entries, over 2",
		`{"goal":"a","n":1,"tags":["c"]}`:         "$.tags[0]: c is not one of",
		`{"goal":"a","n":1,"code":"A1"}`:          `$.code: "A1" does not match`,
		`{"goal":"a","n":1,"extra":1}`:            "$: unknown field extra",
		`{"goal":"a","n":1,"open":{"k":2}}`:       "$.open.k: not a string",
		`[]`:                                      "$: not an object",
		`{"goal":"a","n":1} {}`:                   "text after the value",
	} {
		err := s.ValidateJSON([]byte(answer))
		if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", answer, err, want)
		}
	}
	props := s.Properties()
	if len(props) != 8 || props[0].Name != "code" || props[3].Name != "ok" {
		t.Fatalf("properties %+v", props)
	}
	for _, p := range props {
		switch p.Name {
		case "tags":
			if p.Type != "array" || strings.Join(p.Enum, ",") != "a,b" {
				t.Errorf("tags %+v", p)
			}
		case "risk":
			if p.Type != "string" || strings.Join(p.Enum, ",") != "low,high" || string(p.Source) != `{"type":"string","enum":["low","high"]}` {
				t.Errorf("risk %+v %s", p, p.Source)
			}
		}
	}
}

// TestEveryBundledSchemaCompiles runs every schema an agent answers against
// through the subset: the brief's, the extraction's, the headless answer's,
// the eval's and every schema.json of the bundled profile.
func TestEveryBundledSchemaCompiles(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	paths := []string{
		"internal/brief/schema.json",
		"internal/core/headless/schema.json",
		"plugins/slack/extract/schema.json",
		"plugins/slack/eval/brief.schema.json",
		"plugins/slack/eval/judge.schema.json",
	}
	matches, _ := filepath.Glob(filepath.Join(root, "profiles/default/prompts/*schema*.json"))
	plugins, _ := filepath.Glob(filepath.Join(root, "plugins/*/profile/prompts/*schema*.json"))
	for _, m := range append(matches, plugins...) {
		rel, _ := filepath.Rel(root, m)
		paths = append(paths, rel)
	}
	if err := filepath.WalkDir(filepath.Join(root, "profiles/default"), func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Name() == "schema.json" {
			rel, _ := filepath.Rel(root, p)
			paths = append(paths, rel)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(paths) < 5 {
		t.Fatalf("found only %v", paths)
	}
	for _, p := range paths {
		src, err := os.ReadFile(filepath.Join(root, p))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Compile(src, Options{}); err != nil {
			t.Errorf("%s: %v", p, err)
		}
	}
}

func TestCompileRefusesAnEnumNoAnswerCanBeComparedTo(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{`{"type":"array","items":{"type":"string"},"enum":[["x"]]}`, `$: enum: ["x"] is not a string, number, boolean or null`},
		{`{"type":"object","properties":{"o":{"type":"object","enum":[{"k":"v"}]}}}`, `$.o: enum: {"k":"v"} is not a string`},
		{`{"type":"object","properties":{"risk":{"type":"string","enum":["low",3]}}}`, `$.risk: enum: 3 is not a string`},
		{`{"type":"integer","enum":[1.5]}`, `$: enum: 1.5 is not an integer`},
		{`{"type":"boolean","enum":["true"]}`, `$: enum: "true" is not a boolean`},
	} {
		_, err := Compile([]byte(c.src), Options{})
		if !errors.Is(err, ErrSchema) || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.src, err, c.want)
		}
	}
	for _, src := range []string{
		`{"type":"integer","enum":[1,2]}`, `{"type":"number","enum":[1.5,2]}`,
		`{"type":"null","enum":[null]}`, `{"type":"boolean","enum":[true]}`,
	} {
		if _, err := Compile([]byte(src), Options{}); err != nil {
			t.Errorf("%s: %v", src, err)
		}
	}
}
