package store

import (
	"context"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/plugins/slack/unit"
)

var noteAt = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)

// noteEnv is one store under test; seeds leave what calls need in it.
type noteEnv struct{ db *DB }

// writeCase pins what one write method, named Type.Method, tells the
// listeners: seed runs first and is left out; call tells them once, of
// units, or never when touches is false.
type writeCase struct {
	method  string
	name    string
	seed    func(t *testing.T, e *noteEnv)
	call    func(ctx context.Context, e *noteEnv) error
	touches bool
	units   []unit.Unit
}

// writes is every exported write method of the store's types.
var writes []writeCase

// reads are the exported methods of the store's types that tell nothing:
// reads, and the handle's own plumbing.
var reads = []string{"DB.Close", "DB.OnChange", "DB.R", "DB.Read", "DB.Write"}

type hotPlan struct {
	name, query string
	args        int
	want        []string
	// tempSort is the one sort the statement keeps.
	tempSort string
}

var hotPlans []hotPlan

func openTemp(t *testing.T) (*DB, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, dir
}

func TestEveryWriteTells(t *testing.T) {
	t.Parallel()
	for _, c := range writes {
		t.Run(c.method+c.name, func(t *testing.T) {
			t.Parallel()
			db, _ := openTemp(t)
			e := &noteEnv{db: db}
			if c.seed != nil {
				c.seed(t, e)
			}
			var heard []Change
			db.OnChange(func(ch Change) { heard = append(heard, ch) })
			if err := c.call(context.Background(), e); err != nil {
				t.Fatal(err)
			}
			want := 0
			if c.touches {
				want = 1
			}
			if len(heard) != want {
				t.Fatalf("told %d times, want %d: %+v", len(heard), want, heard)
			}
			if want == 0 {
				return
			}
			units := slices.Clone(c.units)
			slices.SortFunc(units, unit.Compare)
			if got := heard[0]; got.All != (len(units) == 0) || !slices.Equal(got.Units, units) {
				t.Errorf("told %+v, want units %v", got, units)
			}
		})
	}
}

func TestEveryExportedMethodIsReadOrWrite(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	known := map[string]bool{}
	for _, m := range reads {
		known[m] = true
	}
	for _, c := range writes {
		known[c.method] = true
	}
	for _, v := range []any{db, db.Slack(), db.Segments(), db.Extraction()} {
		typ := reflect.TypeOf(v)
		name := typ.Elem().Name()
		for i := range typ.NumMethod() {
			if m := name + "." + typ.Method(i).Name; !known[m] {
				t.Errorf("%s is neither a read nor a write case", m)
			}
		}
	}
}

func queryPlan(t *testing.T, db *DB, q string, args ...any) string {
	t.Helper()
	rows, err := db.R().QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+q, args...)
	if err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, detail)
	}
	return strings.Join(lines, "\n")
}

var tempBTree = regexp.MustCompile(`USE TEMP B-TREE[^\n]*`)

func TestHotPlans(t *testing.T) {
	t.Parallel()
	db, _ := openTemp(t)
	tables := Tables()
	for _, p := range hotPlans {
		t.Run(p.name, func(t *testing.T) {
			args := make([]any, p.args)
			for i := range args {
				args[i] = "x"
			}
			plan := queryPlan(t, db, p.query, args...)
			for _, w := range p.want {
				if !strings.Contains(plan, w) {
					t.Errorf("plan lacks %q:\n%s", w, plan)
				}
			}
			for _, line := range strings.Split(plan, "\n") {
				target, ok := strings.CutPrefix(line, "SCAN ")
				if ok && !strings.Contains(line, "USING") && slices.Contains(tables, strings.Fields(target)[0]) {
					t.Errorf("full scan %q:\n%s", line, plan)
				}
			}
			for _, sort := range tempBTree.FindAllString(plan, -1) {
				if sort != p.tempSort {
					t.Errorf("unexpected %q:\n%s", sort, plan)
				}
			}
			if p.tempSort != "" && !strings.Contains(plan, p.tempSort) {
				t.Errorf("the allowed sort %q is gone:\n%s", p.tempSort, plan)
			}
		})
	}
}
