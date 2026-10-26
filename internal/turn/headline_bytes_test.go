package turn

import (
	"bytes"
	"testing"
	"text/template"

	"github.com/wloophq/wloop/internal/brand"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/profile"
)

// before are the bundled headlines as they read when headlines executed
// over the turn context: {{.PR.Slug}}, {{.Wording.Ref}} and {{.Who}}.
var before = map[dintent.Name][]string{
	"act":     {"act on {{.Wording.Ref}}", "Act on the open items of {{.Wording.Ref}}, {{.Who}}."},
	"address": {"address round on {{.PR.Slug}}", "Address round on {{.PR.Slug}}, which someone else opened: ask before taking it over.", "Take over {{.PR.Slug}} and address it, {{.Who}}.", "Address round on {{.PR.Slug}}, {{.Who}}."},
	"nudge":   {"nudge on {{.PR.Slug}}", "Nudge on {{.PR.Slug}}, {{.Who}}."},
	"probe":   {"{{name}} probe {{.PR.Slug}}", "{{name}} probe {{.PR.Slug}}, {{.Who}}."},
	"review":  {"review of {{.PR.Slug}}", "Dry-run review of {{.PR.Slug}}: trying a profile, nothing is published.", "Follow-up review of {{.PR.Slug}}, {{.Who}}.", "First review of {{.PR.Slug}}, {{.Who}}."},
}

type oldRoot struct {
	PR      struct{ Slug string }
	Wording struct{ Ref string }
	Who     string
}

func TestHeadlinesRenderTheSameBytes(t *testing.T) {
	l := bundled(t)
	for name, srcs := range before {
		for _, trig := range []Trigger{ByHand, Automated} {
			c := completed(t, Context{Intent: Intent(name), Trigger: trig, Payload: pr(), Owned: true})
			var old oldRoot
			old.PR.Slug, old.Wording.Ref, old.Who = "acme/api#7", c.Wording.Ref, "asked for by you"
			if trig == Automated {
				old.Who = "started by " + brand.Name
			}
			for i, src := range srcs {
				want := execute(t, template.Must(template.New("old").Funcs(funcs).Parse(src)), old)
				h := l.Templates[name].Lookup(intentload.HeadlineName(i - 1))
				if h == nil {
					t.Fatalf("%s: no %s", name, intentload.HeadlineName(i-1))
				}
				if got := execute(t, h, c.facts()); got != want {
					t.Errorf("%s %s %s: %q, want %q", name, trig, intentload.HeadlineName(i-1), got, want)
				}
			}
		}
	}
}

func execute(t *testing.T, tm *template.Template, data any) string {
	t.Helper()
	var b bytes.Buffer
	if err := tm.Execute(&b, data); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// TestRepoBlocksStillSelect: the dimension repo reads the repository's
// name, as a repo=<name>/ block path writes it, never owner/repo.
func TestRepoBlocksStillSelect(t *testing.T) {
	l := bundled(t)
	c := completed(t, Context{Intent: Intent(dintent.Review), Trigger: Automated, Payload: pr()})
	f := c.dimFacts(profile.Vocabulary(l.Registry), map[string]string{dintent.DimStage: c.Round})
	if f.Values["pr.repo_name"] != "api" || f.Values["pr.owner"] != "acme" || f.Values["turn.stage"] != "first" {
		t.Fatalf("%v", f.Values)
	}
}
