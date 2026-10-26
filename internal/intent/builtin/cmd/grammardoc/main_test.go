package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
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
