package builtin

import (
	"slices"

	"github.com/wloophq/wloop/internal/domain/unit"
)

// The roles Go resolves an intent by (Registry.ByRole).
const (
	RoleReview   = "pr.review"
	RoleAddress  = "pr.address"
	RoleNudge    = "pr.nudge"
	RoleProbe    = "probe"
	RoleHeadless = "headless"
	RoleAttach   = "attach"
)

// BuildsBrief is what the brief intent builds, and the kind of the record, of records.toml, that a brief is.
const BuildsBrief = "brief"

// Builds are what a builder intent may build.
var Builds = []string{BuildsBrief}

// The round kinds a role binds: a review round, first or follow-up by the
// step's stage, and an address round.
const (
	RoundKindReview  = "review"
	RoundKindAddress = "address"
)

// LaunchProbe is the launch of a probe: a synthetic pull request in the
// workspace the request names.
const LaunchProbe = "probe"

// The agent policies a role binds: a round's, and the probe's.
const (
	PolicyRound = "round"
	PolicyProbe = "probe"
)

// The contexts a role binds: a real pull request, and a made-up one.
const (
	ContextPR        = "pr"
	ContextSynthetic = "synthetic"
)

// PrepareAction prefixes a role's prepare: the plugin action that fills its
// turn's inputs or refuses the turn, such as "action:nudge.prepare".
const PrepareAction = "action:"

// ReconcileMyReview reconciles a review turn with the review the viewer
// published.
const ReconcileMyReview = "my_review"

// Role is what an intent's role binds it to: the plugin mechanism Go runs
// its turns with.
type Role struct {
	Name string
	Doc  string
	// Provider is the provider whose units the role's turns run on; "" any.
	Provider unit.ProviderID
	// RoundKind is the sweep round its turns are; "" none.
	RoundKind string
	// Reconcile, Prepare, Launch: builtins its turns run; "" none.
	Reconcile, Prepare, Launch string
	// Workspace is the workspace builtin its holder names; "" any.
	Workspace string
	// Context is what its turn is about; Policy the agent policy of its
	// sessions.
	Context, Policy string
	// DrivesRow says its turns drive the row of the workspace they run in;
	// a user intent taking the role takes it over.
	DrivesRow bool
}

// Roles are what Go resolves an intent by, a manifest's role: the core's,
// then each plugin's.
var Roles = slices.Clone(coreRoles)

var coreRoles = []Role{
	{Name: RoleHeadless, Doc: "The intent a headless turn on a unit runs when its request names none.",
		Context: ContextPR, Policy: PolicyRound},
	{Name: RoleAttach, Doc: "The intent a session started outside the loop runs when it attaches to a task: started by hand on that session, which becomes a run of the task.",
		Provider: unit.Task, Context: ContextPR, Policy: PolicyRound},
}

// The host mechanisms a plugin's role may name, by the manifest key that
// names them.
var (
	RoundKinds = []string{RoundKindReview, RoundKindAddress}
	Reconciles = []string{ReconcileMyReview}
	Launches   = []string{LaunchProbe}
	Contexts   = []string{ContextPR, ContextSynthetic}
	Policies   = []string{PolicyRound, PolicyProbe}
)

// NoRole is what an intent without a role binds.
var NoRole = Role{Context: ContextPR, Policy: PolicyRound}

// RoleOf is the role named name; NoRole and false when there is none.
func RoleOf(name string) (Role, bool) {
	for _, r := range Roles {
		if r.Name == name {
			return r, true
		}
	}
	return NoRole, false
}

// RoleNames are the names of Roles.
func RoleNames() []string {
	out := make([]string, len(Roles))
	for i, r := range Roles {
		out[i] = r.Name
	}
	return out
}
