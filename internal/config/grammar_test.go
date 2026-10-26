package config

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

// configPaths are the grammar path and kind of every key Config reads.
func configPaths(t reflect.Type, at string, out map[string]builtin.Kind) {
	for i := range t.NumField() {
		f := t.Field(i)
		name := jsonName(f)
		if !f.IsExported() || name == "-" {
			continue
		}
		path := join(at, name)
		ft := f.Type
		for ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		pathOf(ft, path, out)
	}
}

var (
	tierType = reflect.TypeFor[turns.Tier]()
	daysType = reflect.TypeFor[Days]()
)

func join(at, k string) string {
	if at == "" {
		return k
	}
	return at + "." + k
}

func pathOf(ft reflect.Type, path string, out map[string]builtin.Kind) {
	for ft.Kind() == reflect.Pointer {
		ft = ft.Elem()
	}
	switch {
	case ft == daysType || ft == reflect.TypeFor[time.Duration]():
		out[path] = builtin.KindDur
	case ft == tierType:
		out[path] = builtin.KindEnum
	case ft.Kind() == reflect.Struct:
		out[path] = builtin.KindTable
		configPaths(ft, path, out)
	case ft.Kind() == reflect.Map:
		out[path] = builtin.KindMap
		pathOf(ft.Elem(), path+".*", out)
	case ft.Kind() == reflect.Slice && ft.Elem().Kind() == reflect.Struct:
		out[path+"[]"] = builtin.KindTable
		configPaths(ft.Elem(), path+"[]", out)
	case ft.Kind() == reflect.Slice:
		out[path] = builtin.KindList
	case ft.Kind() == reflect.Bool:
		out[path] = builtin.KindBool
	case ft.Kind() == reflect.Int, ft.Kind() == reflect.Int64:
		out[path] = builtin.KindInt
	case ft.Kind() == reflect.Float64:
		out[path] = builtin.KindNumber
	case ft.Kind() == reflect.String:
		out[path] = builtin.KindText
	default:
		panic(fmt.Sprintf("%s: %s", path, ft))
	}
}

func configRows() map[string]builtin.Key {
	out := map[string]builtin.Key{}
	for _, k := range builtin.Grammar {
		if k.In == builtin.SurfaceConfig {
			out[k.Path] = k
		}
	}
	return out
}

// refines says a row of kind row may hold a key the decoder reads as kind
// field: a string holds a predicate or a value of a set.
func refines(row, field builtin.Kind) bool {
	return row == field || field == builtin.KindText && (row == builtin.KindPred || row == builtin.KindEnum)
}

func TestEveryConfigKeyIsInTheGrammar(t *testing.T) {
	t.Parallel()
	want := map[string]builtin.Kind{}
	configPaths(reflect.TypeFor[Config](), "", want)
	rows := configRows()
	for _, p := range slices.Sorted(maps.Keys(want)) {
		r, ok := rows[p]
		switch {
		case !ok:
			t.Errorf("%s has no grammar row", p)
		case !refines(r.Kind, want[p]):
			t.Errorf("%s: the grammar says kind %d, the decoder reads %d", p, r.Kind, want[p])
		case r.Doc == "":
			t.Errorf("%s has no doc", p)
		}
	}
	for _, p := range slices.Sorted(maps.Keys(rows)) {
		if _, ok := want[p]; !ok {
			t.Errorf("the grammar row %s names no key", p)
		}
	}
}

func TestTheGrammarsConfigDefaultsAreDefaults(t *testing.T) {
	t.Parallel()
	b, err := json.Marshal(Defaults())
	if err != nil {
		t.Fatal(err)
	}
	var tree map[string]any
	if err := json.Unmarshal(b, &tree); err != nil {
		t.Fatal(err)
	}
	if ps := builtin.Check(builtin.SurfaceConfig, withoutNulls(tree)); len(ps) > 0 {
		t.Errorf("the defaults do not check: %+v", ps)
	}
	for p, r := range configRows() {
		if r.Default == "" {
			continue
		}
		v, ok := lookup(tree, strings.Split(p, "."))
		if !ok || fmt.Sprint(v) != r.Default {
			t.Errorf("%s: the grammar's default is %q, Defaults() holds %v", p, r.Default, v)
		}
	}
	tiers := make([]string, len(turns.Tiers))
	for i, tier := range turns.Tiers {
		tiers[i] = string(tier)
	}
	var values []string
	for _, v := range builtin.ConfigTiers() {
		values = append(values, v.Name)
	}
	if !slices.Equal(values, tiers) {
		t.Errorf("the grammar's tiers %v, turns.Tiers %v", values, tiers)
	}
}

// withoutNulls is m without the nil lists and maps Defaults leaves, which
// a file leaves out.
func withoutNulls(m map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		switch x := v.(type) {
		case nil:
		case map[string]any:
			out[k] = withoutNulls(x)
		default:
			out[k] = v
		}
	}
	return out
}

func lookup(tree map[string]any, path []string) (any, bool) {
	v, ok := tree[path[0]]
	if !ok || len(path) == 1 {
		return v, ok
	}
	m, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	return lookup(m, path[1:])
}

func TestTheGrammarRefusesTheOldUserConfig(t *testing.T) {
	t.Parallel()
	var tree map[string]any
	if err := json.Unmarshal([]byte(userConfig), &tree); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range builtin.CheckUser(builtin.SurfaceConfig, tree) {
		got = append(got, p.Path)
	}
	slices.Sort(got)
	want := []string{"address_without_workspace", "automated_reviewers", "followups_without_workspace",
		"projects[1].followups_without_workspace", "projects[1].trusted_authors"}
	if !slices.Equal(got, want) {
		t.Errorf("problems at %v, want %v", got, want)
	}
}
