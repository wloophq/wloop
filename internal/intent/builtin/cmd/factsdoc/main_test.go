package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const factsPage = "docs/src/content/docs/reference/facts.md"

func TestFactsDocIsCurrent(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "..", factsPage))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range tables() {
		header, _, _ := strings.Cut(want, "\n")
		_, rest, ok := strings.Cut(string(doc), header+"\n")
		if !ok {
			t.Errorf("%s lacks the generated table %q; regenerate it with make docs-ref", factsPage, header)
			continue
		}
		got, _, _ := strings.Cut(header+"\n"+rest, "\n\n")
		if got+"\n" != want {
			t.Errorf("%s's generated table %q drifted; regenerate it with make docs-ref\ngot:\n%s\nwant:\n%s", factsPage, header, got, want)
		}
	}
}
