package turn

import (
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/template"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/instructions"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/unit"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/profile"
)

// CheckTemplate renders an intent's instructions and headlines against
// sample turns: every declared input set and every one zero, each with and
// without a resume. Each failure is placed at the template's line.
func CheckTemplate(d dintent.Def, t *template.Template) []intentload.Problem {
	var out []intentload.Problem
	seen := map[string]bool{}
	add := func(p intentload.Problem) {
		key := p.File + "\x00" + strconv.Itoa(p.Line) + "\x00" + p.Field + "\x00" + p.Msg
		if !seen[key] {
			seen[key] = true
			out = append(out, p)
		}
	}
	for _, c := range samples(d) {
		if err := t.Execute(io.Discard, c); err != nil {
			add(execProblem(t.Name(), err))
		}
		for i := -1; i < len(d.Headline.New); i++ {
			h := t.Lookup(intentload.HeadlineName(i))
			if h == nil {
				continue
			}
			if err := h.Execute(io.Discard, c); err != nil {
				txt, field := d.Headline.Resumed, "headline.resumed"
				if i >= 0 {
					txt, field = d.Headline.New[i].Text, "headline.new["+strconv.Itoa(i)+"].text"
				}
				p := execProblem(h.Name(), err)
				add(intentload.Problem{File: txt.At.File, Line: txt.At.Line, Col: txt.At.Col, Field: field, Msg: p.Msg})
			}
		}
	}
	return out
}

func samples(d dintent.Def) []Context {
	set, zero := map[string]any{}, map[string]any{}
	for _, in := range d.Inputs {
		switch in.Type {
		case "flag":
			set[in.Name], zero[in.Name] = true, false
		case "list":
			set[in.Name], zero[in.Name] = []string{"sample"}, []string{}
		default:
			set[in.Name], zero[in.Name] = "sample", ""
		}
	}
	resume := &instructions.Resume{
		Question: &instructions.Question{Text: "sample", Recommend: "a", Options: []instructions.Option{{ID: "a", Label: "a", Publishes: true}, {ID: "b", Label: "b"}}},
		Verdict:  "APPROVE", ArtifactKind: "sample", ArtifactBody: "sample", Items: []instructions.Item{{Path: "a.go", Line: 1, Body: "sample"}},
		Notes: "sample", Head: "a1", CurrentHead: "b2", Answer: "sample",
	}
	pr := PR{URL: "https://github.com/acme/api/pull/7", Owner: "acme", Repo: "api", Number: 7, Title: "sample", Author: "sample", State: "OPEN",
		Association: "MEMBER", Head: "a1", Base: "main", Labels: []string{"sample"}, Prior: &PriorReview{Verdict: "APPROVED", Head: "a0"}}
	var payload Payload = &pr
	if p := d.ProviderOrDefault(); p != unit.GitHub {
		payload = samplePayload{p}
	}
	base := Context{Intent: Intent(d.Name), Payload: payload, Unit: payload.Unit(), Wording: payload.Wording(), Def: d, Headline: "sample", Records: true, Guidance: "sample", Agent: "sample", Command: brand.Name,
		Guide: profile.Guide{Parts: []profile.Part{{Label: "sample", Level: 3, Text: "sample"}}}, BriefMissing: noBrief,
		History: []instructions.PastTurn{{Intent: "sample", Trigger: "by-hand", State: "done", Summary: "sample"}}}
	if slices.Contains(d.Dims, "round") {
		base.Round = "first"
	}
	base.Guides = map[string]profile.Guide{}
	for _, a := range d.AlsoProfiles {
		base.Guides[a.Profile] = base.Guide
	}
	if d.Profile != "" {
		base.Guides[d.Profile] = base.Guide
	}
	var out []Context
	for _, inputs := range []map[string]any{set, zero} {
		for _, r := range []*instructions.Resume{nil, resume} {
			c := base
			c.Inputs, c.Resume = inputs, r
			out = append(out, c)
		}
	}
	why := base
	why.Inputs, why.Why = set, &Why{Workflow: "sample", Trigger: "event", Event: "sample", EventData: "sample", Matched: []WhyFact{{Name: "sample", Value: "sample"}}}
	return append(out, why)
}

// samplePayload is a unit of a provider other than github, for checking its intents' templates.
type samplePayload struct{ provider unit.ProviderID }

func (s samplePayload) Unit() unit.Unit { return unit.Unit{Provider: s.provider, Key: "sample"} }
func (s samplePayload) Arg() string     { return string(s.provider) + ":sample" }
func (s samplePayload) Wording() Wording {
	return Wording{Noun: "sample", This: "this sample", Ref: "sample", Short: "sample"}
}
func (samplePayload) Fact(string) (any, bool) { return nil, false }

var execAt = regexp.MustCompile(`^template: ([^:]+):(\d+):(\d+): (.*)$`)

// execProblem places text/template's "template: <name>:<line>:<col>: executing ..." error.
func execProblem(name string, err error) intentload.Problem {
	m := execAt.FindStringSubmatch(err.Error())
	if m == nil {
		return intentload.Problem{File: name, Line: 1, Col: 1, Msg: err.Error()}
	}
	line, _ := strconv.Atoi(m[2])
	col, _ := strconv.Atoi(m[3])
	msg := m[4]
	if i := strings.Index(msg, ": "); strings.HasPrefix(msg, "executing ") && i >= 0 {
		msg = msg[i+2:]
	}
	return intentload.Problem{File: m[1], Line: line, Col: col, Msg: msg}
}
