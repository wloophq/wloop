package cli

import (
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func TestTheOpenHelpListsEveryFormOfItsArgument(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	help := strings.Join(strings.Fields(helpOut(t, te, "open", "--help")), " ")
	for _, want := range []string{"#<route>", "<cockpit-url>", brand.URLScheme() + "://open/#<route>", te.command() + " open '#queue=answer'", "window when the app runs, else in the browser"} {
		if !strings.Contains(help, want) {
			t.Errorf("the open help lacks %q:\n%s", want, help)
		}
	}
}
