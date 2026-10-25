// Command rulesdoc prints the architecture rules reference page, read from
// the test sources of internal/archtest.
package main

import (
	"fmt"
	"os"

	"github.com/wloophq/wloop/internal/archtest/rulesdoc"
)

const archtestDir = "internal/archtest"

func main() {
	p, err := page(archtestDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Fprint(os.Stdout, p)
}

func page(dir string) (string, error) {
	rules, err := rulesdoc.Read(dir)
	if err != nil {
		return "", err
	}
	return rulesdoc.Page(rules), nil
}
