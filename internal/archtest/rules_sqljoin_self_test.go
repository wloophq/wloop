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
func query(any, string) {}
`

func TestRSQLJoinFlagsAPluginTableJoinedWithACoreTable(t *testing.T) {
	t.Parallel()
	fset, f := parse(t, joinsPluginAndCore)
	core := []string{"events", "unit_cards", "view_applied", "links"}
	flagged(t, "joins", ruleSQLJoin(fset, map[string]*ast.File{"internal/adapter/store/x/x.go": f}, core), 3, "(R-sqljoin)")
}
