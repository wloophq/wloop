package intent_test

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/turn"
)

// TestRegistryGolden pins what the bundled profile and the default
// configuration decode to: every intent definition, every workflow and
// config.Defaults(), one "path = value" line each, positions left out.
func TestRegistryGolden(t *testing.T) {
	l, err := turn.Bundled()
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, d := range l.Registry.All() {
		lines = append(lines, flatten("intent/"+string(d.Name), reflect.ValueOf(d))...)
	}
	for _, w := range l.Workflows {
		lines = append(lines, flatten("workflow/"+w.Name, reflect.ValueOf(w))...)
	}
	b, err := json.Marshal(config.Defaults())
	if err != nil {
		t.Fatal(err)
	}
	var cfg any
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	lines = append(lines, flattenJSON("config", cfg)...)
	got := strings.Join(lines, "\n") + "\n"

	path := filepath.Join("testdata", "registry.golden")
	if f := flag.Lookup("update"); f != nil && f.Value.String() == "true" {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("the bundled registry changed; run go test ./internal/intent -run TestRegistryGolden -update and review the diff\n%s", firstDiff(string(want), got))
	}
}

var durationType = reflect.TypeFor[time.Duration]()

// flatten writes v's non-zero leaves under prefix. A value with a Source
// method is a compiled predicate and prints its source; Pos fields and
// unexported fields of exported structs are skipped, except in a type that
// has only unexported fields, such as a cron schedule.
func flatten(prefix string, v reflect.Value) []string {
	switch v.Kind() {
	case reflect.Invalid:
		return nil
	case reflect.Interface, reflect.Pointer:
		if v.IsNil() {
			return nil
		}
		if v.CanInterface() {
			if s, ok := v.Interface().(interface{ Source() string }); ok {
				return []string{prefix + " = " + quote(s.Source())}
			}
		}
		return flatten(prefix, v.Elem())
	case reflect.Struct:
		if v.Type().Name() == "Pos" {
			return nil
		}
		if v.CanInterface() {
			if s, ok := v.Interface().(interface{ Source() string }); ok {
				return []string{prefix + " = " + quote(s.Source())}
			}
		}
		exported := false
		for i := range v.NumField() {
			exported = exported || v.Type().Field(i).IsExported()
		}
		var out []string
		for i := range v.NumField() {
			f := v.Type().Field(i)
			if exported && !f.IsExported() {
				continue
			}
			out = append(out, flatten(prefix+"."+f.Name, v.Field(i))...)
		}
		return out
	case reflect.Slice, reflect.Array:
		var out []string
		for i := range v.Len() {
			out = append(out, flatten(fmt.Sprintf("%s[%d]", prefix, i), v.Index(i))...)
		}
		return out
	case reflect.Map:
		keys := v.MapKeys()
		slices.SortFunc(keys, func(a, b reflect.Value) int { return strings.Compare(fmt.Sprint(a), fmt.Sprint(b)) })
		var out []string
		for _, k := range keys {
			out = append(out, flatten(fmt.Sprintf("%s[%v]", prefix, k), v.MapIndex(k))...)
		}
		return out
	}
	if v.IsZero() {
		return nil
	}
	if v.Type() == durationType {
		return []string{prefix + " = " + time.Duration(v.Int()).String()}
	}
	switch v.Kind() {
	case reflect.String:
		return []string{prefix + " = " + quote(v.String())}
	case reflect.Bool:
		return []string{fmt.Sprintf("%s = %t", prefix, v.Bool())}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return []string{fmt.Sprintf("%s = %d", prefix, v.Int())}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return []string{fmt.Sprintf("%s = %d", prefix, v.Uint())}
	case reflect.Float32, reflect.Float64:
		return []string{fmt.Sprintf("%s = %g", prefix, v.Float())}
	}
	return []string{fmt.Sprintf("%s = <%s>", prefix, v.Kind())}
}

func flattenJSON(prefix string, v any) []string {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		var out []string
		for _, k := range keys {
			out = append(out, flattenJSON(prefix+"."+k, x[k])...)
		}
		return out
	case []any:
		var out []string
		for i, e := range x {
			out = append(out, flattenJSON(fmt.Sprintf("%s[%d]", prefix, i), e)...)
		}
		return out
	case nil:
		return nil
	}
	b, _ := json.Marshal(v)
	return []string{prefix + " = " + string(b)}
}

func quote(s string) string { return strconv.Quote(s) }

func firstDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := range max(len(w), len(g)) {
		var a, b string
		if i < len(w) {
			a = w[i]
		}
		if i < len(g) {
			b = g[i]
		}
		if a != b {
			return fmt.Sprintf("line %d:\n- %s\n+ %s", i+1, a, b)
		}
	}
	return ""
}
