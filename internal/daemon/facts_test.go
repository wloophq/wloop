package daemon

import (
	"context"
	"slices"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
)

func TestFactsOfAPullRequest(t *testing.T) {
	var asked []github.Ref
	s := newServerWith(t, func(d *Deps) {
		d.Facts = func(_ context.Context, u unit.Unit) (api.Facts, error) {
			ref := refOf(u)
			asked = append(asked, ref)
			if ref.Number == 404 {
				return api.Facts{}, notFound{"acme/api#404 is not on GitHub"}
			}
			return api.Facts{Owner: ref.Owner, Repo: ref.Repo, Author: "ana", Kind: "fix", Tags: []string{"change:fix"}, HasBrief: true}, nil
		}
	})
	var f api.Facts
	if code := get(t, s, api.AdminFacts+"?pr=https://github.com/acme/api/pull/7", &f); code != 200 || f.Author != "ana" || !f.HasBrief || !slices.Equal(f.Tags, []string{"change:fix"}) {
		t.Fatalf("%d %+v", code, f)
	}
	if !slices.Equal(asked, []github.Ref{{Owner: "acme", Repo: "api", Number: 7}}) {
		t.Fatalf("asked %v", asked)
	}
	if code := get(t, s, api.AdminFacts+"?pr=https://github.com/acme/api/pull/404", nil); code != 404 {
		t.Fatalf("missing: %d", code)
	}
	if code := get(t, s, api.AdminFacts+"?pr=acme/api", nil); code != 400 {
		t.Fatalf("bad pr: %d", code)
	}
	none := newServer(t)
	if code := get(t, none, api.AdminFacts+"?pr=https://github.com/acme/api/pull/7", nil); code != 503 {
		t.Fatalf("no facts: %d", code)
	}
}
