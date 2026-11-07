package cli

import (
	"bytes"
	"cmp"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"
	"text/template"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/i18n"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/profile"
)

// draftTextData is what a draft's template reads: item text is data, never
// template code. Facts are the step's subject's facts by name, none once
// the subject is gone.
type draftTextData struct {
	Workflow, Purpose, Unit, Task string
	Person                        draftTextPerson
	Items                         []draftTextItem
	Facts                         workflow.Facts
}

type draftTextPerson struct{ Key, Name string }

type draftTextItem struct {
	ID, Summary, Line, Resolution, State, Permalink string
}

// draftTemplate is file of the workflow's directory through layers, the bundled one read from bundled, in
// locale by the layer rule of profile.Texts, and the locale the served file is written in. Nil for an
// emoji delivery that names no template of its own, so a reaction carries no text even where the
// workflow's message text exists. An error wraps fs.ErrNotExist when no layer holds the file.
func draftTemplate(layers []profile.Layer, bundled fs.FS, workflowName, file string, d draft.Delivery, locale string) (*template.Template, string, error) {
	if slices.Contains(builtin.DraftEmojiDeliveries, string(d)) && file == defaultDraftText {
		return nil, "", nil
	}
	rel := path.Join("workflows", workflowName, file)
	r, err := profile.NewTexts(layers, bundled, locale, nil).File(rel)
	if err != nil {
		return nil, "", fmt.Errorf("draft.propose: %w", err)
	}
	written := cmp.Or(r.Locale, i18n.Default)
	t, err := template.New(rel).Funcs(intentload.TextFuncs(written)).Option("missingkey=error").Parse(string(r.Body))
	if err != nil {
		return nil, "", fmt.Errorf("draft.propose: %s: %w", r.Path, err)
	}
	return t, written, nil
}

// draftLanguage is the locale a draft on a unit is written in: the unit's language, the value of fact
// among facts, as the best of available; the default when available holds no locale of it; userLocale
// when the unit does not tell.
func draftLanguage(facts workflow.Facts, fact, userLocale string, available []string) string {
	v := factString(facts, fact)
	if fact == "" || v == "" {
		return userLocale
	}
	return i18n.Match([]string{v}, available)
}

// renderDraft renders t over data, filled with sub's unit and task, person and its items.
func renderDraft(t *template.Template, data draftTextData, sub draftSubject, person items.Person, its []items.Item) (string, error) {
	if t == nil {
		return "", nil
	}
	data.Unit, data.Person = sub.unit.String(), draftTextPerson{Key: person.Key, Name: person.Name}
	if sub.task != nil {
		data.Task = string(sub.task.ID)
	}
	for _, it := range its {
		data.Items = append(data.Items, draftTextItem{ID: it.ID, Summary: it.Summary, Line: strings.Join(strings.Fields(it.Summary), " "),
			Resolution: string(it.Resolution), State: string(it.State), Permalink: it.Origin.Permalink})
	}
	var b bytes.Buffer
	if err := t.Execute(&b, data); err != nil {
		return "", fmt.Errorf("draft.propose: rendering %s: %w", t.Name(), err)
	}
	return b.String(), nil
}
