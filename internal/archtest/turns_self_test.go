package archtest

import (
	"go/token"
	"go/types"
	"testing"
)

const plantedTurnDomain = `package turn
type Turn struct{ ID string }
type Book struct{ Turns []Turn }
func (b *Book) Send(t Turn) {}
func (b *Book) SendOn(t Turn, handle string) {}
func (b *Book) Start(id, handle string) error { return nil }
func (b *Book) ByID(id string) *Turn { return nil }
type Store interface {
	Save(b Book) (Book, error)
	Books() ([]Book, error)
}
`

func TestOnlyTurnsRecordsATurnFlagsAPlantedRecord(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()
	_, _, tp := planted(t, fset, turnDomainPkg, "turn.go", plantedTurnDomain, nil)
	src := "package x\n" +
		"import \"github.com/wloophq/wloop/internal/domain/turn\"\n" +
		"func f(b *turn.Book, st turn.Store) {\n" +
		"\tb.Send(turn.Turn{})\n" +
		"\tb.SendOn(turn.Turn{}, \"h\")\n" +
		"\t_ = b.Start(\"a\", \"h\")\n" +
		"\t_, _ = st.Save(*b)\n" +
		"\t_ = b.ByID(\"a\")\n" +
		"\t_, _ = st.Books()\n" +
		"}\n"
	f, info, _ := planted(t, fset, module+"internal/x", "x.go", src, map[string]*types.Package{turnDomainPkg: tp})
	flagged(t, "planted records", turnRecordHits(fset, "internal/x/x.go", info, f), 4, "records a turn")
}

func TestTurnstestOnlyInTestsFlagsAPlantedImport(t *testing.T) {
	t.Parallel()
	imports := []string{"context", module + "internal/core/turns", turnstestPkg}
	flagged(t, "a non-test import", ruleTurnstest("internal/cli", imports), 1, "turnstest")
	flagged(t, "no turnstest", ruleTurnstest("internal/cli", imports[:2]), 0, "")
}
