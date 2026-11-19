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
			if l.Gates.Drafts.BySession == "" || l.Gates.Drafts.BySessionAbout == "" {
				t.Error("the drafts a session wrote have no words")
			}
			for _, list := range [][]intent.FacetDef{l.Views.Facets, l.Views.DraftFacets} {
				i := slices.IndexFunc(list, func(f intent.FacetDef) bool { return f.ID == "source" })
				for _, id := range []string{"github", "slack"} {
					if i < 0 || list[i].Values[id] == "" {
						t.Errorf("source has no word for %s, which its plugin's layer gives", id)
					}
				}
			}
		})
	}
}

func TestDefaultProfileWordsTheOutboxInPortuguese(t *testing.T) {
	en, problems := loadProfile(t, "en", nil)
	if len(problems) != 0 {
		t.Fatalf("problems %v", problems)
	}
	pt, problems := loadProfile(t, "pt-BR", nil)
	if len(problems) != 0 {
		t.Fatalf("problems %v", problems)
	}
	enReview, _ := en.Registry.Get(dintent.Review)
	ptReview, _ := pt.Registry.Get(dintent.Review)
	for _, c := range []struct{ name, en, pt string }{
		{"review's outbox about", enReview.Outbox.About, ptReview.Outbox.About},
		{"drafts.by_hand", en.Gates.Drafts.ByHand, pt.Gates.Drafts.ByHand},
		{"drafts.by_session", en.Gates.Drafts.BySession, pt.Gates.Drafts.BySession},
	} {
		if c.pt == "" || c.pt == c.en {
			t.Errorf("pt-BR %s is %q, want a translation of %q", c.name, c.pt, c.en)
		}
	}
}

// TestAUserLayerWordsOneSourceValueAndKeepsThePluginsOthers checks that a layer's values of a facet join the
// lower layers' by value, so a plugin layer or the user's words one value without dropping another layer's.
func TestAUserLayerWordsOneSourceValueAndKeepsThePluginsOthers(t *testing.T) {
	l, problems := loadProfile(t, "en", map[string]string{"views.toml": "[facets.source.values]\ngithub = \"Hub\"\n\n[draft_facets.source.values]\nslack = \"Chat\"\n"})
	if len(problems) != 0 {
		t.Fatalf("problems %v", problems)
	}
	words := func(list []intent.FacetDef) map[string]string {
		i := slices.IndexFunc(list, func(f intent.FacetDef) bool { return f.ID == "source" })
		if i < 0 {
			t.Fatal("no source facet")
		}
		return list[i].Values
	}
	if w := words(l.Views.Facets); w["github"] != "Hub" || w["slack"] != "Slack" || w["session"] != "Session" {
		t.Errorf("task source words %v", w)
	}
	if w := words(l.Views.DraftFacets); w["github"] != "GitHub" || w["slack"] != "Chat" {
		t.Errorf("draft source words %v", w)
	}
}
