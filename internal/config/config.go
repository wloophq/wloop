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
	"strconv"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/turns"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/workflow"
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
	WorkspaceManagers  []string `json:"workspace_managers"`
	Agent              string   `json:"agent"`
	ReviewPrefix       string   `json:"review_prefix"`
	Rounds             Rounds   `json:"rounds"`
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
	// AgentHome is the agent's own configuration directory (CLAUDE_CONFIG_DIR
	// for Claude Code), "" for the agent's default.
	AgentHome string `json:"agent_home"`
	// Profile is the directory with the reviewer's judgment.
	Profile string `json:"profile"`
	// AgentModels pins, per agent, a model tier to one of that agent's
	// own model names; the loop hands the name to the agent unread.
	AgentModels map[string]map[turns.Tier]string `json:"agent_models"`
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
	AddressConflicts bool `json:"address_conflicts"`
	// Projects override settings for the pull requests of matching
	// repositories; the first match wins.
	Projects []Project `json:"projects"`
	Scope    Scope     `json:"scope"`
	Stalled  Stalled   `json:"stalled"`
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
	// ProjectSettings maps an intent to a predicate over its pull request's
	// facts: where it does not hold, the intent's sessions start without the
	// checkout's own agent settings. An intent it does not name loads them.
	ProjectSettings map[string]string `json:"project_settings"`
	// Plugins switch each compiled-in plugin by provider id.
	Plugins map[string]Plugin `json:"plugins"`
	// Placement is the rules that place units; the first match wins.
	Placement []PlacementRule `json:"placement,omitempty"`
	GTD       GTD             `json:"gtd"`
	// Notifications sets the menu bar's daily digest.
	Notifications Notifications `json:"notifications"`
	Slack         Slack         `json:"slack"`
	Dossiers      Dossiers      `json:"dossiers"`
	// Proposals sets the rules that propose drafts on their own.
	Proposals Proposals `json:"proposals"`
	Items     Items     `json:"items"`
	Headless  Headless  `json:"headless"`
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

// Days is a duration written as "2d", "36h" or "90m" in the config.
type Days time.Duration

func (d *Days) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := ParseDays(s)
	if err != nil {
		return err
	}
	*d = Days(v)
	return nil
}

// MarshalJSON writes the form ParseDays reads: "2d", "36h", "2m".
func (d Days) MarshalJSON() ([]byte, error) {
	v := time.Duration(d)
	if v > 0 && v%(24*time.Hour) == 0 {
		return json.Marshal(fmt.Sprintf("%dd", v/(24*time.Hour)))
	}
	s := v.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return json.Marshal(s)
}

