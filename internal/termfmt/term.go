// Package termfmt writes text for a terminal: whether to color it, the
// styles and links, and the width of what it prints. Width is measured on
// plain text; styles are applied after, so no escape reaches a measure.
package termfmt

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/term"
)

// Mode is the --color choice.
type Mode int

const (
	Auto Mode = iota
	Always
	Never
)

var modeNames = []string{"auto", "always", "never"}

func (m Mode) String() string { return modeNames[m] }

// Set parses auto, always or never, so a Mode is a flag.Value.
func (m *Mode) Set(s string) error {
	for i, n := range modeNames {
		if s == n {
			*m = Mode(i)
			return nil
		}
	}
	return fmt.Errorf("--color takes auto, always or never, not %q", s)
}

// Term is how to write to one output: TTY lays it out for a person, Color
// styles it, Width is its columns.
type Term struct {
	TTY   bool
	Color bool
	Width int
}

// Detect decides for w: a terminal gets the layout and color; GH_FORCE_TTY
// and --color always force both; NO_COLOR, TERM=dumb and CLICOLOR=0 take the
// color away and CLICOLOR_FORCE gives it back, never the layout, unless
// NO_COLOR is set; mode overrides the color.
func Detect(w io.Writer, getenv func(string) string, mode Mode) Term {
	f, ok := w.(*os.File)
	terminal := ok && term.IsTerminal(int(f.Fd()))
	t := decide(terminal, getenv, mode, runtime.GOOS)
	if terminal {
		if cols, _, err := term.GetSize(int(f.Fd())); err == nil && cols > 0 {
			t.Width = cols
		}
	}
	return t
}

// decide is Detect without the file: terminal says whether it is one.
func decide(terminal bool, getenv func(string) string, mode Mode, goos string) Term {
	tty := terminal || getenv("GH_FORCE_TTY") != ""
	force := getenv("CLICOLOR_FORCE")
	var color bool
	switch {
	case mode != Auto:
		color = mode == Always
	case getenv("NO_COLOR") != "":
	case force != "" && force != "0":
		color = true
	case getenv("TERM") == "dumb" || getenv("CLICOLOR") == "0":
	default:
		// A Windows console prints escapes raw until VT processing is on.
		color = tty && goos != "windows"
	}
	return Term{TTY: tty || mode == Always, Color: color, Width: width(getenv)}
}

// width is the columns off a terminal: GH_FORCE_TTY when it is a number,
// as gh reads it, else COLUMNS, else 80.
func width(getenv func(string) string) int {
	for _, k := range []string{"GH_FORCE_TTY", "COLUMNS"} {
		if n, err := strconv.Atoi(getenv(k)); err == nil && n > 0 {
			return n
		}
	}
	return 80
}

func (t Term) sgr(on, off, s string) string {
	if !t.Color {
		return s
	}
	return "\x1b[" + on + "m" + s + "\x1b[" + off + "m"
}

func (t Term) Bold(s string) string   { return t.sgr("1", "22", s) }
func (t Term) Dim(s string) string    { return t.sgr("2", "22", s) }
func (t Term) Red(s string) string    { return t.sgr("31", "39", s) }
func (t Term) Green(s string) string  { return t.sgr("32", "39", s) }
func (t Term) Yellow(s string) string { return t.sgr("33", "39", s) }
func (t Term) Cyan(s string) string   { return t.sgr("36", "39", s) }

// Reverse swaps the foreground and background, as a selected line is drawn.
func (t Term) Reverse(s string) string { return t.sgr("7", "27", s) }

// Link makes text an OSC 8 hyperlink to url; a terminal that does not know
// it prints the text.
func (t Term) Link(url, text string) string {
	if !t.Color || url == "" || strings.ContainsFunc(url, unicode.IsControl) {
		return text
	}
	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}

var escapes = regexp.MustCompile(`\x1b\[[0-9;]*m|\x1b\]8;;[^\x1b]*\x1b\\`)

// Plain is s without the styles and links of this package.
func Plain(s string) string { return escapes.ReplaceAllString(s, "") }
