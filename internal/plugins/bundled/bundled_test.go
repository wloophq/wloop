package bundled

import (
	"reflect"
	"testing"

	"github.com/wloophq/wloop/pkg/plugin/manifest"
)

func TestSourcesAreTheCallersToChange(t *testing.T) {
	first, err := Sources()
	if err != nil || len(first) == 0 {
		t.Fatalf("sources: %v, %v", first, err)
	}
	want := deepCopy(first[0])
	s := first[0]
	s.Name = "changed"
	s.Codec.Keys[0] = "changed"
	s.Codec.Fields["changed"] = "x"
	s.Facts[0].Enum = append(s.Facts[0].Enum, "changed")
	s.Tools = append(s.Tools, manifest.Tool{})
	again, err := Sources()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(again[0], want) {
		t.Error("a caller's change to its source reached the next caller")
	}
}
