// Package config loads the user's settings and locates the state directory.
package config

import (
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/duration"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
)

type Config struct {
	// ProjectsDir is where `review` clones a repository that is not
	// registered in the workspace manager yet. No default: a wrong guess
	// clones duplicates.
	ProjectsDir string `json:"projects_dir"`
	// WorkspaceManagers are the enabled workspace managers in the order a new
	// worktree picks them: the first that hosts the agent and honors the
	// session's launch policy owns it.
	WorkspaceManagers []string `json:"workspace_managers"`
	Agent             string   `json:"agent"`
	// GitHub is the GitHub plugin's settings.
	GitHub GitHub `json:"github"`
	// AgentHome is the agent's own configuration directory (CLAUDE_CONFIG_DIR
	// for Claude Code), "" for the agent's default.
	AgentHome string `json:"agent_home"`
	// Profile is the directory with the reviewer's judgment.
	Profile string `json:"profile"`
	// AgentModels pins, per agent, a model tier to one of that agent's
	// own model names; the loop hands the name to the agent unread.
	AgentModels map[string]map[turns.Tier]string `json:"agent_models"`
	// Projects override settings for the pull requests of matching
	// repositories; the first match wins.
	Projects []Project `json:"projects"`
	Sweep    Sweep     `json:"sweep"`
	HTTP     HTTP      `json:"http"`
	MCP      MCP       `json:"mcp"`
	Brief    Brief     `json:"brief"`
	// Views holds each view sink's own settings by sink name.
	Views    map[string]SinkConfig `json:"views"`
	Cleanup  Cleanup               `json:"cleanup"`
	Nudge    Nudge                 `json:"nudge"`
	Classify Classify              `json:"classify"`
	// Workflows override each workflow's manifest by workflow name.
	Workflows map[string]WorkflowConfig `json:"workflows"`
	// Intents set each intent's settings by intent name.
	Intents map[string]IntentConfig `json:"intents"`
	// Plugins switch the plugins that have no settings of their own in the
	// file, by provider id; the file switches a plugin with <id>.enabled.
	Plugins map[string]Plugin `json:"-"`
	// Placement is the rules that place units; the first match wins.
	Placement []PlacementRule `json:"placement,omitempty"`
	GTD       GTD             `json:"gtd"`
	// Notifications sets the menu bar's daily digest.
	Notifications Notifications `json:"notifications"`
	Slack         Slack         `json:"slack"`
	Dossiers      Dossiers      `json:"dossiers"`
	Drafts        Drafts        `json:"drafts"`
	Items         Items         `json:"items"`
	Headless      Headless      `json:"headless"`
	Turns         Turns         `json:"turns"`
}

// Turns caps the automated turns. PerUnitPerDay stops a runaway loop on one
// unit whose plugin keeps a per-unit count (GitHub's pull requests), counted
// per local day; PerDay caps the automated turns on every other unit, all
// workflows together.
type Turns struct {
	PerDay        int `json:"per_day"`
	PerUnitPerDay int `json:"per_unit_per_day"`
}

// GitHub is the GitHub plugin's settings.
type GitHub struct {
	// Enabled switches the plugin; nil means enabled.
	Enabled            *bool    `json:"enabled,omitempty"`
	ReviewPrefix       string   `json:"review_prefix"`
	AutomatedReviewers []string `json:"automated_reviewers"`
	// Owners restricts automated follow-ups to these GitHub owners. Empty
	// means the owners of every repository the workspace manager knows.
	Owners []string `json:"owners"`
	// TrustedAuthors are logins whose pull requests pass the author
	// association check of automated rounds, such as a bot with no
	// association to the repository.
	TrustedAuthors []string `json:"trusted_authors"`
	// FollowupsWithoutWorkspace lets a follow-up review recreate the review
	// workspace the user deleted; off, the sweep only logs the follow-up.
	FollowupsWithoutWorkspace bool `json:"followups_without_workspace"`
	// AddressWithoutWorkspace lets a triage round on a pull request I
	// develop create its author workspace when it has none; off, the sweep
	// only records the round it would start.
	AddressWithoutWorkspace bool `json:"address_without_workspace"`
	// AddressReviews starts a triage round in the author workspace when
	// someone reviews a pull request I develop.
	AddressReviews bool `json:"address_reviews"`
	// AddressSkipReviewers lists logins whose reviews never start a triage
	// round.
	AddressSkipReviewers []string `json:"address_skip_reviewers"`
	// AddressChecks starts a triage round when checks fail on the head of a
	// pull request I develop; IgnoreChecks names checks that never do.
	AddressChecks bool     `json:"address_checks"`
	IgnoreChecks  []string `json:"ignore_checks"`
	// AddressConflicts starts a triage round when a pull request I develop
	// conflicts with its base past the conflict window.
	AddressConflicts bool         `json:"address_conflicts"`
	Scope            Scope        `json:"scope"`
	Stalled          Stalled      `json:"stalled"`
	Drafts           PluginDrafts `json:"drafts"`
}

