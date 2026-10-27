// Command decisionsdoc rewrites the index of the decision log's page from the
// entries under its directory. Run it from the repository's root, through
// make docs-ref.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/wloophq/wloop/internal/archtest/decisionlog"
)

const (
	logPage = "docs/src/content/docs/architecture/decisions.md"
	logDir  = "docs/src/content/docs/architecture/decisions"
)

func main() {
	p, err := page(".")
	if err == nil {
		err = os.WriteFile(logPage, []byte(p), 0o644)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func page(root string) (string, error) {
	ds, err := decisionlog.Read(filepath.Join(root, logDir))
	if err != nil {
		return "", err
	}
	doc, err := os.ReadFile(filepath.Join(root, logPage))
	if err != nil {
		return "", err
	}
	return decisionlog.Replace(string(doc), ds)
}
