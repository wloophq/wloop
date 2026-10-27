package cli

import (
	"cmp"
	"context"
	"path/filepath"
	"time"

	"github.com/wloophq/wloop/internal/adapter/agent/claude"
	acd "github.com/wloophq/wloop/internal/adapter/claudedesktop"
	adossier "github.com/wloophq/wloop/internal/adapter/dossier"
	factentry "github.com/wloophq/wloop/internal/adapter/facts/entry"
	factsgithub "github.com/wloophq/wloop/internal/adapter/facts/github"
	factsession "github.com/wloophq/wloop/internal/adapter/facts/session"
	factworkspace "github.com/wloophq/wloop/internal/adapter/facts/workspace"
	"github.com/wloophq/wloop/internal/adapter/gitcheck"
	"github.com/wloophq/wloop/internal/adapter/orca"
	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/dossiers"
	coregtd "github.com/wloophq/wloop/internal/core/gtd"
	"github.com/wloophq/wloop/internal/core/port"
	coresweep "github.com/wloophq/wloop/internal/core/sweep"
	"github.com/wloophq/wloop/internal/core/turns"
	coreusage "github.com/wloophq/wloop/internal/core/usage"
	"github.com/wloophq/wloop/internal/core/views"
	"github.com/wloophq/wloop/internal/core/workspaces"
	"github.com/wloophq/wloop/internal/domain/dossier"
	"github.com/wloophq/wloop/internal/domain/pr"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/gitx"
	"github.com/wloophq/wloop/internal/source"
	"github.com/wloophq/wloop/internal/workspace"
	wscd "github.com/wloophq/wloop/internal/workspace/claudedesktop"
	wsorca "github.com/wloophq/wloop/internal/workspace/orca"
)

// Defaults is the config a file overrides: config's defaults with the agent
// and the workspace managers this build picks.
func Defaults() config.Config {
	c := config.Defaults()
	c.Agent = "claude"
	c.WorkspaceManagers = []string{wscd.Name, "orca"}
	return c
}

func defaultAgents() map[string]agentEntry {
	return map[string]agentEntry{"claude": {
		Kind:     claude.Agent{Sh: claude.Shell, AskSh: claude.Exec, UserHome: config.Home()},
		Home:     claude.NewHomeKind(claude.Shell),
		Protocol: claude.Protocol{},
	}}
}

func defaultUsage(userHome string) map[string]coreusage.Reader {
	return map[string]coreusage.Reader{"claude": claude.Usage{UserHome: userHome}}
}

// transcriptPrompts reads the user's prompts from the transcripts the hooks
// stored, through lookup.
func transcriptPrompts(lookup func(ctx context.Context, agentSession string) (path, kind string, err error)) turns.Prompts {
	return claude.Prompts{UserHome: config.Home(), Transcript: lookup}
}

// dossierFiles keeps the dossiers under the state directory; the adapter meters its own git calls.
func (e *Env) dossierFiles(p dossier.Policy) dossiers.Files {
	return adossier.Files{StateDir: e.StateDir, Home: e.Home, MaxFile: p.MaxFile, Now: e.Now}
}

// dossierPages reads and writes the dossiers' single files for the turn checks.
func (e *Env) dossierPages(p dossier.Policy) dossiers.Pages {
	return adossier.Files{StateDir: e.StateDir, Home: e.Home, MaxFile: p.MaxFile, Now: e.Now}
}

// stateCheckout is the repository checkout holding the state directory, or "".
func (e *Env) stateCheckout() string { return adossier.Checkout(e.StateDir, e.Home) }

// transcriptDir is where the agent keeps the transcripts of the sessions run in worktree; home
// "" is the user's default agent home.
func (e *Env) transcriptDir(home, worktree string) string {
	if home == "" {
		home = claude.DefaultHome(e.Home)
	}
	return claude.ProjectDir(home, worktree)
}

func defaultManagers() map[string]ManagerEntry {
	return map[string]ManagerEntry{"orca": orcaManager(map[string]string{}), wscd.Name: desktopManager(config.StateDir(), config.Home())}
}

// desktopWorktrees is where Claude Desktop's worktrees live: under the state
// directory, since Desktop garbage-collects its own .claude/worktrees.
func desktopWorktrees(stateDir string) string { return filepath.Join(stateDir, "worktrees") }

func desktopManager(stateDir, userHome string) ManagerEntry {
	root := desktopWorktrees(stateDir)
	return ManagerEntry{Display: "Claude Desktop", SessionVar: wscd.HandleVar, Session: wscd.SessionEnv{Root: root},
		Control: wscd.Control, ControlPaths: wscd.ControlPaths(userHome),
		Ports:     desktopPorts(stateDir),
		Advice:    []Advice{{Name: wscd.AutoFix.Name, Info: wscd.AutoFix.Info, Fix: wscd.AutoFix.Fix}},
		Available: func(ctx context.Context) error { return wscd.New(root, userHome).Available(ctx) },
		New: func() workspace.Manager {
			m := wscd.New(root, userHome)
			return workspace.NewCached(m, m, time.Duration(Defaults().Sweep.Fast))
		}}
}