// Drafts holds the settings every proposer's drafts share.
type Drafts struct {
	// RepeatAfter is how long a nudged, discarded, sent or observed draft
	// keeps its rule or workflow from proposing it again, by default.
	RepeatAfter Days `json:"repeat_after"`
}

// IntentConfig is one intent's settings.
type IntentConfig struct {
	ProjectSettings *ProjectSettings `json:"project_settings,omitempty"`
}

// ProjectSettings says where an intent's sessions load the checkout's own
// agent settings: When is a predicate over its pull request's facts; where
// it does not hold, they start without them.
type ProjectSettings struct {
	When string `json:"when"`
}

// Headless caps the headless turns: the model tier of their calls, what one call may cost and what they
// may cost in a day; a cost of zero leaves it uncapped.
type Headless struct {
	Tier       turns.Tier `json:"tier"`
	PerCallUSD float64    `json:"per_call_usd"`
	PerDayUSD  float64    `json:"per_day_usd"`
}

// Dossiers sets the dossiers' lifecycle and quotas; a value of zero or less keeps its default.
type Dossiers struct {
	ArchiveAfter Days  `json:"archive_after"` // after the unit or project resolved
	PurgeAfter   Days  `json:"purge_after"`   // after the archive
	SoftCapMB    int64 `json:"soft_cap_mb"`   // per dossier, reported by doctor
	TotalCapMB   int64 `json:"total_cap_mb"`  // dossiers and archives, reported by doctor
	MaxFileMB    int64 `json:"max_file_mb"`   // a larger file is refused, linked instead
	CompactAtKB  int64 `json:"compact_at_kb"` // log/ size that triggers compaction
}

// GTD holds the entries' settings: when a waiting entry goes stale, and the areas and contexts offered.
type GTD struct {
	StaleWaitingDays int      `json:"stale_waiting_days"` // working days
	Areas            []string `json:"areas"`
	Contexts         []string `json:"contexts"`
}

// Notifications: DigestAt is the local "HH:MM" of the daily digest, "" for none.
type Notifications struct {
	DigestAt string `json:"digest_at"`
}

// SinkConfig is one view sink's own settings.
type SinkConfig struct {
	// Columns maps a semantic status (working, working-fallback, needs-me, reviewed, ready, in-progress, in-review, completed) to the sink's column.
	Columns map[string]string `json:"columns"`
}

// Column is sink's column for status, "" when unset.
func (c Config) Column(sink, status string) string { return c.Views[sink].Columns[status] }

const (
	ColumnWorking         = "working"
	ColumnWorkingFallback = "working-fallback"
	ColumnNeedsMe         = "needs-me"
	ColumnReviewed        = "reviewed"
	ColumnReady           = "ready"
	ColumnInProgress      = "in-progress"
	ColumnInReview        = "in-review"
	ColumnCompleted       = "completed"
)

// WithColumns is c with, for each sink in defaults, the default of each
// column c leaves out.
func (c Config) WithColumns(defaults map[string]map[string]string) Config {
	views := maps.Clone(c.Views)
	if views == nil {
		views = map[string]SinkConfig{}
	}
	for sink, def := range defaults {
		cols := maps.Clone(def)
		maps.Copy(cols, c.Views[sink].Columns)
		views[sink] = SinkConfig{Columns: cols}
	}
	c.Views = views
	return c
}

type Scope struct {
	People []string `json:"people"`
	Teams  []string `json:"teams"`
	Repos  []string `json:"repos"`
	Watch  []string `json:"watch"`
}

// Days is a duration written as duration.Parse reads it: "2d", "36h", "90m".
type Days time.Duration

func (d *Days) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := duration.Parse(s)
	if err != nil {
		return err
	}
	*d = Days(v)
	return nil
}

