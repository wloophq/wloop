package claude

// DisplayName names the agent for a person.
func (Agent) DisplayName() string { return "Claude Code" }

// InstallCommand installs the agent's command line.
func (Agent) InstallCommand() string { return "npm install -g @anthropic-ai/claude-code" }