// desktopPorts points the cached manager's reloads at ctx; Desktop has no board.
func desktopPorts(string) func(context.Context, workspace.Manager, *inventory.Store, config.Config, *change.Log) (managerSessions, []views.Sink, error) {
	return func(ctx context.Context, ws workspace.Manager, _ *inventory.Store, _ config.Config, _ *change.Log) (managerSessions, []views.Sink, error) {
		a, err := acd.Ports(ws)
		if err != nil {
			return nil, nil, err
		}
		if sm, ok := ws.(workspace.Snapshots); ok {
			sm.Snapshot().SetBase(ctx)
		}
		return a, nil, nil
	}
}

// checkouts are what the workspace removal guards read of a checkout.
func checkouts(g gitx.Git) (workspaces.Remote, workspaces.Tree, workspaces.Files) {
	c := gitcheck.Git{G: g}
	return c, c, gitcheck.Files{}
}

// defaultFacts are the facts providers that are not sources: the agent
// sessions and the workspaces, the workspace manager's resources the loop
// manages.
func defaultFacts(sessionFacts, workspaceFacts automation.Units) map[unit.ProviderID]automation.Units {
	return map[unit.ProviderID]automation.Units{unit.Session: sessionFacts, unit.Workspace: workspaceFacts}
}

// sessionFacts is the session facts provider over the daemon's turns; nil
// without them or when the manager keeps no workspace snapshot.
func (e *Env) sessionFacts() automation.Units {
	snap := e.planSnapshot()
	if e.Turns == nil || e.Store == nil || snap == nil {
		return nil
	}
	d := factsession.Deps{Turns: e.Turns, Snapshot: snap, PRs: inventory.ByUnit{Store: e.Store}, Transcript: e.transcriptOf, ClosedByManager: e.closedByManager,
		Cfg: e.currentConfig, Clock: clockFunc(func() time.Time { return e.Now() })}
	if e.changes != nil {
		d.Changes = e.changes
	}
	return factsession.New(d)
}

// workspaceFacts is the workspace facts provider over the workspace
// snapshot; nil when the manager keeps none.
func (e *Env) workspaceFacts() automation.Units {
	snap := e.planSnapshot()
	if snap == nil || e.Store == nil {
		return nil
	}
	d := factworkspace.Deps{Snapshot: snap, Sessions: e.planSessions(), PRs: e.planInputs(), Overrides: inventory.ByUnit{Store: e.Store}, Books: e.planBooks(),
		Git: factworkspace.LocalGit{R: e.Git.R}, Disk: factworkspace.OS{}, Cfg: e.currentConfig, Clock: clockFunc(func() time.Time { return e.Now() })}
	if e.changes != nil {
		d.Changes = e.changes
	}
	return factworkspace.New(d)
}

// siblingFacts is a workspace facts provider sharing f's checkout reads with
// a Seen of its own; nil, so the engine builds its own, for any other f.
func siblingFacts(f automation.Units) automation.Units {
	if p, ok := f.(*factworkspace.Provider); ok {
		return p.Sibling()
	}
	return nil
}

// candidatesOf is the rounds the github module's facts plan; nil when no
// module offers them.
func candidatesOf(mods []source.Module) roundCandidates {
	for _, m := range mods {
		if p, ok := m.Facts.(*factsgithub.Provider); ok {
			return githubCandidates{p}
		}
	}
	return nil
}

type githubCandidates struct{ p *factsgithub.Provider }

func (g githubCandidates) Candidate(ctx context.Context, u unit.Unit, r string) (roundCandidate, bool, error) {
	c, ok, err := g.p.Candidate(ctx, u, r)
	return roundCandidate(c), ok, err
}

func sessionAndWorkspaceCodecs() []unit.Codec {
	return []unit.Codec{dturn.SessionCodec{}, wsdomain.Codec{}}
}

const orcaSink = "orca"

func orcaManager(identities map[string]string) ManagerEntry {
	return ManagerEntry{Display: "Orca", Binary: "orca", SessionVar: wsorca.HandleVar, Session: wsorca.SessionEnv{},
		Sink: orcaSink, Columns: map[string]string{
			config.ColumnWorking: "reviewing", config.ColumnWorkingFallback: "in-review", config.ColumnNeedsMe: "needs-me",
			config.ColumnReviewed: "reviewed", config.ColumnReady: "ready-to-merge",
			config.ColumnInProgress: "in-progress", config.ColumnInReview: "in-review", config.ColumnCompleted: "completed",
		},
		Ports: orcaPorts, Homes: (&wsorca.Manager{Identities: identities}).ProfileHomes,
		New: func() workspace.Manager {
			m := wsorca.New()
			m.Identities = identities
			return workspace.NewCached(m, m, time.Duration(Defaults().Sweep.Fast))
		}}
}

