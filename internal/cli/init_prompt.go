package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/termfmt"
)

// initPrompt asks at a terminal; an empty answer, or the end of the input,
// takes the default.
type initPrompt struct {
	out io.Writer
	in  *bufio.Reader
	t   termfmt.Term
	eof bool
}

func newInitPrompt(e *Env, t termfmt.Term) *initPrompt {
	in := e.In
	if in == nil {
		in = os.Stdin
	}
	return &initPrompt{out: e.Out, in: bufio.NewReader(in), t: t}
}

func (p *initPrompt) read() string {
	s, err := p.in.ReadString('\n')
	if err != nil && s == "" {
		p.eof = true
		fmt.Fprintln(p.out)
	}
	return strings.TrimSpace(s)
}

func (p *initPrompt) line(question, def string) string {
	if def != "" {
		fmt.Fprintf(p.out, "%s %s: ", question, p.t.Dim("["+def+"]"))
	} else {
		fmt.Fprintf(p.out, "%s: ", question)
	}
	s := p.read()
	if s == "" {
		return def
	}
	return s
}

func (p *initPrompt) yes(question string, def bool) bool {
	hint := "[y/N]"
	if def {
		hint = "[Y/n]"
	}
	for {
		fmt.Fprintf(p.out, "%s %s ", question, hint)
		switch strings.ToLower(p.read()) {
		case "":
			return def
		case "y", "yes":
			return true
		case "n", "no":
			return false
		}
	}
}

// list reads a comma- or space-separated list; "-" is none.
func (p *initPrompt) list(question string, def []string) []string {
	shown := strings.Join(def, ", ")
	if len(def) == 0 {
		shown = "-"
	}
	s := p.line(question, shown)
	if s == "-" || s == "" {
		return nil
	}
	return splitList(s)
}

func splitList(s string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' }) {
		if !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	return out
}

func (p *initPrompt) choose(question string, options []string, def string) string {
	for {
		s := p.line(question+" ("+strings.Join(options, ", ")+")", def)
		if slices.Contains(options, s) {
			return s
		}
		if s == def {
			return def
		}
		fmt.Fprintf(p.out, "Choose one of: %s.\n", strings.Join(options, ", "))
	}
}
