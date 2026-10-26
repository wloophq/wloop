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
			tags = append(tags, typ.Field(i).Tag.Get("toml"))
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
