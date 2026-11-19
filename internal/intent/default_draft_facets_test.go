package intent_test

import (
	"slices"
	"testing"

	"github.com/pelletier/go-toml/v2"

	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/plugins"
)

// bundledDraftKinds is the draft kinds each bundled plugin's manifest declares, by plugin id.
func bundledDraftKinds(t *testing.T) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, id := range []string{"github", "slack"} {
		raw, err := plugins.Manifests.ReadFile(id + "/plugin.toml")
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Source struct {
				DraftKinds []struct {
					Name string `toml:"name"`
				} `toml:"draft_kinds"`
			} `toml:"source"`
		}
		if err := toml.Unmarshal(raw, &doc); err != nil {
			t.Fatal(err)
		}
		for _, k := range doc.Source.DraftKinds {
			out[id] = append(out[id], k.Name)
		}
		if len(out[id]) == 0 {
			t.Fatalf("%s declares no draft kinds", id)
		}
	}
	return out
}

// TestDefaultProfileDraftFacets loads the embedded default with its plugin layers: the draft facets, the
// Outbox's grouping, a word for every draft kind and rule a plugin ships, and a plural head for each intent
// that proposes drafts, in both locales.
func TestDefaultProfileDraftFacets(t *testing.T) {
	for _, locale := range []string{"en", "pt-BR"} {
		t.Run(locale, func(t *testing.T) {
			l, problems := loadProfile(t, locale, nil)
			if len(problems) != 0 {
				t.Fatalf("problems %v", problems)
			}
			var ids []string
			for _, f := range l.Views.DraftFacets {
				ids = append(ids, f.ID)
				if f.Label == "" {
					t.Errorf("draft facet %s has no label", f.ID)
				}
			}
			want := []string{"proposer", "workflow", "kind", "source", "where", "people", "state", "age"}
			if !slices.Equal(ids, want) {
				t.Errorf("draft facets %v, want %v", ids, want)
			}
			byID := map[string]intent.FacetDef{}
			for _, f := range l.Views.DraftFacets {
				byID[f.ID] = f
			}
			if f := byID["source"]; f.Fact != "draft.provider" {
				t.Errorf("source reads %q", f.Fact)
			}
			if f := byID["people"]; f.Match != "team" {
				t.Errorf("people matches %q", f.Match)
			}
			if f := byID["state"]; !slices.Equal(f.Rank, []string{"failed", "sending", "queued", "held"}) {
				t.Errorf("state rank %v", f.Rank)
			}
			if f := byID["age"]; f.Fact != "draft.created_at" || f.Sort != "time" {
				t.Errorf("age %+v", f)
			}
			var outbox *intent.ViewDef
			for i := range l.Views.Views {
				if l.Views.Views[i].ID == "outbox" {
					outbox = &l.Views.Views[i]
				}
			}
			if outbox == nil || outbox.Group != "draft.proposer" || len(outbox.Sort) != 1 || outbox.Sort[0].Fact != "draft.created_at" {
				t.Errorf("outbox view %+v", outbox)
			}

			for id, kinds := range bundledDraftKinds(t) {
				for _, k := range kinds {
					if l.UI[id].Drafts[k].Label == "" {
						t.Errorf("%s draft kind %q has no label", id, k)
					}
				}
			}
			if l.Gates.Drafts.Rules["github-nudge"] == "" {
				t.Error("rule github-nudge has no word")
			}
			for _, name := range []string{"review", "address", "nudge", "act", "start"} {
				d, ok := l.Registry.Get(dintent.Name(name))
				if !ok || d.Outbox.Title == "" {
					t.Errorf("intent %s has no outbox title", name)
				}
			}
		})
	}
}
