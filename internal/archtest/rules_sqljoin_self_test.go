package archtest

import (
	"go/ast"
	"testing"
)

const joinsPluginAndCore = `package x
const prColumns = "pr.title, pr.url"
const fromPR = " FROM pull_requests pr"
const joined = "SELECT " + prColumns + fromPR + " JOIN events e ON e.unit_key = pr.number"
const mutes = ` + "`" + `SELECT pr.title, o.muted_until FROM pull_requests pr LEFT JOIN overrides o ON o.unit_key = pr.number` + "`" + `
const tracked = ` + "`" + `SELECT pr.title, o.tracked FROM pull_requests pr LEFT JOIN overrides o ON o.unit_key = pr.number` + "`" + `
const linksColumn = ` + "`" + `SELECT links FROM slack_messages WHERE ts = ?` + "`" + `
const coreOnly = ` + "`" + `SELECT e.kind FROM events e JOIN unit_cards c ON c.unit_key = e.unit_key` + "`" + `
func f(db any, marks string) {
	query(db, "DELETE FROM view_applied WHERE unit_key IN (SELECT number FROM "+"pull_requests"+" WHERE state = ?)")
	query(db, joined)
	query(db, "SELECT kind FROM events WHERE unit_key IN ("+marks+")")
}
func built(db any) {
	q := "SELECT p.title FROM pull_requests p"
	q += " JOIN unit_marks m ON m.unit_key = p.number"
	query(db, q)
}
func query(any, string) {}
`

const sharesATransaction = `package x
const insertEvent = "INSERT INTO events (kind) VALUES (?)"
func (s *Store) save(ctx any) error {
	return s.db.Write(ctx, func(tx *sqlite.Tx) error {
		tx.ExecContext(ctx, "UPDATE pull_requests SET title = ?")
		return addEvent(ctx, tx)
	})
}
func addEvent(ctx any, tx *sqlite.Tx) error {
	tx.ExecContext(ctx, insertEvent)
	return nil
}
func (s *Store) apart(ctx any) error {
	if err := s.db.Write(ctx, func(tx *sqlite.Tx) error {
		tx.ExecContext(ctx, "UPDATE pull_requests SET title = ?")
		return nil
	}); err != nil {
		return err
	}
	return s.db.Write(ctx, func(tx *sqlite.Tx) error { return addEvent(ctx, tx) })
}
func (s *Store) allowed(ctx any) error {
	return s.db.Write(ctx, func(tx *sqlite.Tx) error {
		tx.ExecContext(ctx, "UPDATE pull_requests SET title = ?")
		return addEvent(ctx, tx)
	})
}
`

func TestRSQLJoinFlagsAPluginTableJoinedWithACoreTable(t *testing.T) {
	t.Parallel()
	fset, f := parse(t, joinsPluginAndCore)
	core := []string{"events", "unit_cards", "view_applied", "links", "unit_marks"}
	flagged(t, "joins", ruleSQLJoin(fset, map[string]*ast.File{"internal/adapter/store/x/x.go": f}, core, nil, map[int]bool{}), 4, "a query names")
}

func TestRSQLJoinFlagsATransactionWritingAPluginTableAndACoreTable(t *testing.T) {
	t.Parallel()
	fset, f := parse(t, sharesATransaction)
	allow := []sqlJoinAllowed{{"internal/adapter/store/x/x.go", "allowed", "planted"}}
	used := map[int]bool{}
	flagged(t, "transactions", ruleSQLJoin(fset, map[string]*ast.File{"internal/adapter/store/x/x.go": f}, []string{"events"}, allow, used), 1, "a transaction of save")
	if !used[0] {
		t.Error("the allowlisted transaction was not matched")
	}
}
