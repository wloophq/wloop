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
	for k, want := range map[draft.Kind]draft.Delivery{draft.Reply: draft.CommentDelivery, draft.Nudge: draft.CommentDelivery, draft.Close: draft.CommentDelivery,
		draft.AskOwner: draft.CommentDelivery, draft.Comment: draft.CommentDelivery, draft.React: draft.Reaction, draft.Edit: draft.EditDelivery} {
		if got := p.Delivers(k); got != want {
			t.Errorf("%s: %s, want %s", k, got, want)
		}
	}
}

func TestSlackDeliversItsKindsAsItsManifestNamesThem(t *testing.T) {
	t.Parallel()
	p := bundledPublisher(t, "slack")
	for k, want := range map[draft.Kind]draft.Delivery{draft.Reply: draft.Message, draft.Nudge: draft.Message, draft.Comment: draft.Message,
		draft.React: draft.Reaction, draft.Edit: draft.EditDelivery} {
		if got := p.Delivers(k); got != want {
			t.Errorf("%s: %s, want %s", k, got, want)
		}
	}
}

func TestAManifestWithNoDeliveriesPostsAKindAsItsShape(t *testing.T) {
	t.Parallel()
	p := psource.Publisher{ID: "acme", Source: &manifest.Source{}}
	if got := p.Delivers(draft.React); got != draft.Reaction {
		t.Fatalf("react: %s", got)
	}
	if got := p.Delivers(draft.Reply); got != draft.Message {
		t.Fatalf("reply: %s", got)
	}
}
