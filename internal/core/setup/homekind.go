package setup

import "time"

// HomeSpec is what an agent's home installer needs from this process.
type HomeSpec struct {
	Command, MCPURL, UserHome string
	// MCPKey is the secret the MCP endpoint requires, "" before install writes one.
	MCPKey string
	// CallerHome is the home of the session running the loop, "" when unset.
	CallerHome string
	// SessionVars are the variables the enabled workspace managers set, in a
	// session's agent process, to the handle of its process; one manager's
	// sessions leave every other's unset.
	SessionVars []string
	// ManagerHomes are the homes of this kind the workspace manager keeps.
	ManagerHomes []string
	Now          func() time.Time
}

// HomeKind is one agent's configuration homes.
type HomeKind interface {
	New(s HomeSpec) AgentHome
	// Find lists the homes of this kind on this machine.
	Find(s HomeSpec) []string
	// Default is the home the agent reads with no override.
	Default(userHome string) string
	// SkillsRoot is the directory whose skills/<name>/SKILL.md the agent reads in home, "" for none.
	SkillsRoot(home string) string
}

// HomeOwner is a HomeKind that can tell its homes from another kind's.
type HomeOwner interface {
	Owns(dir string) bool
}

// SessionValue is the value, in the loop's MCP registration, that the agent
// expands to the handle its manager gave the session: the first of vars that
// is set, as ${A:-${B}}.
func SessionValue(vars []string) string {
	if len(vars) == 0 {
		return ""
	}
	if len(vars) == 1 {
		return "${" + vars[0] + "}"
	}
	return "${" + vars[0] + ":-" + SessionValue(vars[1:]) + "}"
}
