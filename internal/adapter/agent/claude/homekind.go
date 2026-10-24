package claude

import (
	"os"
	"path/filepath"

	"github.com/wloophq/wloop/internal/core/setup"
)

// NewHomeKind is Claude Code's homes, whose installs run claude through sh.
func NewHomeKind(sh ShellFunc) setup.HomeKind { return homeKind{sh: sh} }

type homeKind struct{ sh ShellFunc }

var _ setup.HomeOwner = homeKind{}

func (k homeKind) New(s setup.HomeSpec) setup.AgentHome {
	return Home{Command: s.Command, MCPURL: s.MCPURL, MCPKey: s.MCPKey, DefaultHome: DefaultHome(s.UserHome), Sh: k.sh, Now: s.Now}
}

func (homeKind) Find(s setup.HomeSpec) []string {
	return FindHomes(s.UserHome, s.CallerHome, s.ManagerHomes)
}

func (homeKind) Default(userHome string) string { return DefaultHome(userHome) }

func (homeKind) SkillsRoot(home string) string { return home }

func (homeKind) Owns(dir string) bool {
	for _, f := range []string{".claude.json", "settings.json"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			return true
		}
	}
	return false
}
