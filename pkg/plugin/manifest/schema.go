package manifest

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// shape is plugin.toml as its schema and reference page describe it.
type shape struct {
	ID       string   `toml:"id" doc:"The plugin's id, a lowercase name of at most 32 letters, digits, - and _; a source plugin's units carry it as their provider. Required."`
	Kinds    []string `toml:"kinds" doc:"The kinds the plugin's binary serves; each has a table of its name. Required." enum:"source"`
	Protocol int      `toml:"protocol" doc:"The plugin protocol version the binary speaks. Required."`
	Source   *Source  `toml:"source" doc:"What a source plugin declares: its codec, its vocabulary, its jobs, commands and tools."`
}

// SchemaID is the $id of plugin.toml's JSON schema.
const SchemaID = "https://github.com/wloophq/wloop/plugin.schema.json"

// Schema is the JSON schema of plugin.toml.
func Schema() ([]byte, error) {
	doc := schemaOf(reflect.TypeFor[shape](), "")
	doc["$id"] = SchemaID
	doc["$schema"] = "http://json-schema.org/draft-07/schema#"
	doc["title"] = "Plugin manifest (" + File + ")"
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

type field struct {
	key, doc string
	enum     []string
	typ      reflect.Type
}

func fieldsOf(t reflect.Type) []field {
	var out []field
	for f := range t.Fields() {
		if f.Anonymous {
			out = append(out, fieldsOf(f.Type)...)
			continue
		}
		key := f.Tag.Get("toml")
		if key == "" || key == "-" {
			continue
		}
		var enum []string
		if e := f.Tag.Get("enum"); e != "" {
			enum = strings.Split(e, ",")
		}
		out = append(out, field{key: key, doc: f.Tag.Get("doc"), enum: enum, typ: f.Type})
	}
	return out
}

var durationType = reflect.TypeFor[Duration]()

func schemaOf(t reflect.Type, doc string) map[string]any {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	out := map[string]any{}
	if doc != "" {
		out["description"] = doc
	}
	switch {
	case t == durationType:
		out["type"] = "string"
	case t.Kind() == reflect.String:
		out["type"] = "string"
	case t.Kind() == reflect.Bool:
		out["type"] = "boolean"
	case t.Kind() == reflect.Int:
		out["type"] = "integer"
	case t.Kind() == reflect.Slice:
		out["type"] = "array"
		out["items"] = schemaOf(t.Elem(), "")
	case t.Kind() == reflect.Map:
		out["type"] = "object"
		out["additionalProperties"] = schemaOf(t.Elem(), "")
	case t.Kind() == reflect.Struct:
		out["type"] = "object"
		out["additionalProperties"] = false
		props := map[string]any{}
		var required []string
		for _, f := range fieldsOf(t) {
			p := schemaOf(f.typ, f.doc)
			if f.enum != nil {
				if items, ok := p["items"].(map[string]any); ok {
					items["enum"] = f.enum
				} else {
					p["enum"] = f.enum
				}
			}
			props[f.key] = p
			if strings.HasSuffix(f.doc, "Required.") {
				required = append(required, f.key)
			}
		}
		out["properties"] = props
		if required != nil {
			out["required"] = required
		}
	}
	return out
}

// Reference is the reference page of plugin.toml, in Markdown.
func Reference() string {
	var b strings.Builder
	b.WriteString(`---
title: Plugin manifest
description: Every field of plugin.toml, the manifest beside a plugin's binary.
---

A plugin lives in ` + "`<config>/plugins/<id>/`" + `: its binary, its ` + "`" + File + "`" + ` and its profile layer. The host reads the manifest before it starts the binary, so a disabled plugin's unit keys still parse. The loader refuses any key this page does not list. A source plugin's manifest:

` + "```toml" + `
id = "pulls"
kinds = ["source"]
protocol = 1

[source]
name = "Pulls"
slot = "pull"

[source.codec]
keys = ["{owner}/{repo}#{number}"]
urls = ["https://pulls.example/{owner}/{repo}/pull/{number}"]

[source.codec.fields]
number = '[1-9][0-9]*'

[[source.facts]]
name = "pull.state"
type = "string"
doc = "The pull request's state."

[[source.jobs]]
name = "ingest"
every = "5m"
` + "```" + `
`)
	tables(&b, reflect.TypeFor[shape](), "", "Top-level keys")
	b.WriteString("\nThis page is generated from `pkg/plugin/manifest` by `make docs-ref`.\n")
	return b.String()
}

func tables(b *strings.Builder, t reflect.Type, path, title string) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	fs := fieldsOf(t)
	fmt.Fprintf(b, "\n## %s\n\n| Key | Kind | Meaning |\n|---|---|---|\n", title)
	var nested []field
	for _, f := range fs {
		fmt.Fprintf(b, "| `%s` | %s | %s |\n", f.key, kindOf(f), strings.ReplaceAll(f.doc, "|", `\|`))
		e := f.typ
		for e.Kind() == reflect.Pointer || e.Kind() == reflect.Slice {
			e = e.Elem()
		}
		if e.Kind() == reflect.Struct && e != durationType {
			nested = append(nested, f)
		}
	}
	for _, f := range nested {
		p := f.key
		if path != "" {
			p = path + "." + f.key
		}
		head := "[" + p + "]"
		if f.typ.Kind() == reflect.Slice {
			head = "[[" + p + "]]"
		}
		e := f.typ
		for e.Kind() == reflect.Pointer || e.Kind() == reflect.Slice {
			e = e.Elem()
		}
		tables(b, e, p, "`"+head+"`")
	}
}

func kindOf(f field) string {
	t := f.typ
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	quoted := func(vs []string) string {
		out := slices.Clone(vs)
		for i, v := range out {
			out[i] = "`" + v + "`"
		}
		return strings.Join(out, ", ")
	}
	switch {
	case t == durationType:
		return "duration"
	case t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Struct:
		return "array of tables"
	case t.Kind() == reflect.Slice && f.enum != nil:
		return "list of " + quoted(f.enum)
	case t.Kind() == reflect.Slice:
		return "list of strings"
	case t.Kind() == reflect.Map:
		return "table of strings"
	case t.Kind() == reflect.Struct:
		return "table"
	case f.enum != nil:
		return "one of " + quoted(f.enum)
	case t.Kind() == reflect.Bool:
		return "bool"
	case t.Kind() == reflect.Int:
		return "integer"
	}
	return "string"
}
