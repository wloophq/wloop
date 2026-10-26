package intent

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/intent/builtin"
)

func TestAnUnknownKeyNamesTheKeyThatReplacedIt(t *testing.T) {
	saved := builtin.Grammar
	t.Cleanup(func() { builtin.Grammar = saved })
	builtin.Grammar = append(slices.Clone(saved), builtin.Key{Path: "label", In: builtin.SurfaceIntent, Kind: builtin.KindText, Was: []string{"title"}})
	if got := unknownKey(reflect.TypeFor[manifest](), []string{"title"}); got != "title was renamed label" {
		t.Errorf("%q", got)
	}
	if got := unknownKey(reflect.TypeFor[manifest](), []string{"lable"}); got != "unknown key; did you mean label?" {
		t.Errorf("%q", got)
	}
	if got := unknownKey(reflect.TypeFor[wManifest](), []string{"title"}); strings.Contains(got, "renamed") {
		t.Errorf("a workflow key was read as an intent's: %q", got)
	}
}

func TestPredicateHintCoversEveryPredKey(t *testing.T) {
	for _, k := range builtin.Grammar {
		if k.Kind != builtin.KindPred || k.In == builtin.SurfaceConfig {
			continue
		}
		segs := strings.Split(k.Path, ".")
		key := segs[len(segs)-1]
		var src string
		for i, s := range segs[:len(segs)-1] {
			name := strings.Join(segs[:i+1], ".")
			if strings.HasSuffix(s, "[]") {
				src += "[[" + strings.ReplaceAll(name, "[]", "") + "]]\n"
			}
		}
		if parent := strings.ReplaceAll(strings.Join(segs[:len(segs)-1], "."), "[]", ""); parent != "" && !strings.HasSuffix(segs[len(segs)-2], "[]") {
			src += "[" + parent + "]\n"
		}
		src += key + " = !pr.mine\n"
		_, msg := syntaxError([]byte(src), strings.Count(src, "\n"), "invalid character")
		if !strings.Contains(msg, "a predicate is a string: quote it") {
			t.Errorf("%s %s: %q\n%s", k.In, k.Path, msg, src)
		}
	}
}
