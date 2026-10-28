// Package api is the schema every client of the daemon speaks: its version,
// its headers and the wire types it decodes.
package api

import (
	"errors"
	"net/url"
	"regexp"
	"strings"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Version is the API this binary speaks, a major only. A client of another
// major is refused; a change older clients can ignore, such as a new field,
// keeps it.
const Version = "1"

const (
	Header = "X-Loop-API"
	// SessionHeader carries the handle the workspace manager gave the calling
	// agent's process; the daemon resolves it to the loop's session.
	SessionHeader = "X-Loop-Session"
	// ManagerHeader names the manager that gave SessionHeader's handle, when
	// the caller knows it: a hook does, an MCP request does not.
	ManagerHeader = "X-Loop-Manager"
	// MCPKeyHeader carries the per-install secret the MCP endpoint requires.
	MCPKeyHeader = "X-Loop-Key"
)

// Major is the major of a version, "0" for a missing one.
func Major(v string) string {
	major, _, _ := strings.Cut(strings.TrimSpace(v), ".")
	if major == "" {
		return "0"
	}
	return major
}

// AdminAPI paths: the daemon's answers to diagnostics and dry runs, served
// on the Unix socket only, like AgentAPI.
const (
	AdminPrefix     = "/api/admin/"
	AdminDryRunTurn = "/api/admin/dry-run-turn"
	AdminDoctor     = "/api/admin/doctor"
	// AdminTurn takes the pull request's URL in the query parameter pr.
	AdminTurn = "/api/admin/turn"
	// AdminProbe sends a probe turn: the loop's self-test of an agent home.
	AdminProbe = "/api/admin/probe"
	// AdminGitAccess records a git access outcome another process saw.
	AdminGitAccess = "/api/admin/git-access"
	// AdminMeter is the metered cost of every scope since the daemon started.
	AdminMeter = "/api/admin/meter"
)

// Rows takes the query parameters view, person and since, a Since's
// String.
const Rows = "/api/rows"

// Plan is the workflows' plan: GET takes a PlanQuery's fields as query
// parameters, POST a PlanActRequest.
const Plan = "/api/plan"

// Resolve is the unit an address lands in: GET takes the address as the
// address query parameter and answers a Resolved.
const Resolve = "/api/resolve"

// UnitDetail is a unit's detail; its checks and actions are under it.
const UnitDetail = "/api/units/{provider}/{key}"

// UnitPath is the path of u's detail, followed by the segments of rest.
func UnitPath(u unit.Unit, rest ...string) string {
	p := "/api/units/" + url.PathEscape(string(u.Provider)) + "/" + url.PathEscape(u.Key)
	for _, r := range rest {
		p += "/" + url.PathEscape(r)
	}
	return p
}

// Operation is one route of the daemon's API.
type Operation struct{ Method, Path string }

// Pattern is the operation as a net/http ServeMux pattern.
func (o Operation) Pattern() string { return o.Method + " " + o.Path }

// Operations is every route the daemon serves, UserAPI, AgentAPI and
// AdminAPI; the daemon routes each exactly once and no other.
var Operations = []Operation{
	{"GET", "/api/health"},
	{"GET", Rows},
	{"GET", Catalog},
	{"GET", Resolve},
	{"GET", UnitDetail},
	{"GET", UnitDetail + "/checks"},
	{"POST", "/api/jobs/{name}"},
	{"POST", UnitDetail + "/{action}"},
	{"GET", Actions},
	{"GET", Keys},
	{"GET", "/api/events"},
	{"GET", "/api/activity"},
	{"POST", "/api/pause"},
	{"POST", "/api/app"},
	{"POST", "/api/workspaces/remove-done"},
	{"GET", Intakes},
	{"POST", IntakeWithout},
	{"POST", IntakeCancel},
	{"GET", Plan},
	{"POST", Plan},
	{"GET", Plugins},
	{"GET", PluginAssets},
	{"GET", PluginView},
	{"POST", PluginView},
	{"POST", PluginAction},
	{"POST", PluginRun},
	{"GET", ViewsPath},
	{"GET", TasksPath},
	{"POST", TasksPath},
	{"GET", TaskDetail},
	{"POST", TaskVerb},
	{"GET", TimelinePath},
	{"GET", RunsPath},
	{"POST", ChatPath},
	{"POST", AttachPath},
	{"GET", GatesPath},
	{"POST", GatePath},
	{"POST", RunOpen},
	{"POST", RunEnter},
	{"GET", RunTranscript},
	{"GET", CountersPath},
	{"GET", Drafts},
	{"POST", Drafts},
	{"GET", DraftDetail},
	{"POST", DraftActionPath},
	{"GET", DigestPath},
	{"GET", ReviewPath},
	{"POST", ReviewStepDone},

	{"POST", AgentTurnsStart},
	{"POST", AgentTurnsByHand},
	{"POST", AgentTurnsEnd},
	{"POST", AgentSessionsStart},
	{"POST", AgentSessionsStop},
	{"POST", AgentSessionsPrompt},
	{"GET", AgentAnswer},

	{"POST", AdminDryRunTurn},
	{"GET", AdminDoctor},
	{"GET", AdminTurn},
	{"POST", AdminProbe},
	{"POST", AdminGitAccess},
	{"GET", AdminTimings},
	{"GET", AdminFacts},
	{"GET", AdminMeter},
}

var sessionHandle = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)

var ErrSessionHandle = errors.New(SessionHeader + " is missing or not a process handle: this session does not run under one of " + brand.Name + "'s workspace managers, so " + brand.Name + " cannot tell which turn it records")

// ErrUnknownSession is a handle no session of the loop took.
var ErrUnknownSession = errors.New(SessionHeader + " names a process no session of " + brand.Name + " took: start the session from " + brand.Name + ", or let its SessionStart hook run")

// SessionHandle validates the value of SessionHeader.
func SessionHandle(v string) (string, error) {
	if v == "" || strings.Contains(v, "${") || !sessionHandle.MatchString(v) {
		return "", ErrSessionHandle
	}
	return v, nil
}
