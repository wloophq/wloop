package catalog

import (
	"fmt"
	"net/url"
	"slices"

	"github.com/wloophq/wloop/internal/api"
	intentload "github.com/wloophq/wloop/internal/intent"
)

// CopyFacts are what the copy texts of a built-in action read: the row of the unit it runs on and the unit's
// brief, nil when it has none.
type CopyFacts struct {
	Row   api.Row
	Brief *api.Brief
}

// Get resolves a fact of the copy slot by its dotted name; nil for one the slot does not have.
func (f CopyFacts) Get(name string) any {
	b := f.Brief
	if b == nil {
		b = &api.Brief{}
	}
	switch name {
	case "copy.title":
		return f.Row.Title
	case "copy.link":
		return webLink(f.Row.Link)
	case "copy.label":
		return f.Row.Label
	case "copy.brief.goal":
		return b.Goal
	case "copy.brief.context":
		return b.Context
	case "copy.brief.kind":
		return b.Kind
	case "copy.brief.size":
		return b.Size
	case "copy.brief.risk":
		return b.Risk
	case "copy.brief.risk_why":
		return b.RiskWhy
	case "copy.brief.open_questions":
		return slices.Clone(b.OpenQuestions)
	}
	return nil
}

// webLink is link when it is an http or https address, else "": a copy pastes it as a link.
func webLink(link string) string {
	u, err := url.Parse(link)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	return link
}

// Copy is what built-in action id gives the person to copy over f, with what the client says once it copied;
// nil when the action's table under l has no copy.
func Copy(l *intentload.Loaded, id string, f CopyFacts) (*api.Copy, string, error) {
	a := actionWords(l, id)
	if !a.Copies() {
		return nil, "", nil
	}
	facts := intentload.CopyFactMap(f.Get)
	var texts [3]string
	for i, name := range []string{"text", "html", "notice"} {
		out, err := a.RenderCopy(name, facts)
		if err != nil {
			return nil, "", fmt.Errorf("the profile has errors: %s: %s.copy.%s: %w", intentload.ActionsFile, id, name, err)
		}
		texts[i] = out
	}
	return &api.Copy{Text: texts[0], HTML: texts[1]}, texts[2], nil
}
