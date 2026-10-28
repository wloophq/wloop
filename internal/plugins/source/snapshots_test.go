package source_test

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/domain/unit"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// fill sets every field of v, a pointer, to a value derived from its path,
// so a field the encoding drops or renames shows in the golden.
func fill(v reflect.Value, path string, n *int) {
	*n++
	switch v.Kind() {
	case reflect.Pointer:
		v.Set(reflect.New(v.Type().Elem()))
		fill(v.Elem(), path, n)
	case reflect.Struct:
		if v.Type() == reflect.TypeFor[time.Time]() {
			v.Set(reflect.ValueOf(time.Date(2026, 10, 28, 9, 0, *n, 0, time.UTC)))
			return
		}
		for i := range v.NumField() {
			fill(v.Field(i), path+"."+v.Type().Field(i).Name, n)
		}
	case reflect.Slice:
		if v.Type() == reflect.TypeFor[json.RawMessage]() {
			v.SetBytes([]byte(fmt.Sprintf(`{"n":%d}`, *n)))
			return
		}
		s := reflect.MakeSlice(v.Type(), 1, 1)
		fill(s.Index(0), path+"[0]", n)
		v.Set(s)
	case reflect.Map:
		m := reflect.MakeMap(v.Type())
		k, e := reflect.New(v.Type().Key()).Elem(), reflect.New(v.Type().Elem()).Elem()
		fill(k, path+".key", n)
		fill(e, path+".value", n)
		m.SetMapIndex(k, e)
		v.Set(m)
	case reflect.String:
		v.SetString(path)
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Int, reflect.Int64:
		v.SetInt(int64(*n))
	case reflect.Uint64:
		v.SetUint(uint64(*n))
	default:
		panic(fmt.Sprintf("fill: %s is %s", path, v.Kind()))
	}
}

func TestTheStoredSnapshotShapeIsPinned(t *testing.T) {
	ctx := context.Background()
	var snap src.Snapshot
	n := 0
	fill(reflect.ValueOf(&snap).Elem(), "snapshot", &n)
	snap.Gone = false
	u := unit.Unit{Provider: "acme", Key: "one"}
	db, err := sqlite.Open(filepath.Join(t.TempDir(), "store.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	snaps := psource.NewSnapshots(db.Snapshots())
	if err := snaps.Put(ctx, map[unit.Unit]src.Snapshot{u: snap}); err != nil {
		t.Fatal(err)
	}
	stored, err := db.Snapshots().Get(ctx, []unit.Unit{u})
	if err != nil {
		t.Fatal(err)
	}
	var got bytes.Buffer
	if err := json.Indent(&got, stored[u].JSON, "", "  "); err != nil {
		t.Fatal(err)
	}
	got.WriteByte('\n')
	path := filepath.Join("testdata", "snapshot.golden")
	if *update {
		if err := os.WriteFile(path, got.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Errorf("the stored snapshot changed shape; a field renamed in the SDK loses it in every stored row:\n%s", got.Bytes())
	}
	back, ok, err := snaps.Last(ctx, u)
	if err != nil || !ok || !reflect.DeepEqual(back, snap) {
		t.Errorf("the round trip: %t %v\n got %+v\nwant %+v", ok, err, back, snap)
	}
}
