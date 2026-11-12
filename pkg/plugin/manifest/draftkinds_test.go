package manifest

import (
	"strings"
	"testing"
	"testing/fstest"
)

const draftHead = "id = \"acme\"\nkinds = [\"source\"]\nprotocol = 1\n[source]\nname = \"Acme\"\nslot = \"acme\"\ncodec = {keys = [\"{id}\"]}\n" +
	"[[source.deliveries]]\nname = \"review\"\nposts = true\n"

const verdictSchema = `{"type": "object", "required": ["event", "inner"], "properties": {
	"event": {"type": "string", "enum": ["APPROVE", "COMMENT"]},
	"note": {"type": "string", "enum": ["a", "b"]},
	"count": {"type": "integer"},
	"free": {"type": "string"},
	"inner": {"type": "object", "required": ["mode"], "properties": {"mode": {"type": "string", "enum": ["x"]}}},
	"loose": {"type": "object", "properties": {"mode": {"type": "string", "enum": ["x"]}}}
}}`

// reviewKind is a draft kind of acme with one fact, whose name and pointer vary.
func reviewKind(name, pointer string) string {
	return "[[source.draft_kinds]]\nname = \"review\"\ndelivery = \"review\"\nschema = \"drafts/review/schema.json\"\n" +
		"[[source.draft_kinds.facts]]\nname = \"" + name + "\"\npointer = \"" + pointer + "\"\n"
}

func draftSource(t *testing.T, extra string, files fstest.MapFS) (*Source, error) {
	t.Helper()
	m, err := Parse([]byte(draftHead + extra))
	if err != nil {
		t.Fatal(err)
	}
	if files != nil {
		m.Files = files
	}
	return m.Source()
}

func reviewFiles() fstest.MapFS {
	return fstest.MapFS{"drafts/review/schema.json": {Data: []byte(verdictSchema)}}
}

func TestFactPointerMustBeRequiredEnum(t *testing.T) {
	s, err := draftSource(t, reviewKind("draft.acme.review.event", "/event"), reviewFiles())
	if err != nil {
		t.Fatal(err)
	}
	if got := string(s.DraftKinds[0].SchemaJSON); got != verdictSchema {
		t.Errorf("schema read %q", got)
	}
	if _, err := draftSource(t, reviewKind("draft.acme.review.mode", "/inner/mode"), reviewFiles()); err != nil {
		t.Errorf("a required enum inside a required object: %v", err)
	}
	for _, c := range []struct{ name, pointer, want string }{
		{"an optional enum", "/note", "not required"},
		{"a field the schema lacks", "/nope", "no property"},
		{"an optional integer", "/count", "not required"},
		{"an optional string with no enum", "/free", "not required"},
		{"an enum inside an optional object", "/loose/mode", "not required"},
		{"no pointer", "event", "not a JSON pointer"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := draftSource(t, reviewKind("draft.acme.review.x", c.pointer), reviewFiles())
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("Source = %v, want an error naming %q", err, c.want)
			}
		})
	}
	for _, c := range []struct{ name, schema, want string }{
		{"a required string with no enum", `{"type": "object", "required": ["x"], "properties": {"x": {"type": "string"}}}`, "no enum"},
		{"a required enum that is not a string", `{"type": "object", "required": ["x"], "properties": {"x": {"type": "integer", "enum": [1, 2]}}}`, "not a string"},
		{"an enum under a string parent", `{"type": "object", "required": ["a"], "properties": {"a": {"type": "string", "required": ["x"], "properties": {"x": {"type": "string", "enum": ["x"]}}}}}`, "not an object"},
		{"an enum under a array parent", `{"type": "object", "required": ["a"], "properties": {"a": {"type": "array", "required": ["x"], "properties": {"x": {"type": "string", "enum": ["x"]}}}}}`, "not an object"},
		{"an enum under a null parent", `{"type": "object", "required": ["a"], "properties": {"a": {"type": "null", "required": ["x"], "properties": {"x": {"type": "string", "enum": ["x"]}}}}}`, "not an object"},
		{"a string enum with a number in it", `{"type": "object", "required": ["x"], "properties": {"x": {"type": "string", "enum": ["a", 2]}}}`, "not a string"},
	} {
		t.Run(c.name, func(t *testing.T) {
			files := fstest.MapFS{"drafts/review/schema.json": {Data: []byte(c.schema)}}
			pointer := "/x"
			if strings.Contains(c.schema, `"required": ["a"]`) {
				pointer = "/a/x"
			}
			if _, err := draftSource(t, reviewKind("draft.acme.review.x", pointer), files); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("Source = %v, want an error naming %q", err, c.want)
			}
		})
	}
}

func TestFactNamePrefix(t *testing.T) {
	for _, name := range []string{"draft.other.review.event", "draft.acme.reply.event", "acme.review.event", "draft.acme.review.", "draft.acme.review.a.b", "draft.acme.review.Event", "event"} {
		t.Run(name, func(t *testing.T) {
			_, err := draftSource(t, reviewKind(name, "/event"), reviewFiles())
			if err == nil || !strings.Contains(err.Error(), "draft.acme.review.<field>") {
				t.Errorf("Source = %v, want an error naming the prefix", err)
			}
		})
	}
	two := reviewKind("draft.acme.review.event", "/event") + "[[source.draft_kinds.facts]]\nname = \"draft.acme.review.event\"\npointer = \"/event\"\n"
	if _, err := draftSource(t, two, reviewFiles()); err == nil || !strings.Contains(err.Error(), "twice") {
		t.Errorf("a fact declared twice: %v", err)
	}
}

func TestDraftKindFilesRefuse(t *testing.T) {
	kind := "[[source.draft_kinds]]\nname = \"review\"\ndelivery = \"review\"\n"
	for _, c := range []struct {
		name, extra string
		files       fstest.MapFS
		want        string
	}{
		{"no schema", kind, reviewFiles(), "schema: is required"},
		{"a schema not in the directory", kind + "schema = \"drafts/nope.json\"\n", reviewFiles(), "nope.json"},
		{"no directory", kind + "schema = \"drafts/review/schema.json\"\n", nil, "no plugin directory"},
		{"a schema outside the directory", kind + "schema = \"../review/schema.json\"\n", reviewFiles(), "not a path inside"},
		{"a schema that is not an object", kind + "schema = \"drafts/review/schema.json\"\n", fstest.MapFS{"drafts/review/schema.json": {Data: []byte(`[]`)}}, "not a JSON object"},
		{"a payload that is not an object", kind + "schema = \"drafts/review/schema.json\"\n", fstest.MapFS{"drafts/review/schema.json": {Data: []byte(`{"type": "string"}`)}}, "not of type object"},
		{"an unknown basis", kind + "schema = \"drafts/review/schema.json\"\nbasis = \"person\"\n", reviewFiles(), "basis"},
		{"an editable pointer the schema lacks", kind + "schema = \"drafts/review/schema.json\"\neditable = [\"text\", \"/nope\"]\n", reviewFiles(), "editable \"/nope\""},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := draftSource(t, c.extra, c.files)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("Source = %v, want an error naming %q", err, c.want)
			}
		})
	}
	t.Run("a preview not written yet loads", func(t *testing.T) {
		extra := kind + "schema = \"drafts/review/schema.json\"\npreview = \"drafts/review/preview.md.tmpl\"\neditable = [\"text\", \"/event\"]\n"
		if _, err := draftSource(t, extra, reviewFiles()); err != nil {
			t.Error(err)
		}
	})
}
