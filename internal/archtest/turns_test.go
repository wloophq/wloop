package archtest

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"slices"
	"strings"
	"testing"
)

const (
	turnDomainPkg = module + "internal/domain/turn"
	turnstestPkg  = module + "internal/core/turns/turnstest"
)

// turnRecorders are the files that may record a turn: the turn service and
// its test doubles, the stores that implement its port with their test
// seeds, and the domain's book.
var turnRecorders = []string{
	"internal/core/turns/",
	"internal/adapter/store/",
	"internal/domain/turn/",
}

// bookRecords are the turn.Book methods that put a turn on the book.
var bookRecords = []string{"Send", "SendOn", "Start"}

// TestOnlyTurnsRecordsATurn checks that outside `internal/core/turns`, the
// store adapters (`storetest` among them) and the domain's turn book, no
// code calls Send, SendOn or Start on a turn.Book, or a Save that takes a
// turn.Book: a turn is recorded only by the turn service's sinks, which take
// the Turn `turns.Starter` admitted.
func TestOnlyTurnsRecordsATurn(t *testing.T) {
	t.Parallel()
	fset, files := typedFiles(t)
	var hits []string
	for _, tf := range files {
		if slices.ContainsFunc(turnRecorders, func(p string) bool { return strings.HasPrefix(tf.rel, p) }) {
			continue
		}
		hits = append(hits, turnRecordHits(fset, tf.rel, tf.info, tf.file)...)
	}
	slices.Sort(hits)
	for _, h := range hits {
		t.Error(h)
	}
}

func turnRecordHits(fset *token.FileSet, rel string, info *types.Info, f *ast.File) []string {
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if ok && recordsATurn(info, sel) {
			out = append(out, fmt.Sprintf("%s:%d: records a turn with %s; record it through turns.Service, from a turn turns.Starter admitted", rel, fset.Position(sel.Pos()).Line, sel.Sel.Name))
		}
		return true
	})
	return out
}

// recordsATurn says whether sel is a turn.Book method that records a turn,
// or a Save that takes a turn.Book.
func recordsATurn(info *types.Info, sel *ast.SelectorExpr) bool {
	s, ok := info.Selections[sel]
	if !ok {
		return false
	}
	fn, ok := s.Obj().(*types.Func)
	if !ok {
		return false
	}
	if slices.Contains(bookRecords, fn.Name()) && isBook(s.Recv()) {
		return true
	}
	if fn.Name() != "Save" {
		return false
	}
	params := fn.Signature().Params()
	for i := range params.Len() {
		if isBook(params.At(i).Type()) {
			return true
		}
	}
	return false
}

func isBook(t types.Type) bool {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	n, ok := types.Unalias(t).(*types.Named)
	return ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == turnDomainPkg && n.Obj().Name() == "Book"
}

// ruleTurnstest flags a non-test import of turnstest.
func ruleTurnstest(self string, imports []string) []string {
	if slices.Contains(imports, turnstestPkg) {
		return []string{fmt.Sprintf("%s imports %s outside its tests; its Started admits a turn no entry asked for", self, turnstestPkg)}
	}
	return nil
}

// TestTurnstestOnlyInTests checks that only `_test.go` files import
// `internal/core/turns/turnstest`, whose Started admits a turn without
// going through an entry's `turns.Starter`.
func TestTurnstestOnlyInTests(t *testing.T) {
	t.Parallel()
	for _, p := range packages(t) {
		self, ok := local(p.ImportPath)
		if !ok {
			continue
		}
		for _, b := range ruleTurnstest(self, p.Imports) {
			t.Error(b)
		}
	}
}
