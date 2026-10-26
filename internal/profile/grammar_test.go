package profile

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/intent/builtin"
)

func TestProfileFilesAreTheGrammar(t *testing.T) {
	for surface, typ := range map[builtin.Surface]reflect.Type{
		builtin.SurfaceLabels: reflect.TypeFor[Labels](),
		builtin.SurfaceBlock:  reflect.TypeFor[rawFrontMatter](),
	} {
		var tags, rows []string
		for i := range typ.NumField() {
			f := typ.Field(i)
			tag := f.Tag.Get("toml")
			tags = append(tags, tag)
			if f.Type.Kind() == reflect.Map && !slices.ContainsFunc(builtin.Grammar, func(k builtin.Key) bool { return k.In == surface && k.Path == tag+".*" }) {
				t.Errorf("%s %s: the decoder reads a table by name, and no %s.* row", surface, tag, tag)
			}
		}
		for _, k := range builtin.Grammar {
			if k.In == surface && !strings.Contains(k.Path, ".") {
				rows = append(rows, k.Path)
			}
		}
		slices.Sort(tags)
		slices.Sort(rows)
		if !slices.Equal(tags, rows) {
			t.Errorf("%s: the decoder reads %v, the grammar has %v", surface, tags, rows)
		}
	}
}