func (d Days) MarshalJSON() ([]byte, error) {
	return json.Marshal(duration.Format(time.Duration(d)))
}

type Stalled struct {
	Review Days `json:"review"`
	Reply  Days `json:"reply"`
	Merge  Days `json:"merge"`
}

type Sweep struct {
	Fast              Days    `json:"fast"`
	Slow              Days    `json:"slow"`
	RateLimitFraction float64 `json:"rate_limit_fraction"`
}

type HTTP struct {
	Port int `json:"port"`
}

// MCP is the daemon's MCP endpoint, which agent homes register by URL.
type MCP struct {
	Port int `json:"port"`
}

const DefaultMCPPort = 47800

// URL is the endpoint agent homes register.
func (m MCP) URL() string { return fmt.Sprintf("http://127.0.0.1:%d/mcp", m.Port) }

// Brief previews each pull request with one headless call of the agent,
// outside the sweeps.
type Brief struct {
	Enabled       bool       `json:"enabled"`
	Tier          turns.Tier `json:"tier"`
	MaxInputBytes int        `json:"max_input_bytes"`
}

// Cleanup holds the thresholds of the session and workspace cleanup workflows.
type Cleanup struct {
	Idle           Days `json:"idle"`
	AskedIdle      Days `json:"asked_idle"`
	WorkspaceGrace Days `json:"workspace_grace"` // counted in working time
	ManualIdle     Days `json:"manual_idle"`
	StaleWorkspace Days `json:"stale_workspace"`
	// Disposable names ignored files and directories a tool recreates, so
	// removing a workspace that holds them loses nothing.
	Disposable []string `json:"disposable"`
}

// WorkflowConfig overlays a workflow's manifest; a field left out keeps the
// manifest's.
type WorkflowConfig struct {
	Enabled *bool `json:"enabled,omitempty"`
	// Observe plans the workflow's items pending, for a person to run.
	Observe  *bool           `json:"observe,omitempty"`
	Approval *ApprovalConfig `json:"approval,omitempty"`
	// OnlyWhen is a predicate over the workflow's facts that must hold
	// besides its manifest's when; "" adds none.
	OnlyWhen string          `json:"only_when,omitempty"`
	Drafts   *WorkflowDrafts `json:"drafts,omitempty"`
}

// WorkflowDrafts holds the approval of the drafts a workflow proposes.
type WorkflowDrafts struct {
	Approval *ApprovalConfig `json:"approval,omitempty"`
}

// ApprovalConfig overlays an approval block field by field: Mode and
// Window replace the manifest's, Rule its rules; "", nil and a nil Rule
// keep the manifest's, an empty Rule clears them.
type ApprovalConfig struct {
	Mode   string               `json:"mode,omitempty"`
	Window *Days                `json:"window,omitempty"`
	Rule   []ApprovalRuleConfig `json:"rule,omitempty"`
}

type Nudge struct {
	SilentDays int `json:"silent_days"` // calendar days
}

type Classify struct {
	PerDay int `json:"per_day"`
}

// Project matches repositories by an owner/repo pattern such as
// "acme/*" or "acme/infra". Empty fields keep the
// top-level value.
type Project struct {
	Match       string `json:"match"`
	ProjectsDir string `json:"projects_dir"`
	// Agent is the agent the project's sessions run.
	Agent     string `json:"agent"`
	AgentHome string `json:"agent_home"`
	// WorkspaceManagers orders the managers a new worktree of the project picks from.
	WorkspaceManagers []string `json:"workspace_managers"`

	GitHub ProjectGitHub `json:"github"`
}

// ProjectGitHub are the GitHub settings a project overrides; nil keeps the
// top-level value.
type ProjectGitHub struct {
	TrustedAuthors            []string `json:"trusted_authors"`
	FollowupsWithoutWorkspace *bool    `json:"followups_without_workspace"`
	AddressWithoutWorkspace   *bool    `json:"address_without_workspace"`
}

