package brandtest

import (
	"fmt"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/brand"
)

type fatalTB struct {
	testing.TB
	msg string
}

func (f *fatalTB) Helper() {}

func (f *fatalTB) Name() string { return "TestX" }

func (f *fatalTB) Fatalf(format string, args ...any) { f.msg = fmt.Sprintf(format, args...) }

func TestUpdatesNeedTheTestBrand(t *testing.T) {
	saved := brand.Command
	t.Cleanup(func() { brand.Command = saved })

	brand.Command = brand.Name
	f := &fatalTB{TB: t}
	CanUpdate(f)
	if !strings.Contains(f.msg, "print-brand-ldflags") || !strings.Contains(f.msg, "-run TestX -update") {
		t.Errorf("an update under Command == Name went on: %q", f.msg)
	}

	brand.Command = "zz" + brand.Name
	f = &fatalTB{TB: t}
	CanUpdate(f)
	if f.msg != "" {
		t.Errorf("an update under the test brand stopped: %q", f.msg)
	}
}
