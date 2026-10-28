package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/wloophq/wloop/internal/intent/builtin"
)

func TestGrammarDocIsCurrent(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..", "..")
	out, err := outputs(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range out {
		got, err := os.ReadFile(f.path)
		if err != nil || !bytes.Equal(got, f.body) {
			t.Errorf("%s is not what the grammar table generates (%v); regenerate it with make docs-ref", f.path, err)
		}
	}
}

func TestAKeyOutsideEveryGroupFails(t *testing.T) {
	gs := []group{{"Identity", []string{"label"}}}
	rows := []builtin.Key{{Path: "label"}, {Path: "label.short"}, {Path: "role"}}
	if _, err := grouped(rows, gs); err == nil {
		t.Fatal("role is in no group, and grouped took it")
	}
}

func TestAGroupKeyNoRowMatchesFails(t *testing.T) {
	gs := []group{{"Identity", []string{"label", "gone"}}}
	if _, err := grouped([]builtin.Key{{Path: "label"}}, gs); err == nil {
		t.Fatal("gone matches no row, and grouped took it")
	}
}

func TestARowGoesToItsLongestKey(t *testing.T) {
	gs := []group{{"GitHub", []string{"github"}}, {"Scope", []string{"github.scope"}}}
	rows := []builtin.Key{{Path: "github"}, {Path: "github.scope"}, {Path: "github.scope.people"}, {Path: "github.enabled"}}
	got, err := grouped(rows, gs)
	if err != nil {
		t.Fatal(err)
	}
	if len(got[0]) != 2 || got[0][1].Path != "github.enabled" || len(got[1]) != 2 {
		t.Errorf("grouped = %v", got)
	}
}

func TestALayeredFileSchemaRequiresNoEntryKey(t *testing.T) {
	entry := func(doc map[string]any, path ...string) map[string]any {
		for _, p := range path {
			doc = doc["properties"].(map[string]any)[p].(map[string]any)
		}
		return doc["additionalProperties"].(map[string]any)
	}
	tasks := object(tree(builtin.SurfaceTasks))
	gates := object(tree(builtin.SurfaceGates))
	views := object(tree(builtin.SurfaceViews))
	for name, e := range map[string]map[string]any{
		"props.*": entry(tasks, "props"), "states.*": entry(tasks, "states"), "verbs.*": entry(tasks, "verbs"),
		"gates.*": entry(gates, "gates"), "gates.*.intents.*": entry(entry(gates, "gates"), "intents"),
		"views.*": entry(views, "views"),
	} {
		if r, ok := e["required"]; ok {
			t.Errorf("%s requires %v", name, r)
		}
	}
	intent := object(tree(builtin.SurfaceIntent))
	if _, ok := intent["required"]; !ok {
		t.Error("a file that is not layered key by key lost its required keys")
	}
}
