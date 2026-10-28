package slack

import (
	"context"
	"github.com/wloophq/wloop/plugins/slack/store"
	"testing"

	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/slack/config"
	dslack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/unit"
)

// places are the plugin's placement under the host's rules.
func (f *fixture) places(rules ...config.PlacementRule) Places {
	f.p.all.Placement = rules
	return f.p.places()
}

func (f *fixture) hint(t *testing.T, u unit.Unit, hints map[string]store.ExtractionHint) {
	t.Helper()
	if err := f.db.Extraction().Save(context.Background(), store.ExtractionSave{Hints: map[unit.Unit]map[string]store.ExtractionHint{u: hints}}); err != nil {
		t.Fatal(err)
	}
}

func TestPlaceDefaultAndSuggestion(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.seedReadThread(t)
	u := dslack.ThreadUnit(team, readConv, readParent)
	f.hint(t, u, map[string]store.ExtractionHint{"e1": {Mode: "session", Context: "github:acme/web*", Why: "the web app"}})
	byID := config.PlacementRule{Match: "slack:" + u.Key, Repo: "acme/byid", Checkout: "main"}
	hinted := config.PlacementRule{Match: "github:acme/web*", Repo: "acme/web", Role: "work"}
	channel := config.PlacementRule{Match: "slack:#gen*", Repo: "acme/channel", Role: "work"}
	ctx := context.Background()
	for _, step := range []struct {
		name               string
		rules              []config.PlacementRule
		suggested, byRules string
	}{
		{"hint and channel", []config.PlacementRule{hinted, byID, channel}, "acme/web", "acme/channel"},
		{"channel rule", []config.PlacementRule{byID, channel}, "", "acme/channel"},
		{"no rule", nil, "", ""},
	} {
		got, err := f.places(step.rules...).Placement(ctx, u, "")
		if err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		if repo := placed(got.Suggested); repo != step.suggested {
			t.Errorf("%s: suggested %q, want %q", step.name, repo, step.suggested)
		}
		if repo := placed(got.Default); repo != step.byRules {
			t.Errorf("%s: default %q, want %q", step.name, repo, step.byRules)
		}
	}
}

func placed(p *src.Placement) string {
	if p == nil {
		return ""
	}
	return p.Repo
}

func TestPlaceHintByItem(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	f.seedReadThread(t)
	u := dslack.ThreadUnit(team, readConv, readParent)
	f.hint(t, u, map[string]store.ExtractionHint{
		"e1": {Mode: "session", Context: "slack:#api"}, "e2": {Mode: "session", Context: "slack:#web"}, "e3": {Mode: "none"}})
	rules := []config.PlacementRule{{Match: "slack:#api", Repo: "acme/api"}, {Match: "slack:#web", Repo: "acme/web"}}
	pl := f.places(rules...)
	ctx := context.Background()
	for item, repo := range map[string]string{"e1": "acme/api", "e2": "acme/web"} {
		if got, ok, err := pl.Suggested(ctx, u, item); err != nil || !ok || got.Repo != repo {
			t.Errorf("%s: %+v %t %v", item, got, ok, err)
		}
	}
	for _, item := range []string{"", "e3", "e9"} {
		if got, ok, err := pl.Suggested(ctx, u, item); err != nil || ok {
			t.Errorf("%q: %+v %t %v", item, got, ok, err)
		}
	}
	if got, ok, err := f.places(rules[1:]...).Suggested(ctx, u, "e1"); err != nil || ok {
		t.Errorf("a context the config dropped: %+v %t %v", got, ok, err)
	}
}

func TestPlaceDefaultSkipsDirectMessages(t *testing.T) {
	f := newFixture(t)
	f.login(t)
	const dm = "D0000000001"
	f.seed(t, dslack.Facts{Conversations: []dslack.Conversation{{ID: dm, Kind: dslack.KindIM, DMUser: other}}})
	pl := f.places(config.PlacementRule{Match: "slack:#*", Repo: "acme/any"})
	ctx := context.Background()
	for _, u := range []unit.Unit{
		dslack.Key{Team: team, Conversation: dm, Segment: "s1"}.Unit(),
		dslack.ThreadUnit(team, "C0000000009", readParent),
	} {
		if got, ok, err := pl.Default(ctx, u, ""); err != nil || ok {
			t.Errorf("%s: %+v %t %v", u, got, ok, err)
		}
	}
}
