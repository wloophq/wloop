package inventory

import (
	"cmp"
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/github"
)

var ref = github.Ref{Owner: "acme", Repo: "api", Number: 1}

func open(t *testing.T) *Store {
	t.Helper()
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "nested", "inv.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return New(db)
}

// memPRs are the pull requests a test reports, as the store reads them.
type memPRs struct{ prs map[github.Ref]dinv.PR }

func (m *memPRs) PRs(_ context.Context, refs []github.Ref) (map[github.Ref]dinv.PR, error) {
	out := map[github.Ref]dinv.PR{}
	for _, r := range refs {
		if p, ok := m.prs[r]; ok {
			out[r] = p
		}
	}
	return out, nil
}

func (m *memPRs) List(_ context.Context, open bool) ([]dinv.PR, error) {
	var out []dinv.PR
	for _, p := range m.prs {
		if !open || p.State == "OPEN" {
			out = append(out, p)
		}
	}
	slices.SortFunc(out, func(a, b dinv.PR) int {
		if c := b.UpdatedAt.Compare(a.UpdatedAt); c != 0 {
			return c
		}
		return cmp.Or(strings.Compare(a.Ref.Owner, b.Ref.Owner), strings.Compare(a.Ref.Repo, b.Ref.Repo), b.Ref.Number-a.Ref.Number)
	})
	return out, nil
}

// upsert reports pr to s.
func upsert(t *testing.T, s *Store, pr dinv.PR) {
	t.Helper()
	var m *memPRs
	if cur := s.prs.Load(); cur != nil {
		m, _ = (*cur).(*memPRs)
	}
	if m == nil {
		m = &memPRs{prs: map[github.Ref]dinv.PR{}}
		s.UsePRs(m)
	}
	if pr.URL == "" {
		pr.URL = pr.Ref.URL()
	}
	m.prs[pr.Ref] = pr
}
