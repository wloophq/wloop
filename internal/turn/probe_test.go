package turn

import (
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/brand/brandtest"
)

func TestProbeNamesTheManagersBinary(t *testing.T) {
	for binary, want := range map[string]string{
		"orca": "so never run `gh`, `git` or `orca`, and write no file outside `.loop/`.",
		"zed":  "so never run `gh`, `git` or `zed`, and write no file outside `.loop/`.",
		"":     "so never run `gh` or `git`, and write no file outside `.loop/`.",
	} {
		c := probeContext(nil)
		c.ManagerBinary = binary
		r, err := render(c)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(strings.ReplaceAll(r.Text, "\n", " "), want) {
			t.Errorf("%q: probe lacks %q:\n%s", binary, want, r.Text)
		}
		if binary == "orca" && brandtest.IsDefault() && r.ID != "c0e6c4bae63a" {
			t.Errorf("orca's probe changed: id %s", r.ID)
		}
	}
}
