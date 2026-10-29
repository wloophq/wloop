package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory/inventorytest"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	"github.com/wloophq/wloop/internal/github"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	ghapi "github.com/wloophq/wloop/plugins/github/api"
	"github.com/wloophq/wloop/plugins/github/explore"
)

// ghSearch is GitHub's search as a test fakes it.
type ghSearch interface {
	SearchCandidates(ctx context.Context, q, viewer string, first int) (dgh.CandidatePage, error)
	SearchCandidatesEach(ctx context.Context, qs []string, viewer string, first int) ([]dgh.CandidatePage, []error, error)
	CandidateByRef(ctx context.Context, ref github.Ref, viewer string) (dgh.CandidatePage, error)
	IsUser(ctx context.Context, login string) (bool, error)
}

// exploreOn serves d's GitHub presenter the GitHub plugin's Explore over
// fe, as the running plugin does: the pull requests d's store holds, the
// viewer "me", recent searches in memory, and d's budget.
func exploreOn(d *Deps, fe ghSearch) *explore.Explorer {
	g := githubTestOf(d)
	x := &explore.Explorer{GitHub: searchOf{fe}, Store: &exploreStore{g: g}, Loop: brand.Name, Budget: func() (bool, ghapi.Limit) {
		if d.Budget == nil || !d.Budget.Low() {
			return true, ghapi.Limit{}
		}
		rl := d.Budget.RateLimit()
		return false, ghapi.Limit{Resource: ghapi.ResourceGraphQL, Remaining: rl.Remaining, Limit: rl.Limit, ResetAt: rl.ResetAt}
	}}
	g.deps.View = func(ctx context.Context, req src.ViewRequest) (json.RawMessage, error) {
		var q struct {
			Query  string `json:"query"`
			Owner  string `json:"owner"`
			Repo   string `json:"repo"`
			Number int    `json:"number"`
		}
		if len(req.Query) > 0 {
			if err := json.Unmarshal(req.Query, &q); err != nil {
				return nil, err
			}
		}
		switch req.View {
		case "explore":
			return marshalOf(x.Search(ctx, q.Query))
		case "explore.recent":
			return marshalOf(x.Recent(ctx))
		case "candidate":
			return marshalOf(fe.CandidateByRef(ctx, github.Ref{Owner: q.Owner, Repo: q.Repo, Number: q.Number}, "me"))
		}
		return nil, fmt.Errorf("%w: no view %s", src.ErrUnsupported, req.View)
	}
	return x
}

func marshalOf[T any](v T, err error) (json.RawMessage, error) {
	if err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

// searchOf is a test's fake search as the plugin's Explore reads GitHub.
type searchOf struct{ fe ghSearch }

func pluginPage(p dgh.CandidatePage) ghapi.CandidatePage {
	var out ghapi.CandidatePage
	raw, _ := json.Marshal(p)
	_ = json.Unmarshal(raw, &out)
	return out
}

func (s searchOf) SearchCandidates(ctx context.Context, q, viewer string, first int) (ghapi.CandidatePage, error) {
	p, err := s.fe.SearchCandidates(ctx, q, viewer, first)
	return pluginPage(p), err
}

func (s searchOf) SearchCandidatesEach(ctx context.Context, qs []string, viewer string, first int) ([]ghapi.CandidatePage, []error, error) {
	ps, errs, err := s.fe.SearchCandidatesEach(ctx, qs, viewer, first)
	out := make([]ghapi.CandidatePage, len(ps))
	for i, p := range ps {
		out[i] = pluginPage(p)
	}
	return out, errs, err
}

func (s searchOf) CandidateByRef(ctx context.Context, ref ghapi.Ref, viewer string) (ghapi.CandidatePage, error) {
	p, err := s.fe.CandidateByRef(ctx, github.Ref(ref), viewer)
	return pluginPage(p), err
}

func (s searchOf) IsUser(ctx context.Context, login string) (bool, error) {
	return s.fe.IsUser(ctx, login)
}

// exploreStore is the plugin's store as a test's Explore reads it: the
// pull requests the host's store holds, and the recent searches in memory.
type exploreStore struct {
	g    *githubTest
	mu   sync.Mutex
	meta map[string]string
}

func (s *exploreStore) Refs(ctx context.Context) ([]ghapi.Ref, error) {
	prs, err := inventorytest.Of(s.g.deps.Store).ListPRs(ctx, false)
	refs := make([]ghapi.Ref, len(prs))
	for i, x := range prs {
		refs[i] = ghapi.Ref{Owner: x.Ref.Owner, Repo: x.Ref.Repo, Number: x.Ref.Number}
	}
	return refs, err
}

func (s *exploreStore) Viewer(context.Context) (string, error) { return "me", nil }

func (s *exploreStore) Meta(_ context.Context, key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.meta[key], nil
}

func (s *exploreStore) SetMeta(_ context.Context, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.meta == nil {
		s.meta = map[string]string{}
	}
	s.meta[key] = value
	return nil
}

// fakeBudget is the GitHub plugin's budget as a test sets it: low below
// half its limit until it resets.
type fakeBudget struct {
	rl  api.RateLimit
	now func() time.Time
}

func (b fakeBudget) live() bool { return b.now().Before(b.rl.ResetAt) }

func (b fakeBudget) Low() bool { return b.live() && b.rl.Remaining*2 < b.rl.Limit }

func (b fakeBudget) RateLimit() api.RateLimit {
	if !b.live() {
		return api.RateLimit{}
	}
	return b.rl
}
