package ghsource

// The commands plugin.toml declares, which the host runs through the
// GitHub plugin.
const (
	// CommandClone clones a repository: clone <owner>/<name> <path>.
	CommandClone = "clone"
	// CommandAuth checks that gh is installed and logged in; it exits
	// ExitNoGH when gh is not installed.
	CommandAuth = "auth"
	// CommandReviewedRepos lists the repositories of the pull requests the
	// viewer reviewed since a date: reviewed-repos <yyyy-mm-dd>, one
	// owner/name a line, once per pull request.
	CommandReviewedRepos = "reviewed-repos"
	// CommandTeams lists the viewer's teams, one org/slug a line.
	CommandTeams = "teams"
)

// ExitNoGH is the exit of a command that found no gh to run, the shell's
// "command not found".
const ExitNoGH = 127

// The GitHub plugin's actions the host calls.
const (
	// ActionBasics reads a pull request's basics: its answer is the
	// pr.PullRequest as JSON.
	ActionBasics = "pr.basics"
	// ActionAdopt takes a pull request over into the loop.
	ActionAdopt = "pr.adopt"
	// ActionRefresh reads a pull request in full into the plugin's store
	// and reports it.
	ActionRefresh = "pr.refresh"
)

// ViewBudget is the GitHub plugin's view of its API budget.
const ViewBudget = "budget"
