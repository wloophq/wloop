package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDecisionsDocIsCurrent(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..")
	want, err := page(root)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(root, logPage))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("%s drifted from %s; regenerate it with make docs-ref", logPage, logDir)
	}
}