// orcaPorts also points the cached manager's reloads at ctx and notes each moved
// workspace's units under TopicAgents in changes.
func orcaPorts(ctx context.Context, ws workspace.Manager, store *inventory.Store, cfg config.Config, changes *change.Log) (managerSessions, []views.Sink, error) {
	links := func(ctx context.Context) (map[pr.Ref]orca.Link, error) {
		prs, err := store.ListPRs(ctx, true)
		if err != nil {
			return nil, err
		}
		out := map[pr.Ref]orca.Link{}
		for _, p := range prs {
			if p.Workspace.ID != "" && cmp.Or(p.Workspace.Manager, ws.Name()) == ws.Name() {
				out[pr.Ref(p.Ref)] = orca.Link{WorkspaceID: p.Workspace.ID, Review: p.Workspace.IsReview}
			}
		}
		return out, nil
	}
	col := func(status string) string { return cfg.Column(orcaSink, status) }
	a, err := orca.Ports(ws, orca.Columns{ReviewStatus: col(config.ColumnWorking), ReviewFallbackStatus: col(config.ColumnWorkingFallback),
		NeedsMeStatus: col(config.ColumnNeedsMe), ReviewedStatus: col(config.ColumnReviewed),
		InProgressStatus: col(config.ColumnInProgress), InReviewStatus: col(config.ColumnInReview), ReadyStatus: col(config.ColumnReady), CompletedStatus: col(config.ColumnCompleted), AuthorColumns: true}, links)
	if err != nil {
		return nil, nil, err
	}
	if sm, ok := ws.(workspace.Snapshots); ok {
		sm.Snapshot().SetBase(ctx)
		if changes != nil {
			a.WatchAgents(ctx, func(units []unit.Unit) {
				if units == nil {
					changes.NoteAll(port.TopicAgents)
					return
				}
				changes.Note(port.TopicAgents, units...)
			})
		}
	}
	return a, []views.Sink{{ViewSink: a, Placements: a}}, nil
}

// planSnapshot is the snapshot merged from every manager that the facts read;
// nil when a manager keeps none.
func (e *Env) planSnapshot() coresweep.Snapshot {
	if e.WS.Snapshots() {
		return e.WS
	}
	return nil
}

// planSessions are the open sessions the workspace facts count; nil without turns.
func (e *Env) planSessions() factworkspace.Sessions {
	if e.Turns == nil {
		return nil
	}
	return e.Turns.Registry
}

func (e *Env) planBooks() coresweep.Books {
	if e.Turns == nil {
		return nil
	}
	return e.Turns.Store
}

// planInputs are the stored pull requests the facts read.
func (e *Env) planInputs() coresweep.Inputs {
	if e.planReads != nil {
		return e.planReads
	}
	if e.Store == nil {
		return nil
	}
	return storeInputs{e.Store}
}

// transcriptOf is the transcript file the hooks stored for the agent session;
// "" outside serve, which keeps no store.
func (e *Env) transcriptOf(ctx context.Context, agentSession string) (string, error) {
	if e.served == nil || e.served.db == nil {
		return "", nil
	}
	path, _, err := e.served.db.AgentTranscript(ctx, agentSession)
	return path, err
}

// closedByManager asks the session's manager, when it keeps its own record of
// sessions, whether the user closed it there; a manager that keeps none never
// closes one, and one whose record cannot be read closes none.
func (e *Env) closedByManager(ctx context.Context, manager, session, lastHandle string) (bool, error) {
	m, ok := e.WS.Named(manager)
	if !ok {
		return false, nil
	}
	st, ok := workspace.Capability[workspace.SessionStore](m)
	if !ok {
		return false, nil
	}
	closed, err := st.Closed(ctx, cmp.Or(lastHandle, session))
	if err != nil {
		return false, nil
	}
	return closed, nil
}

// entryFacts is the entry subject's facts provider, reading the config the
// daemon holds at each read; ended says whether a unit has ended.
func (e *Env) entryFacts(db *sqlite.DB, ended func(context.Context, unit.Unit) (bool, error)) *factentry.Provider {
	enabled := func(p unit.ProviderID) bool { return e.currentConfig().PluginEnabled(string(p)) }
	return factentry.New(factentry.Deps{
		Entries: db.Entries(), Items: db.Items(), Drafts: db.Drafts(),
		Cards:   coregtd.CardFacts{Cards: db.Cards(), Enabled: enabled},
		Enabled: enabled,
		Settings: func() factentry.Settings {
			cfg := e.currentConfig()
			return factentry.Settings{StaleDays: cfg.GTD.StaleWaitingDays, RenudgeAfter: time.Duration(cfg.Drafts.RepeatAfter)}
		},
		Ended: ended,
		Clock: clockFunc(func() time.Time { return e.Now() }),
	})
}