// For returns the settings that apply to a pull request of owner/repo.
func (c Config) For(owner, repo string) Config {
	for _, p := range c.Projects {
		if !p.Matches(owner, repo) {
			continue
		}
		if p.ProjectsDir != "" {
			c.ProjectsDir = p.ProjectsDir
		}
		if p.Agent != "" {
			c.Agent = p.Agent
		}
		if p.AgentHome != "" {
			c.AgentHome = p.AgentHome
		}
		if p.WorkspaceManagers != nil {
			c.WorkspaceManagers = p.WorkspaceManagers
		}
		if p.GitHub.TrustedAuthors != nil {
			c.GitHub.TrustedAuthors = p.GitHub.TrustedAuthors
		}
		if p.GitHub.FollowupsWithoutWorkspace != nil {
			c.GitHub.FollowupsWithoutWorkspace = *p.GitHub.FollowupsWithoutWorkspace
		}
		if p.GitHub.AddressWithoutWorkspace != nil {
			c.GitHub.AddressWithoutWorkspace = *p.GitHub.AddressWithoutWorkspace
		}
		break
	}
	return c
}

func (p Project) Matches(owner, repo string) bool {
	ok, err := path.Match(strings.ToLower(p.Match), strings.ToLower(owner+"/"+repo))
	return err == nil && ok
}

// AgentHomes lists every agent home the settings name.
func (c Config) AgentHomes() []string {
	var out []string
	for _, d := range append([]string{c.AgentHome}, projectHomes(c.Projects)...) {
		if d != "" && !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	return out
}

func projectHomes(ps []Project) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, p.AgentHome)
	}
	return out
}

const DefaultPRDailyCap = 5

// Defaults is the config without a file. It names no agent and no workspace
// manager: the composition root picks those.
func Defaults() Config {
	return Config{
		GitHub: GitHub{
			AddressReviews:     true,
			AddressChecks:      true,
			AddressConflicts:   true,
			ReviewPrefix:       "REVIEW ",
			AutomatedReviewers: []string{},
			Stalled:            Stalled{Review: Days(48 * time.Hour), Reply: Days(24 * time.Hour), Merge: Days(24 * time.Hour)},
		},
		Sweep: Sweep{Fast: Days(2 * time.Minute), Slow: Days(time.Hour), RateLimitFraction: 0.5},
		Brief: Brief{Tier: turns.TierFast, MaxInputBytes: 60000},
		MCP:   MCP{Port: DefaultMCPPort},
		Views: map[string]SinkConfig{},
		Cleanup: Cleanup{
			Idle:           Days(2 * time.Hour),
			AskedIdle:      Days(4 * time.Hour),
			WorkspaceGrace: Days(24 * time.Hour),
			ManualIdle:     Days(24 * time.Hour),
			StaleWorkspace: Days(14 * 24 * time.Hour),
			Disposable: []string{
				"node_modules", ".venv", "venv", "__pycache__", ".pytest_cache", ".mypy_cache", ".ruff_cache",
				"dist", "build", "target", "bin", ".terraform", ".next", "coverage", ".gradle", "_build", ".docusaurus",
				".DS_Store", ".coverage", dturn.ScratchDir,
			},
		},
		Nudge:         Nudge{SilentDays: 30},
		Classify:      Classify{PerDay: 50},
		GTD:           GTD{StaleWaitingDays: 2},
		Notifications: Notifications{DigestAt: "09:00"},
		Dossiers: Dossiers{ArchiveAfter: Days(30 * 24 * time.Hour), PurgeAfter: Days(400 * 24 * time.Hour),
			SoftCapMB: 50, TotalCapMB: 2048, MaxFileMB: 5, CompactAtKB: 256},
		Drafts:   Drafts{RepeatAfter: Days(24 * time.Hour)},
		Headless: Headless{Tier: turns.TierBalanced, PerCallUSD: 0.5, PerDayUSD: 5},
		Turns:    Turns{PerDay: 20, PerUnitPerDay: DefaultPRDailyCap},
	}
}

func Dir() string {
	return xdgDir("XDG_CONFIG_HOME", ".config")
}

func Path() string { return filepath.Join(Dir(), "config.json") }

// StateDir holds the inventory database, the state lock and the logs.
func StateDir() string {
	return xdgDir("XDG_STATE_HOME", filepath.Join(".local", "state"))
}

func DaemonSocket() string { return filepath.Join(StateDir(), "daemon.sock") }

func xdgDir(env, fallback string) string {
	base := strings.TrimSpace(os.Getenv(env))
	if base == "" || !filepath.IsAbs(base) {
		base = filepath.Join(Home(), fallback)
	}
	return filepath.Join(base, brand.StateDir())
}

// UserHomeVar names an absolute directory Home returns instead of the
// user's, so an isolated daemon finds no agent home of the user while HOME,
// which the Keychain needs, stays real.
const UserHomeVar = "LOOP_USER_HOME"

