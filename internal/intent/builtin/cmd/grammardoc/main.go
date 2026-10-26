// Command grammardoc writes what the grammar table derives: the JSON schemas
// of the profile files and the configuration, the generated tables of the
// manifest and configuration reference pages, and the language page. Run it
// from the repository's root, through make docs-ref.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/wloophq/wloop/internal/intent/builtin"
)

const referenceDir = "docs/src/content/docs/reference"

func main() {
	out, err := outputs(".")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, f := range out {
		if err := os.WriteFile(f.path, f.body, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}

type output struct {
	path string
	body []byte
}

// outputs are the files grammardoc writes under root, a page with
// generated tables read from root so its prose stays.
func outputs(root string) ([]output, error) {
	var out []output
	for _, s := range schemas {
		b, err := schemaOf(s)
		if err != nil {
			return nil, err
		}
		out = append(out, output{filepath.Join(root, "internal/intent", s.file), b})
	}
	for page, tables := range map[string][]string{
		"intent-manifest.md":   {keyTable(builtin.SurfaceIntent, "Key")},
		"workflow-manifest.md": {keyTable(builtin.SurfaceWorkflow, "Key"), stepTable()},
		"configuration.md":     {keyTable(builtin.SurfaceConfig, "Setting")},
	} {
		path := filepath.Join(root, referenceDir, page)
		doc, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		for _, t := range tables {
			if doc, err = replaceTable(doc, t); err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
		}
		out = append(out, output{path, doc})
	}
	lang, err := languagePage()
	if err != nil {
		return nil, err
	}
	out = append(out, output{filepath.Join(root, referenceDir, "language.md"), []byte(lang)})
	slices.SortFunc(out, func(a, b output) int { return strings.Compare(a.path, b.path) })
	return out, nil
}

// replaceTable swaps the table of doc that starts with t's header line,
// up to the blank line that ends it, for t.
func replaceTable(doc []byte, t string) ([]byte, error) {
	header, _, _ := strings.Cut(t, "\n")
	before, rest, ok := strings.Cut(string(doc), header+"\n")
	if !ok {
		return nil, fmt.Errorf("no table starts with %q", header)
	}
	_, after, _ := strings.Cut(rest, "\n\n")
	return []byte(before + t + "\n" + after), nil
}

func rows(surface builtin.Surface) []builtin.Key {
	var out []builtin.Key
	for _, k := range builtin.Grammar {
		if k.In == surface {
			out = append(out, k)
		}
	}
	return out
}

// shown is a row's path as a reader writes it: [] marks an array of tables
// and <name> a name the author chooses.
func shown(path string) string {
	return strings.ReplaceAll(path, "*", "<name>")
}

func values(k builtin.Key) []string {
	if k.Values == nil {
		return nil
	}
	var out []string
	for _, v := range k.Values() {
		out = append(out, v.Name)
	}
	return out
}

var kindWords = map[builtin.Kind]string{
	builtin.KindPred: "predicate", builtin.KindFact: "fact", builtin.KindTemplate: "template", builtin.KindFile: "file",
	builtin.KindEnum: "one of", builtin.KindDur: "duration", builtin.KindBool: "bool", builtin.KindInt: "integer",
	builtin.KindText: "string", builtin.KindList: "list", builtin.KindTable: "table", builtin.KindMap: "table by name",
	builtin.KindAny: "string or table", builtin.KindNumber: "number",
}

// kind is what a row's value is, with its values.
func kind(k builtin.Key) string {
	vs := values(k)
	quoted := make([]string, len(vs))
	for i, v := range vs {
		quoted[i] = "`" + v + "`"
	}
	switch {
	case k.Kind == builtin.KindEnum:
		return "one of " + strings.Join(quoted, ", ")
	case k.Kind == builtin.KindList && len(vs) > 0:
		return "list of " + strings.Join(quoted, ", ")
	case k.Kind == builtin.KindList:
		return "list of " + kindWords[k.Elem] + "s"
	}
	return kindWords[k.Kind]
}

func cell(s string) string {
	return strings.NewReplacer("|", `\|`, "<", "&lt;", ">", "&gt;").Replace(s)
}

func defaultOf(k builtin.Key) string {
	if k.Default == "" {
		return ""
	}
	return "`" + k.Default + "`"
}

func keyTable(surface builtin.Surface, title string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "| %s | Kind | Default | Meaning |\n|---|---|---|---|\n", title)
	for _, k := range rows(surface) {
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", cell(shown(k.Path)), kind(k), defaultOf(k), cell(k.Doc))
	}
	return b.String()
}

func stepTable() string {
	var b strings.Builder
	b.WriteString("| Step | Argument | Kind | Default | Meaning |\n|---|---|---|---|---|\n")
	for _, s := range builtin.Steps {
		args := rows(builtin.SurfaceStep)
		for _, k := range args {
			if k.Step == s.Name {
				fmt.Fprintf(&b, "| `%s` | `%s` | %s | %s | %s |\n", s.Name, k.Path, kind(k), defaultOf(k), cell(k.Doc))
			}
		}
	}
	return b.String()
}

// sections are the language page's sections, one per file, in reading order.
var sections = []struct {
	surface builtin.Surface
	title   string
}{
	{builtin.SurfaceIntent, "Intent manifest (`intents/<name>/intent.toml`)"},
	{builtin.SurfaceActions, "Built-in actions (`actions.toml`)"},
	{builtin.SurfaceWorkflow, "Workflow manifest (`workflows/<name>/workflow.toml`)"},
	{builtin.SurfaceStep, "Steps and their arguments"},
	{builtin.SurfaceDefaults, "Defaults (`defaults.toml`)"},
	{builtin.SurfaceLabels, "Block headings (`labels.toml`)"},
	{builtin.SurfaceBlock, "Judgment block front matter (`+++` in `<dim>=<value>/.../intent=<name>.md`)"},
	{builtin.SurfaceConfig, "Configuration (`config.json`)"},
}

func line(k builtin.Key) string {
	s := fmt.Sprintf("- `%s` (%s", shown(k.Path), kind(k))
	if k.Default != "" {
		s += ", default `" + k.Default + "`"
	}
	return s + "): " + cell(k.Doc)
}

func languagePage() (string, error) {
	out := []string{
		"---",
		"title: Profile language",
		"description: Every reserved word of the profile files and the configuration, one line each.",
		"---",
		"",
		"Every key a profile file or `config.json` may set, with its kind, its values and its default. A key or a value not listed here fails to load, with the closest listed one as a suggestion. `<name>` is a name the author chooses, and `[]` marks an array of tables (`[[step]]`). Predicates and templates read the [facts](/reference/facts/); the manifests are detailed in the [intent manifest](/reference/intent-manifest/), [workflow manifest](/reference/workflow-manifest/) and [configuration](/reference/configuration/) pages.",
	}
	for _, s := range sections {
		out = append(out, "", "## "+s.title, "")
		if gs, ok := groups[s.surface]; ok {
			parts, err := grouped(rows(s.surface), gs)
			if err != nil {
				return "", err
			}
			for i, g := range gs {
				out = append(out, "### "+g.title, "")
				for _, k := range parts[i] {
					out = append(out, line(k))
				}
				out = append(out, "")
			}
			out = out[:len(out)-1]
			continue
		}
		if s.surface != builtin.SurfaceStep {
			for _, k := range rows(s.surface) {
				out = append(out, line(k))
			}
			continue
		}
		for _, st := range builtin.Steps {
			args := builtin.StepArgs(st.Name)
			if len(args) == 0 {
				out = append(out, fmt.Sprintf("- `%s`: no arguments.", st.Name))
				continue
			}
			out = append(out, fmt.Sprintf("- `%s`:", st.Name))
			for _, k := range rows(builtin.SurfaceStep) {
				if k.Step == st.Name {
					out = append(out, "  "+line(k))
				}
			}
		}
	}
	out = append(out, "", "This page is generated from the grammar table by `make docs-ref`.")
	return strings.Join(out, "\n") + "\n", nil
}

var schemas = []struct {
	file, title string
	surface     builtin.Surface
}{
	{"intent.schema.json", "Intent manifest (intent.toml)", builtin.SurfaceIntent},
	{"workflow.schema.json", "Workflow manifest (workflow.toml)", builtin.SurfaceWorkflow},
	{"actions.schema.json", "Built-in actions (actions.toml)", builtin.SurfaceActions},
	{"config.schema.json", "Configuration (config.json)", builtin.SurfaceConfig},
}

// node is a table of the grammar's tree: its row, its keys, an array of
// tables' element, and the schema of an author's names.
type node struct {
	row   *builtin.Key
	array bool
	keys  map[string]*node
}

func (n *node) child(seg string) *node {
	if n.keys == nil {
		n.keys = map[string]*node{}
	}
	name := strings.TrimSuffix(seg, "[]")
	c, ok := n.keys[name]
	if !ok {
		c = &node{}
		n.keys[name] = c
	}
	c.array = c.array || strings.HasSuffix(seg, "[]")
	return c
}

func tree(surface builtin.Surface) *node {
	root := &node{}
	for _, k := range rows(surface) {
		n := root
		for _, seg := range strings.Split(k.Path, ".") {
			n = n.child(seg)
		}
		n.row = &k
	}
	return root
}

func schemaOf(s struct {
	file, title string
	surface     builtin.Surface
}) ([]byte, error) {
	doc := object(tree(s.surface))
	doc["$schema"] = "http://json-schema.org/draft-07/schema#"
	doc["$id"] = "https://github.com/wloophq/wloop/" + s.file
	doc["title"] = s.title
	if s.surface == builtin.SurfaceWorkflow {
		step := doc["properties"].(map[string]any)["step"].(map[string]any)["items"].(map[string]any)
		step["allOf"] = stepArgs()
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// object is the schema of a table's keys; a name the author chooses
// takes any key.
func object(n *node) map[string]any {
	out := map[string]any{"type": "object"}
	props := map[string]any{}
	for name, c := range n.keys {
		if name == "*" {
			out["additionalProperties"] = schemaOfNode(c)
			continue
		}
		props[name] = schemaOfNode(c)
	}
	if len(props) > 0 {
		out["properties"] = props
	}
	if _, ok := out["additionalProperties"]; !ok {
		out["additionalProperties"] = false
	}
	return out
}

func schemaOfNode(n *node) map[string]any {
	var s map[string]any
	switch {
	case len(n.keys) > 0 && n.row != nil && n.row.Kind == builtin.KindAny:
		s = map[string]any{"oneOf": []any{map[string]any{"type": "string"}, object(n)}}
	case len(n.keys) > 0:
		s = object(n)
	case n.row != nil:
		s = leaf(*n.row)
	default:
		s = map[string]any{}
	}
	if n.array {
		s = map[string]any{"type": "array", "items": s}
	}
	if n.row != nil {
		s["description"] = n.row.Doc
		if d, ok := typedDefault(*n.row); ok {
			s["default"] = d
		}
	}
	return s
}

func leaf(k builtin.Key) map[string]any {
	switch k.Kind {
	case builtin.KindBool:
		return map[string]any{"type": "boolean"}
	case builtin.KindInt:
		return map[string]any{"type": "integer"}
	case builtin.KindNumber:
		return map[string]any{"type": "number"}
	case builtin.KindAny:
		return map[string]any{"oneOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "array", "items": map[string]any{"type": "string"}}}}
	case builtin.KindTable:
		return map[string]any{"type": "object"}
	case builtin.KindMap:
		return map[string]any{"type": "object"}
	case builtin.KindList:
		el := leaf(builtin.Key{Kind: k.Elem, Values: k.Values})
		return map[string]any{"type": "array", "items": el}
	case builtin.KindEnum:
		return map[string]any{"type": "string", "enum": values(k)}
	}
	return map[string]any{"type": "string"}
}

func typedDefault(k builtin.Key) (any, bool) {
	if k.Default == "" {
		return nil, false
	}
	switch k.Kind {
	case builtin.KindBool:
		return k.Default == "true", true
	case builtin.KindInt:
		n, err := strconv.Atoi(k.Default)
		return n, err == nil
	case builtin.KindNumber:
		n, err := strconv.ParseFloat(k.Default, 64)
		return n, err == nil
	}
	return k.Default, true
}

// stepArgs are a step's arguments, keyed on its use.
func stepArgs() []any {
	var out []any
	for _, st := range builtin.Steps {
		props := map[string]any{"use": map[string]any{"const": st.Name}}
		for _, k := range rows(builtin.SurfaceStep) {
			if k.Step == st.Name {
				s := leaf(k)
				s["description"] = k.Doc
				if d, ok := typedDefault(k); ok {
					s["default"] = d
				}
				props[k.Path] = s
			}
		}
		out = append(out, map[string]any{
			"if":   map[string]any{"properties": map[string]any{"use": map[string]any{"const": st.Name}}, "required": []string{"use"}},
			"then": map[string]any{"properties": props, "additionalProperties": false},
		})
	}
	return out
}
