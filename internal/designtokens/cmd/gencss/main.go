// Command gencss writes the stylesheets generated from tokens.json: the token
// region of the cockpit's app.css, then the design system's bundle.css.
//
//	gencss <tokens.json> <app.css> <preview.css> <bundle.css>
package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/wloophq/wloop/internal/designtokens"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "gencss:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) != 4 {
		return errors.New("usage: gencss <tokens.json> <app.css> <preview.css> <bundle.css>")
	}
	raw, err := os.ReadFile(args[0])
	if err != nil {
		return err
	}
	tok, err := designtokens.Parse(raw)
	if err != nil {
		return err
	}
	css, err := os.ReadFile(args[1])
	if err != nil {
		return err
	}
	if css, err = designtokens.Splice(css, tok.CSS()); err != nil {
		return err
	}
	preview, err := os.ReadFile(args[2])
	if err != nil {
		return err
	}
	bundle, err := designtokens.Bundle(tok, css, preview)
	if err != nil {
		return err
	}
	if err := os.WriteFile(args[1], css, 0o644); err != nil {
		return err
	}
	return os.WriteFile(args[3], bundle, 0o644)
}
