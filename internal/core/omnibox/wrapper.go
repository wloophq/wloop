package omnibox

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"text/template"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/headless"
)

// WrapperFile is the profile file of the omnibox call's prompt.
const WrapperFile = "prompts/omnibox.md.tmpl"

// Wrapper is the omnibox call's prompt around the user's request, a template defining "before", the text
// ahead of the request's markers, "follow", one question already answered (an AskFollowUp), written after the
// request inside the markers, and "after", the text between the end marker and the answer schema. The
// markers, the request between them and the schema are written in Go.
type Wrapper struct{ t *template.Template }

// WrapperData is what the wrapper's templates read.
type WrapperData struct {
	Name string
	// Dir is the snapshot directory the call may read: tasks.json, views.json and repos.json, and
	// vocabulary.json when the omnibox has one.
	Dir string
	// Begin and End are the request's markers; Nonce is their code.
	Begin, End, Nonce string
	MaxOperations     int
}

// MaxOperations is how many operations one proposal may hold.
const MaxOperations = 20

// ParseWrapper reads a wrapper template; it must define before and after.
func ParseWrapper(src string) (Wrapper, error) {
	t, err := template.New(WrapperFile).Option("missingkey=error").Parse(src)
	if err != nil {
		return Wrapper{}, err
	}
	for _, name := range []string{"before", "follow", "after"} {
		if t.Lookup(name) == nil {
			return Wrapper{}, fmt.Errorf("%s defines no %q template", WrapperFile, name)
		}
	}
	return Wrapper{t: t}, nil
}

// Prompt is the omnibox call's prompt: the wrapper's before, the user's request and the questions already
// answered between this call's markers, the wrapper's after, then the answer schema, last so nothing quoted
// follows it.
func (w Wrapper) Prompt(in AskRequest, dir string) (string, error) {
	if w.t == nil {
		return "", errors.New("no omnibox wrapper is loaded")
	}
	begin, end, nonce := headless.Markers("USER REQUEST")
	d := WrapperData{Name: brand.Name, Dir: dir, Begin: begin, End: end, Nonce: nonce, MaxOperations: MaxOperations}
	var b strings.Builder
	if err := w.t.ExecuteTemplate(&b, "before", d); err != nil {
		return "", err
	}
	b.WriteString(begin + "\n" + strings.TrimSpace(in.Text) + "\n")
	for _, f := range in.Follow {
		b.WriteString("\n")
		f := AskFollowUp{Question: strings.TrimSpace(f.Question), Answer: strings.TrimSpace(f.Answer)}
		if err := w.t.ExecuteTemplate(&b, "follow", f); err != nil {
			return "", err
		}
	}
	b.WriteString(end + "\n\n")
	if err := w.t.ExecuteTemplate(&b, "after", d); err != nil {
		return "", err
	}
	b.WriteString(Schema() + "\n")
	return b.String(), nil
}

//go:embed schema.json
var schemaJSON []byte

// Schema is the answer schema of an omnibox call.
func Schema() string {
	var b bytes.Buffer
	if err := json.Compact(&b, schemaJSON); err != nil {
		panic(err)
	}
	return b.String()
}