func ParseDays(s string) (time.Duration, error) {
	if n, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.Atoi(n)
		if err != nil {
			return 0, fmt.Errorf("duration %q: %w", s, err)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return 0, fmt.Errorf("duration %q: %w", s, err)
	}
	return v, nil
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

// Rounds caps the automatic rounds: PerPRPerDay stops a runaway loop on
// one pull request, counted per local day.
type Rounds struct {
	PerPRPerDay int `json:"per_pr_per_day"`
}

// Brief previews each pull request with one headless call of the agent,
// outside the sweeps.
type Brief struct {
	Enabled      bool       `json:"enabled"`
	Tier         turns.Tier `json:"tier"`
	MaxDiffBytes int        `json:"max_diff_bytes"`
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

type WorkflowConfig struct {
	Mode     string          `json:"mode,omitempty"`
	Enabled  *bool           `json:"enabled,omitempty"`
	Approval *ApprovalConfig `json:"approval,omitempty"`
	// OnlyWhen is a predicate over the workflow's facts that must hold
	// besides its manifest's when; "" adds none.
	OnlyWhen string `json:"only_when,omitempty"`
}

type ApprovalConfig struct {
	Default string `json:"default,omitempty"`
	Window  *Days  `json:"window,omitempty"`
}

type Nudge struct {
	SilentDays int `json:"silent_days"` // calendar days
}

type Classify struct {
	DailyCap int `json:"daily_cap"`
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
		if p.TrustedAuthors != nil {
			c.TrustedAuthors = p.TrustedAuthors
		}
		if p.FollowupsWithoutWorkspace != nil {
			c.FollowupsWithoutWorkspace = *p.FollowupsWithoutWorkspace
		}
		if p.AddressWithoutWorkspace != nil {
			c.AddressWithoutWorkspace = *p.AddressWithoutWorkspace
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

func Defaults() Config {
	return Config{
		Agent:              "claude",
		WorkspaceManagers:  []string{"claude-desktop", "orca"},
		AddressReviews:     true,
		AddressChecks:      true,
		AddressConflicts:   true,
		ReviewPrefix:       "REVIEW ",
		Rounds:             Rounds{PerPRPerDay: DefaultPRDailyCap},
		AutomatedReviewers: []string{},
		Stalled:            Stalled{Review: Days(48 * time.Hour), Reply: Days(24 * time.Hour), Merge: Days(24 * time.Hour)},
		Sweep:              Sweep{Fast: Days(2 * time.Minute), Slow: Days(time.Hour), RateLimitFraction: 0.5},
		Brief:              Brief{Tier: turns.TierFast, MaxDiffBytes: 60000},
		MCP:                MCP{Port: DefaultMCPPort},
		Views:              map[string]SinkConfig{},
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
		Classify:      Classify{DailyCap: 50},
		GTD:           GTD{StaleWaitingDays: 2},
		Notifications: Notifications{DigestAt: "09:00"},
		Dossiers: Dossiers{ArchiveAfter: Days(30 * 24 * time.Hour), PurgeAfter: Days(400 * 24 * time.Hour),
			SoftCapMB: 50, TotalCapMB: 2048, MaxFileMB: 5, CompactAtKB: 256},
		Proposals: Proposals{RenudgeAfter: Days(24 * time.Hour)},
		Headless:  Headless{Tier: turns.TierBalanced, PerCallUSD: 0.5, PerDayUSD: 5},
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

func Load() (Config, error) {
	return LoadFile(Path())
}

func LoadFile(file string) (Config, error) {
	c := Defaults()
	raw, err := os.ReadFile(file)
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return c, fmt.Errorf("reading %s: %w", file, err)
	}
	if err := decodeStrict(file, raw, &c); err != nil {
		return Defaults(), err
	}
	c.ProjectsDir = ExpandHome(c.ProjectsDir)
	if len(c.WorkspaceManagers) == 0 {
		return Defaults(), fmt.Errorf("%s: workspace_managers is empty; list at least one, such as %q", file, Defaults().WorkspaceManagers)
	}
	c.AgentHome = ExpandHome(c.AgentHome)
	c.Profile = ResolveProfile(c.Profile)
	for i, p := range c.Projects {
		if _, err := path.Match(p.Match, ""); p.Match == "" || err != nil {
			return Defaults(), fmt.Errorf("%s: projects[%d]: invalid match %q; use owner/repo, owner/*", file, i, p.Match)
		}
		c.Projects[i].ProjectsDir = ExpandHome(p.ProjectsDir)
		c.Projects[i].AgentHome = ExpandHome(p.AgentHome)
	}
	if err := c.validateTrustedAuthors(); err != nil {
		return Defaults(), fmt.Errorf("%s: %w", file, err)
	}
	if c.Rounds.PerPRPerDay <= 0 {
		c.Rounds.PerPRPerDay = DefaultPRDailyCap
	}
	if err := c.validateCockpit(); err != nil {
		return Defaults(), fmt.Errorf("%s: %w", file, err)
	}
	if err := c.validateWorkflows(); err != nil {
		return Defaults(), fmt.Errorf("%s: %w", file, err)
	}
	if err := c.validatePlacement(); err != nil {
		return Defaults(), fmt.Errorf("%s: %w", file, err)
	}
	if err := c.Proposals.validate(); err != nil {
		return Defaults(), fmt.Errorf("%s: %w", file, err)
	}
	return c, nil
}

func (c Config) validateTrustedAuthors() error {
	lists := map[string][]string{"trusted_authors": c.TrustedAuthors}
	for i, p := range c.Projects {
		lists[fmt.Sprintf("projects[%d].trusted_authors", i)] = p.TrustedAuthors
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
		{"stalled.review", c.Stalled.Review},
		{"stalled.reply", c.Stalled.Reply},
		{"stalled.merge", c.Stalled.Merge},
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
	if b := c.Brief.MaxDiffBytes; b < 1000 || b > 200_000 {
		return fmt.Errorf("brief.max_diff_bytes must be between 1000 and 200000, got %d", b)
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
	if c.Classify.DailyCap < 0 {
		return fmt.Errorf("classify.daily_cap must not be negative, got %d", c.Classify.DailyCap)
	}
	for _, intent := range slices.Sorted(maps.Keys(c.ProjectSettings)) {
		if _, err := expr.Compile(c.ProjectSettings[intent], builtin.PREnv()); err != nil {
			return fmt.Errorf("project_settings.%s: %w", intent, err)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(c.Workflows)) {
		w := c.Workflows[name]
		if err := oneOf("workflows."+name+".mode", w.Mode, workflow.ModeObserve, workflow.ModeApply); err != nil {
			return err
		}
		if w.OnlyWhen != "" {
			if _, err := expr.CompileWorkflow(w.OnlyWhen, builtin.WorkflowEnvAll()); err != nil {
				return fmt.Errorf("workflows.%s.only_when: %w", name, err)
			}
		}
		if w.Approval == nil {
			continue
		}
		if err := oneOf("workflows."+name+".approval.default", w.Approval.Default, workflow.OutcomeRun, workflow.OutcomeWait); err != nil {
			return err
		}
		if win := w.Approval.Window; win != nil && *win < 0 {
			return fmt.Errorf("workflows.%s.approval.window must not be negative, got %s", name, time.Duration(*win))
		}
	}
	return nil
}

// ProjectSettingsFor says whether a session of intent on the pull request
// whose facts pr resolves loads the checkout's own agent settings. Without
// the facts, an intent that has a predicate does not.
func (c Config) ProjectSettingsFor(intent string, pr expr.Facts) bool {
	src, ok := c.ProjectSettings[intent]
	if !ok {
		return true
	}
	if pr == nil {
		return false
	}
	p, err := expr.Compile(src, builtin.PREnv())
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
