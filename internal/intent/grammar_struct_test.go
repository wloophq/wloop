package intent

import (
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/intent/builtin"
)

// structPaths are the grammar path and kind of every key t decodes.
func structPaths(t reflect.Type, at string, out map[string]builtin.Kind) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	join := func(k string) string {
		if at == "" {
			return k
		}
		return at + "." + k
	}
	switch t.Kind() {
	case reflect.Struct:
		for i := range t.NumField() {
			f := t.Field(i)
			name := f.Tag.Get("toml")
			if name == "" || name == "-" {
				continue
			}
			pathOf(f.Type, join(name), out)
		}
	case reflect.Map:
		pathOf(t.Elem(), join("*"), out)
	}
}

func pathOf(t reflect.Type, path string, out map[string]builtin.Kind) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch {
	case t.Kind() == reflect.Struct:
		out[path] = builtin.KindTable
		structPaths(t, path, out)
	case t.Kind() == reflect.Map:
		out[path] = builtin.KindMap
		pathOf(t.Elem(), path+".*", out)
	case t.Kind() == reflect.Slice && (t.Elem().Kind() == reflect.Struct || t.Elem().Kind() == reflect.Map):
		out[path+"[]"] = builtin.KindTable
		if t.Elem().Kind() == reflect.Map {
			pathOf(t.Elem().Elem(), path+"[].*", out)
			return
		}
		structPaths(t.Elem(), path+"[]", out)
	case t.Kind() == reflect.Slice:
		out[path] = builtin.KindList
	case t.Kind() == reflect.Bool:
		out[path] = builtin.KindBool
	case t.Kind() == reflect.Int:
		out[path] = builtin.KindInt
	case t.Kind() == reflect.Interface:
		out[path] = builtin.KindAny
	default:
		out[path] = builtin.KindText
	}
}

// refines says a row of kind row may hold a key decoded as kind field.
func refines(row, field builtin.Kind) bool {
	if row == field {
		return true
	}
	switch field {
	case builtin.KindText:
		return slices.Contains([]builtin.Kind{builtin.KindPred, builtin.KindEnum, builtin.KindTemplate, builtin.KindFile, builtin.KindDur, builtin.KindFact}, row)
	case builtin.KindAny:
		return true
	case builtin.KindTable:
		return row == builtin.KindAny
	case builtin.KindMap:
		return row == builtin.KindTable
	}
	return false
}

func TestManifestStructsAreTheGrammar(t *testing.T) {
	for surface, typ := range map[builtin.Surface]reflect.Type{
		builtin.SurfaceIntent:   reflect.TypeFor[manifest](),
		builtin.SurfaceWorkflow: reflect.TypeFor[wManifest](),
		builtin.SurfaceActions:  reflect.TypeFor[map[string]mBuiltinAction](),
		builtin.SurfaceGTD:      reflect.TypeFor[mGTD](),
		builtin.SurfaceKeys:     reflect.TypeFor[mKeys](),
		builtin.SurfaceDefaults: reflect.TypeFor[manifestDefaults](),
	} {
		decoded := map[string]builtin.Kind{}
		structPaths(typ, "", decoded)
		rows := map[string]builtin.Key{}
		for _, k := range builtin.Grammar {
			if k.In == surface {
				rows[k.Path] = k
			}
		}
		for _, p := range slices.Sorted(maps.Keys(decoded)) {
			r, ok := rows[p]
			parent, named := strings.CutSuffix(p, ".*")
			switch {
			case !ok && named && rows[parent].Kind == builtin.KindTable:
			case !ok:
				t.Errorf("%s %s: decoded, and no grammar row", surface, p)
			case !refines(r.Kind, decoded[p]):
				t.Errorf("%s %s: the grammar says kind %d, the decoder reads %d", surface, p, r.Kind, decoded[p])
			}
		}
		for _, p := range slices.Sorted(maps.Keys(rows)) {
			if _, ok := decoded[p]; ok {
				continue
			}
			parent := p[:max(strings.LastIndex(p, "."), 0)]
			if _, wild := decoded[parent+".*"]; wild || decoded[parent] == builtin.KindAny {
				continue
			}
			t.Errorf("%s %s: a grammar row no decoder reads", surface, p)
		}
	}
}
