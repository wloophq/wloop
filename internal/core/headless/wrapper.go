package headless

import (
	"errors"
	"fmt"
	"strings"
	"text/template"

	"github.com/wloophq/wloop/internal/brand"
)

// WrapperFile is the profile file of the headless call's prompt.
const WrapperFile = "prompts/headless.md.tmpl"

// Wrapper is the headless call's prompt around the unit's instructions, a
// template defining "before", the text ahead of the unit data's markers,
// and "after", the text between the end marker and the answer schema. The
// markers, the instructions between them and the schema are written in Go.
type Wrapper struct{ t *template.Template }

// WrapperData is what the wrapper's templates read.
type WrapperData struct {
	Name, Unit, Checkout, Dossier string
	// Items is the items acted on, each in backquotes, comma-separated.
	Items string
	// Guidance is the user's guidance, each line indented four spaces.
	Guidance string
	// Begin and End are the unit data's markers; Nonce is their code.
	Begin, End, Nonce string
	MaxDrafts         int
}

// ParseWrapper reads a wrapper template; it must define before and after.
func ParseWrapper(src string) (Wrapper, error) {
	t, err := template.New(WrapperFile).Option("missingkey=error").Parse(src)
	if err != nil {
		return Wrapper{}, err
	}
	for _, name := range []string{"before", "after"} {
		if t.Lookup(name) == nil {
			return Wrapper{}, fmt.Errorf("%s defines no %q template", WrapperFile, name)
		}
	}
	return Wrapper{t: t}, nil
}

// Prompt is the headless call's prompt: the wrapper's before, the unit's
// instructions between this call's markers, the wrapper's after, then the
// answer schema, last so nothing quoted from the unit follows it.
func (w Wrapper) Prompt(r Request, checkout, dossier, instructions string) (string, error) {
	if w.t == nil {
		return "", errors.New("no headless wrapper is loaded")
	}
	nonce := randomHex()
	d := WrapperData{Name: brand.Name, Unit: r.Unit.Label(), Checkout: checkout, Dossier: dossier,
		Begin: "<<<UNIT DATA " + nonce + ">>>", End: "<<<END OF UNIT DATA " + nonce + ">>>", Nonce: nonce, MaxDrafts: MaxDrafts}
	if len(r.Items) > 0 {
		d.Items = "`" + strings.Join(r.Items, "`, `") + "`"
	}
	if g := strings.TrimSpace(r.Guidance); g != "" {
		for _, l := range strings.Split(g, "\n") {
			d.Guidance += "    " + l + "\n"
		}
	}
	var b strings.Builder
	if err := w.t.ExecuteTemplate(&b, "before", d); err != nil {
		return "", err
	}
	b.WriteString(d.Begin + "\n" + instructions + "\n" + d.End + "\n\n")
	if err := w.t.ExecuteTemplate(&b, "after", d); err != nil {
		return "", err
	}
	b.WriteString(Schema())
	b.WriteString("\n")
	return b.String(), nil
}