func Home() string {
	if h := os.Getenv(UserHomeVar); filepath.IsAbs(h) {
		return h
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

// Load reads the config file over base, falling back to base when the file
// is refused.
func Load(base Config) (Config, error) {
	return LoadFile(Path(), base)
}

func LoadFile(file string, base Config) (Config, error) {
	c := base
	raw, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, fmt.Errorf("reading %s: %w", file, err)
	}
	if err := decodeStrict(file, raw, &c); err != nil {
		return base, err
	}
	c.ProjectsDir = ExpandHome(c.ProjectsDir)
	if len(c.WorkspaceManagers) == 0 {
		return base, fmt.Errorf("%s: workspace_managers is empty; list at least one, such as %q", file, base.WorkspaceManagers)
	}
	c.AgentHome = ExpandHome(c.AgentHome)
	c.Profile = ResolveProfile(c.Profile)
	for i, p := range c.Projects {
		if _, err := path.Match(p.Match, ""); p.Match == "" || err != nil {
			return base, fmt.Errorf("%s: projects[%d]: invalid match %q; use owner/repo, owner/*", file, i, p.Match)
		}
		c.Projects[i].ProjectsDir = ExpandHome(p.ProjectsDir)
		c.Projects[i].AgentHome = ExpandHome(p.AgentHome)
	}
	if err := c.validateTrustedAuthors(); err != nil {
		return base, fmt.Errorf("%s: %w", file, err)
	}
	if c.Turns.PerUnitPerDay <= 0 {
		c.Turns.PerUnitPerDay = DefaultPRDailyCap
	}
	if err := c.validateCockpit(); err != nil {
		return base, fmt.Errorf("%s: %w", file, err)
	}
	if err := c.validateWorkflows(); err != nil {
		return base, fmt.Errorf("%s: %w", file, err)
	}
	if err := c.validatePlacement(); err != nil {
		return base, fmt.Errorf("%s: %w", file, err)
	}
	if err := c.validatePluginDrafts(); err != nil {
		return base, fmt.Errorf("%s: %w", file, err)
	}
	return c, nil
}

func (c Config) validateTrustedAuthors() error {
	lists := map[string][]string{"github.trusted_authors": c.GitHub.TrustedAuthors}
	for i, p := range c.Projects {
		lists[fmt.Sprintf("projects[%d].github.trusted_authors", i)] = p.GitHub.TrustedAuthors
	}
	for key, list := range lists {
		if slices.ContainsFunc(list, func(a string) bool { return strings.TrimSpace(a) == "" }) {
			return fmt.Errorf("%s has an empty login", key)
		}
	}
	return nil
}

func (c Config) validateCockpit() error {
	durations := []struct {
		key string
		v   Days
	}{
		{"github.stalled.review", c.GitHub.Stalled.Review},
		{"github.stalled.reply", c.GitHub.Stalled.Reply},
		{"github.stalled.merge", c.GitHub.Stalled.Merge},
		{"sweep.fast", c.Sweep.Fast},
		{"sweep.slow", c.Sweep.Slow},
	}
	for _, d := range durations {
		if d.v <= 0 {
			return fmt.Errorf("%s must be positive, got %s", d.key, time.Duration(d.v))
		}
	}
	if at := c.Notifications.DigestAt; at != "" {
		if _, err := time.Parse("15:04", at); err != nil {
			return fmt.Errorf("notifications.digest_at must be a local time HH:MM or empty, got %q", at)
		}
	}
	if f := c.Sweep.RateLimitFraction; f < 0 || f > 1 {
		return fmt.Errorf("sweep.rate_limit_fraction must be between 0 and 1, got %v", f)
	}
	if c.HTTP.Port < 0 || c.HTTP.Port > 65535 {
		return fmt.Errorf("http.port must be between 0 and 65535, got %d", c.HTTP.Port)
	}
	if c.MCP.Port < 1 || c.MCP.Port > 65535 {
		return fmt.Errorf("mcp.port must be between 1 and 65535, got %d", c.MCP.Port)
	}
	if b := c.Brief.MaxInputBytes; b < 1000 || b > 200_000 {
		return fmt.Errorf("brief.max_input_bytes must be between 1000 and 200000, got %d", b)
	}
	if _, err := turns.ParseTier(string(c.Brief.Tier)); err != nil {
		return fmt.Errorf("brief.tier: %w", err)
	}
	if _, err := turns.ParseTier(string(c.Headless.Tier)); err != nil {
		return fmt.Errorf("headless.tier: %w", err)
	}
	if c.Headless.PerCallUSD < 0 || c.Headless.PerDayUSD < 0 {
		return fmt.Errorf("headless.per_call_usd and headless.per_day_usd must be zero or more")
	}
	for agent, pins := range c.AgentModels {
		for tier, model := range pins {
			if _, err := turns.ParseTier(string(tier)); err != nil {
				return fmt.Errorf("agent_models.%s: %w", agent, err)
			}
			if strings.TrimSpace(model) == "" {
				return fmt.Errorf("agent_models.%s.%s must name a model", agent, tier)
			}
		}
	}
	return nil
}

func (c Config) validateWorkflows() error {
	durations := []struct {
		key string
		v   Days
	}{
		{"cleanup.idle", c.Cleanup.Idle},
		{"cleanup.asked_idle", c.Cleanup.AskedIdle},
		{"cleanup.workspace_grace", c.Cleanup.WorkspaceGrace},
		{"cleanup.manual_idle", c.Cleanup.ManualIdle},
		{"cleanup.stale_workspace", c.Cleanup.StaleWorkspace},
	}
	for _, d := range durations {
		if d.v <= 0 {
			return fmt.Errorf("%s must be positive, got %s", d.key, time.Duration(d.v))
		}
	}
	if c.Nudge.SilentDays < 1 {
		return fmt.Errorf("nudge.silent_days must be positive, got %d", c.Nudge.SilentDays)
	}
	if c.Classify.PerDay < 0 {
		return fmt.Errorf("classify.per_day must not be negative, got %d", c.Classify.PerDay)
	}
	if c.Turns.PerDay < 0 {
		return fmt.Errorf("turns.per_day must not be negative, got %d", c.Turns.PerDay)
	}
	if c.Drafts.RepeatAfter <= 0 {
		return fmt.Errorf("drafts.repeat_after must be positive, got %s", time.Duration(c.Drafts.RepeatAfter))
	}
	for _, intent := range slices.Sorted(maps.Keys(c.Intents)) {
		ps := c.Intents[intent].ProjectSettings
		if ps == nil {
			continue
		}
		if _, err := expr.Compile(ps.When, builtin.PREnv()); err != nil {
			return fmt.Errorf("intents.%s.project_settings.when: %w", intent, err)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(c.Workflows)) {
		w := c.Workflows[name]
		if w.OnlyWhen != "" {
			if _, err := expr.CompileWorkflow(w.OnlyWhen, builtin.WorkflowEnvAll()); err != nil {
				return fmt.Errorf("workflows.%s.only_when: %w", name, err)
			}
		}
		if a := w.DraftApproval(); a != nil {
			if _, err := a.Policy(builtin.ApprovalEnv(true, builtin.UnitSlots()...)); err != nil {
				return fmt.Errorf("workflows.%s.drafts.approval: %w", name, err)
			}
		}
		if w.Approval != nil {
			if err := w.Approval.validateRun("workflows." + name + ".approval"); err != nil {
				return err
			}
		}
	}
	return nil
}

// ProjectSettingsFor says whether a session of intent on the pull request
// whose facts pr resolves loads the checkout's own agent settings. Without
// the facts, an intent that has a predicate does not.
func (c Config) ProjectSettingsFor(intent string, pr expr.Facts) bool {
	ps := c.Intents[intent].ProjectSettings
	if ps == nil {
		return true
	}
	if pr == nil {
		return false
	}
	p, err := expr.Compile(ps.When, builtin.PREnv())
	return err == nil && p.Eval(pr)
}

// oneOf refuses a set value outside allowed; "" keeps the manifest's.
func oneOf[T ~string](key, v string, allowed ...T) error {
	if v == "" || slices.Contains(allowed, T(v)) {
		return nil
	}
	names := make([]string, len(allowed))
	for i, a := range allowed {
		names[i] = string(a)
	}
	return fmt.Errorf("%s: %q is not one of %s", key, v, strings.Join(names, ", "))
}

// expandConfigPath resolves a path relative to the config directory.
func expandConfigPath(p string) string {
	p = ExpandHome(p)
	if p != "" && !filepath.IsAbs(p) {
		return filepath.Join(Dir(), p)
	}
	return p
}

func ExpandHome(p string) string {
	if p == "~" {
		return Home()
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(Home(), p[2:])
	}
	return p
}
