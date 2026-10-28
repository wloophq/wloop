package intent

import (
	"io/fs"
	"path"
	"regexp"
	"testing"

	"github.com/wloophq/wloop/profiles"
)

// epochCompare is a time compared for equality with the epoch, which a zero
// time is not: a time fact is set when it is > timestamp(0).
var epochCompare = regexp.MustCompile(`[=!]=\s*timestamp\(\s*0\s*\)|timestamp\(\s*0\s*\)\s*[=!]=`)

func TestNoBundledPredicateComparesATimeWithTheEpoch(t *testing.T) {
	err := fs.WalkDir(profiles.Default, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path.Ext(p) != ".toml" {
			return err
		}
		src, err := fs.ReadFile(profiles.Default, p)
		if err != nil {
			return err
		}
		if m := epochCompare.Find(src); m != nil {
			t.Errorf("%s: %q; test a time set with > timestamp(0)", p, m)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTheEpochCheckFlagsAnEquality(t *testing.T) {
	for _, src := range []string{"task.check_on != timestamp(0)", "timestamp(0) == task.wake_until"} {
		if !epochCompare.MatchString(src) {
			t.Errorf("%s passed", src)
		}
	}
	if epochCompare.MatchString("task.check_on > timestamp(0)") {
		t.Error("the right form was flagged")
	}
}
