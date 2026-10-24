package turns

import (
	"testing"

	"github.com/wloophq/wloop/internal/brand"
)

func TestUntrustedErrorNamesWhatToTrust(t *testing.T) {
	for name, c := range map[string]struct {
		err  UntrustedError
		want string
	}{
		"a worktree of a repository": {
			UntrustedError{Folder: "/w/rev", Home: "/h", Root: "/src/api", Fix: brand.Command + " trust --home /h /src/api"},
			"untrusted folder blocks the agent: /w/rev is not trusted in the agent home /h; to trust /src/api and its worktrees, run: " + brand.Command + " trust --home /h /src/api",
		},
		"a folder that is its own root": {
			UntrustedError{Folder: "/src/api", Home: "/h", Root: "/src/api", Fix: brand.Command + " trust --home /h /src/api"},
			"untrusted folder blocks the agent: /src/api is not trusted in the agent home /h; to trust it, run: " + brand.Command + " trust --home /h /src/api",
		},
		"no root known": {
			UntrustedError{Folder: "/w/rev", Home: "/h", Fix: brand.Command + " trust --home /h /w/rev"},
			"untrusted folder blocks the agent: /w/rev is not trusted in the agent home /h; to trust it, run: " + brand.Command + " trust --home /h /w/rev",
		},
		"a dialog in a terminal, no fix": {
			UntrustedError{Folder: "/w/rev", Home: "/h", Root: "/src/api", Terminal: "term_1"},
			"untrusted folder blocks the agent: /w/rev is not trusted in the agent home /h, so its terminal term_1 waits on the trust dialog",
		},
		"the terminal's own home": {
			UntrustedError{Folder: "/w/rev", Root: "/src/api", Terminal: "term_1"},
			"untrusted folder blocks the agent: /w/rev is not trusted in the agent home of its terminal (set agent_home to have " + brand.Name + " name it), so its terminal term_1 waits on the trust dialog",
		},
	} {
		if got := c.err.Error(); got != c.want {
			t.Errorf("%s:\n got %s\nwant %s", name, got, c.want)
		}
	}
}
