package cli

import (
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/source"
)

// foldCodec is a forge whose person keys compare without case.
type foldCodec struct{}

func (foldCodec) Provider() unit.ProviderID { return "forge" }
func (foldCodec) Valid(string) error        { return nil }
func (foldCodec) Equal(a, b string) bool    { return strings.EqualFold(a, b) }

func TestTheItemsOwedByAPersonCompareKeysThroughTheirCodec(t *testing.T) {
	mine := items.Item{ID: "a", WaitingOn: []items.Person{{Key: "forge:octocat"}}}
	theirs := items.Item{ID: "b", WaitingOn: []items.Person{{Key: "forge:hubot"}}}
	got := owedBy(items.Person{Key: "forge:Octocat"}, []items.Item{mine, theirs}, unit.NewRegistry(foldCodec{}))
	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("owed %v, want only the item waiting on octocat", itemIDs(got))
	}
}

func TestTheDraftStepComparesKeysThroughTheModulesCodecs(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, nil)
	w := &serveWiring{DB: te.db, Cfg: te.cfg, Modules: []source.Module{{ID: "forge", Codec: foldCodec{}}}, Log: t.Logf, Engine: &engineDeps{}}
	te.wireDrafts(w)
	if c := w.Engine.Drafts.Codecs; c == nil || !c.KeysEqual("forge", "Octocat", "octocat") {
		t.Fatalf("codecs %v do not fold the forge's keys", c)
	}
}
