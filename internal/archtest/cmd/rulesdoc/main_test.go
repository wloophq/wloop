package main

import (
	"os"
	"path/filepath"
	"testing"
)

const rulesPage = "docs/src/content/docs/reference/architecture-rules.md"

func TestRulesDocIsCurrent(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..")
	want, err := page(filepath.Join(root, archtestDir))
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, rulesPage))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("%s drifted from internal/archtest; regenerate it with make docs-ref", rulesPage)
	}
}
