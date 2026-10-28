package archtest

import (
	"go/token"
	"go/types"
	"testing"
)

const plantedGates = `package gates
type Resolution struct{ Intent string }
type Store interface {
	Resolve(id int64, r Resolution) (int64, error)
}
type Service struct{ s Store }
func (s *Service) Resolve(id int64, r Resolution) error { return nil }
`

func TestOnlyTheGatesServiceResolvesAGateFlagsAPlantedResolve(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()
	_, _, gp := planted(t, fset, gatesPkg, "gates.go", plantedGates, nil)
	src := "package x\n" +
		"import \"github.com/wloophq/wloop/internal/core/gates\"\n" +
		"func f(st gates.Store, svc *gates.Service) {\n" +
		"\t_, _ = st.Resolve(1, gates.Resolution{})\n" +
		"\t_ = svc.Resolve(2, gates.Resolution{})\n" +
		"}\n" +
		"const q = `UPDATE gates SET status = 'approved' WHERE id = ?`\n" +
		"const r = `SELECT status FROM gates`\n"
	f, info, _ := planted(t, fset, module+"internal/x", "x.go", src, map[string]*types.Package{gatesPkg: gp})
	hits := gateResolveHits(fset, "internal/x/x.go", info, f)
	if len(hits) != 2 {
		t.Fatalf("want 2 hits, got %q", hits)
	}
	flagged(t, "store resolve", hits[:1], 1, "resolves a gate on the store")
	flagged(t, "status write", hits[1:], 1, "writes gates.status")
}
