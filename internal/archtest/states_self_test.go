package archtest

import (
	"go/parser"
	"go/token"
	"go/types"
	"testing"
)

const plantedTaskSchema = `package task
type Category string
const (
	CategoryInbox Category = "inbox"
	CategoryDone  Category = "done"
)
var Categories = []Category{CategoryInbox, CategoryDone}
type State string
type Task struct { State State; Category Category }
`

func TestGoNamesNoStateFlagsPlantedStatesAndCategories(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()
	sf, sinfo, tp := planted(t, fset, taskPkg, "schema.go", plantedTaskSchema, nil)
	if got := stateLiteralHits(fset, "internal/domain/task/schema.go", sinfo, sf); len(got) != 0 {
		t.Fatalf("the closed set's declaration is flagged: %q", got)
	}
	if got := stateLiteralHits(fset, "internal/domain/task/other.go", sinfo, sf); len(got) != 2 {
		t.Fatalf("category constants declared outside schema.go: want 2 hits, got %q", got)
	}
	src := `package x
import "github.com/wloophq/wloop/internal/domain/task"
const Next task.State = "next"
func f(e task.Task, s string) bool {
	e.State = task.State("waiting")
	e.Category = "ready"
	switch e.State { case "someday": return true }
	if e.State == "" || e.Category == task.CategoryDone || s == "done" { return false }
	return e.State == task.State(s)
}
`
	f, info, _ := planted(t, fset, module+"internal/x", "x.go", src, map[string]*types.Package{taskPkg: tp})
	hits := stateLiteralHits(fset, "internal/x/x.go", info, f)
	flagged(t, "planted literals", hits, 4, "is a task.")
	for _, want := range []string{`"next" is a task.State`, `"waiting" is a task.State`, `"ready" is a task.Category`, `"someday" is a task.State`} {
		flagged(t, want, filter(hits, want), 1, want)
	}

	sql := `package sqlite
const (
	a = "SELECT id FROM tasks WHERE state = 'next'"
	b = "SELECT id FROM tasks WHERE state NOT IN ('done', 'trash')"
	c = "SELECT id FROM tasks WHERE category = 'done'"
	d = "SELECT id FROM tasks WHERE category NOT IN (?, ?)"
	e = "SELECT 1 FROM items WHERE state = 'open'"
	g = "SELECT 1 FROM turns WHERE state IN ('sent', 'asked')"
)
`
	sqf, err := parser.ParseFile(fset, "sql.go", sql, 0)
	if err != nil {
		t.Fatal(err)
	}
	got := stateSQLHits(fset, "internal/adapter/store/sqlite/sql.go", sqf, []string{"inbox", "next", "done", "trash"})
	flagged(t, "state literals", filter(got, "compares the state column"), 3, "filter by category")
	flagged(t, "inlined categories", filter(got, "inlines a category"), 1, "bind it as ?")
	if len(got) != 4 {
		t.Errorf("want 4 SQL hits, got %q", got)
	}
}
