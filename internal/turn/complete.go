package turn

import (
	"bytes"
	"fmt"
	"slices"
	"strings"
	"text/template"

	dintent "github.com/wloophq/wloop/internal/domain/intent"
	intentload "github.com/wloophq/wloop/internal/intent"
)

// Complete fills what the intent's manifest decides about the turn: its
// definition, its inputs normalized and zero-filled, NeedsTakeOver, Round,
// FollowUpApproval, Headline and Records. It is idempotent.
func Complete(c Context, l *intentload.Loaded) (Context, error) {
	d, err := definition(l, c.Intent)
	if err != nil {
		return c, err
	}
	c.Def = d
	if c.Payload != nil {
		c.Unit, c.Wording = c.Payload.Unit(), c.Payload.Wording()
	}
	if c.Inputs, err = normalizeInputs(d, c.Inputs); err != nil {
		return c, err
	}
	if c.Automated() {
		for _, in := range d.Inputs {
			if in.ByHandOnly && !isZero(c.Inputs[in.Name]) {
				return c, fmt.Errorf("%s: inputs.%s is by hand only: an automated round never sets it", d.Name, in.Name)
			}
		}
	}
	c.NeedsTakeOver = d.OwnerOnly && !c.Owned && !takeOverSet(d, c.Inputs)
	c.Round, c.FollowUpApproval = "", false
	if slices.Contains(d.Dims, "round") {
		c.Round = "first"
		if (c.prior() != nil || c.Planned == PlannedFollowUp) && !c.Plain {
			c.Round = "follow-up"
		}
		c.FollowUpApproval = c.Automated() && c.Round == "follow-up" && c.Planned != PlannedFirst
	}
	p := l.Programs[d.Name]
	f := turnFacts{&c}
	c.Records = p.Records == nil || p.Records.Eval(f)
	if c.Headline, err = headline(c, l.Templates[d.Name], p, f); err != nil {
		return c, err
	}
	return c, nil
}

func definition(l *intentload.Loaded, i Intent) (dintent.Def, error) {
	if l == nil || l.Registry == nil {
		return dintent.Def{}, fmt.Errorf("%s: no intents are loaded", i)
	}
	d, ok := l.Registry.Get(dintent.Name(i))
	if ok {
		return d, nil
	}
	if p, broken := l.Broken[dintent.Name(i)]; broken {
		return d, fmt.Errorf("the intent %s has problems:\n%s", i, p.Error())
	}
	return d, fmt.Errorf("unknown intent %q; use %s", i, l.Registry.Names())
}

// headline renders the manifest's headline: the resumed text inside the
// resume sentence, or the first new case whose when holds.
func headline(c Context, t *template.Template, p intentload.Programs, f turnFacts) (string, error) {
	if t == nil {
		return "", fmt.Errorf("%s: no template is loaded", c.Def.Name)
	}
	render := func(i int) (string, error) {
		h := t.Lookup(intentload.HeadlineName(i))
		if h == nil {
			return "", fmt.Errorf("%s: no %s", c.Def.Name, intentload.HeadlineName(i))
		}
		var b bytes.Buffer
		if err := h.Execute(&b, c); err != nil {
			return "", err
		}
		return b.String(), nil
	}
	if c.Resume != nil {
		what, err := render(-1)
		if err != nil {
			return "", err
		}
		if c.Resume.Answer != "" {
			return "Continue the " + what + ": the user answered.", nil
		}
		return "Continue the " + what + " where the last session stopped.", nil
	}
	for i := range c.Def.Headline.New {
		if i < len(p.Headline) && p.Headline[i] != nil && !p.Headline[i].Eval(f) {
			continue
		}
		return render(i)
	}
	return "", fmt.Errorf("%s: no headline case applies", c.Def.Name)
}

func takeOverSet(d dintent.Def, inputs map[string]any) bool {
	for _, in := range d.Inputs {
		if in.Before == "take_over" && !isZero(inputs[in.Name]) {
			return true
		}
	}
	return false
}

func isZero(v any) bool {
	switch v := v.(type) {
	case nil:
		return true
	case bool:
		return !v
	case string:
		return v == ""
	case []string:
		return len(v) == 0
	}
	return false
}

// normalizeInputs checks every set input against its declaration and
// converts a JSON-decoded list ([]any) to []string; every
// declared input left unset gets its type's zero value.
func normalizeInputs(d dintent.Def, in map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(d.Inputs))
	for name := range in {
		if !slices.ContainsFunc(d.Inputs, func(i dintent.Input) bool { return i.Name == name }) {
			return nil, fmt.Errorf("%s: inputs.%s is not an input of %s", d.Name, name, d.Name)
		}
	}
	for _, decl := range d.Inputs {
		v, err := normalize(decl, in[decl.Name])
		if err != nil {
			return nil, fmt.Errorf("%s: inputs.%s: %w", d.Name, decl.Name, err)
		}
		out[decl.Name] = v
	}
	return out, nil
}

func normalize(decl dintent.Input, v any) (any, error) {
	switch decl.Type {
	case "flag":
		switch v := v.(type) {
		case nil:
			return false, nil
		case bool:
			return v, nil
		}
	case "text":
		switch v := v.(type) {
		case nil:
			return "", nil
		case string:
			return v, nil
		}
	case "list":
		switch v := v.(type) {
		case nil:
			return []string{}, nil
		case []string:
			return slices.Clone(v), nil
		case []any:
			out := make([]string, 0, len(v))
			for _, e := range v {
				s, ok := e.(string)
				if !ok {
					return nil, fmt.Errorf("a list of strings, not one holding %T", e)
				}
				out = append(out, s)
			}
			return out, nil
		}
	default:
		return nil, fmt.Errorf("unknown input type %q", decl.Type)
	}
	return nil, fmt.Errorf("a %s input, not %T", decl.Type, v)
}

// turnFacts resolves the turn slot's facts of a context for predicates.
type turnFacts struct{ c *Context }

func (f turnFacts) Get(field string) any {
	c := f.c
	if name, ok := strings.CutPrefix(field, "inputs."); ok {
		return c.Inputs[name]
	}
	if field == "pr.mine" {
		if c.PR() == nil {
			return nil
		}
		return c.Facts.Mine
	}
	if c.Payload != nil {
		if v, ok := c.Payload.Fact(field); ok {
			return v
		}
	}
	switch field {
	case "turn.trigger":
		return c.Trigger.String()
	case "turn.automated":
		return c.Automated()
	case "turn.owned":
		return c.Owned
	case "turn.needs_take_over":
		return c.NeedsTakeOver
	case "turn.resume":
		return c.Resume != nil
	case "turn.plain":
		return c.Plain
	case "turn.planned":
		return c.Planned.String()
	case "turn.round":
		return c.Round
	case "pr.requested_teams":
		if c.PR() == nil {
			return nil
		}
		if c.Facts.RequestedTeams == nil {
			return []string{}
		}
		return c.Facts.RequestedTeams
	case "last_review.exists":
		return c.prior() != nil
	case "last_review.verdict":
		return c.Facts.LastReviewVerdict
	case "last_review.decision":
		if c.prior() == nil {
			return ""
		}
		return c.prior().Verdict
	case "config.address_reviews":
		return c.Facts.AddressReviews
	case "config.address_checks":
		return c.Facts.AddressChecks
	case "config.followups_without_workspace":
		return c.Facts.FollowupsWithoutWorkspace
	case "config.address_without_workspace":
		return c.Facts.AddressWithoutWorkspace
	}
	return nil
}
