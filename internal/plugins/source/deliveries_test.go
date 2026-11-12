package source_test

import (
	"testing"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/plugins/bundled"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/pkg/plugin/manifest"
)

func bundledPublisher(t *testing.T, id string) psource.Publisher {
	t.Helper()
	srcs, err := bundled.Sources()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range srcs {
		if s.Provider == id {
			return psource.Publisher{ID: id, Source: s}
		}
	}
	t.Fatalf("no bundled %s manifest", id)
	return psource.Publisher{}
}

// TestGitHubDeliversEveryTextDraftAsAComment reads the bundled GitHub
// manifest: every draft but a reaction or an edit is a comment.
func TestGitHubDeliversEveryTextDraftAsAComment(t *testing.T) {
	t.Parallel()
	p := bundledPublisher(t, "github")
	for k, want := range map[draft.Kind]draft.Delivery{draft.Kind("reply"): draft.Delivery("comment"), draft.Kind("nudge"): draft.Delivery("comment"), draft.Kind("close"): draft.Delivery("comment"),
		draft.Kind("ask_owner"): draft.Delivery("comment"), draft.Kind("comment"): draft.Delivery("comment"), draft.Kind("react"): draft.Delivery("reaction"), draft.Kind("edit"): draft.Delivery("edit")} {
		if got := p.Delivers(k); got != want {
			t.Errorf("%s: %s, want %s", k, got, want)
		}
	}
}

func TestSlackDeliversItsKindsAsItsManifestNamesThem(t *testing.T) {
	t.Parallel()
	p := bundledPublisher(t, "slack")
	for k, want := range map[draft.Kind]draft.Delivery{draft.Kind("reply"): draft.Delivery("message"), draft.Kind("nudge"): draft.Delivery("message"), draft.Kind("comment"): draft.Delivery("message"),
		draft.Kind("react"): draft.Delivery("reaction"), draft.Kind("edit"): draft.Delivery("edit"), draft.Kind("delete"): draft.Delivery("delete")} {
		if got := p.Delivers(k); got != want {
			t.Errorf("%s: %s, want %s", k, got, want)
		}
	}
}

func TestAManifestWithNoDeliveriesLeavesADraftsOwn(t *testing.T) {
	t.Parallel()
	p := psource.Publisher{ID: "acme", Source: &manifest.Source{}}
	for _, k := range []draft.Kind{"react", "reply"} {
		if got := p.Delivers(k); got != "" {
			t.Errorf("%s: %s", k, got)
		}
	}
}
