package setup

import (
	"testing"

	"github.com/wloophq/wloop/internal/brand"
)

func TestCommandsArePinned(t *testing.T) {
	for got, want := range map[string]string{
		InstallCommand(brand.Command, "/a b"):     brand.Command + " install --home '/a b'",
		InstallCommand(brand.Command, "/h"):       brand.Command + " install --home /h",
		TrustCommand(brand.Command, "/h", "/r x"): brand.Command + " trust --home /h '/r x'",
		TrustCommand(brand.Command, "", "it's"):   brand.Command + ` trust --home '' 'it'\''s'`,
		TrustCommand("'/bin/p'", "~/h", "/r"):     `'/bin/p' trust --home '~/h' /r`,
	} {
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}
