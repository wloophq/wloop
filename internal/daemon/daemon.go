// Package daemon is the resident process behind the serve command: it runs the
// collector and the sweep on a schedule and serves the API on a Unix socket
// and on a token-guarded localhost port.
package daemon

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	dmessage "github.com/wloophq/wloop/internal/domain/message"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/change"
	coregtd "github.com/wloophq/wloop/internal/core/gtd"
	"github.com/wloophq/wloop/internal/core/ingest"
	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/core/messages"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	coreusage "github.com/wloophq/wloop/internal/core/usage"
	coreviews "github.com/wloophq/wloop/internal/core/views"
	"github.com/wloophq/wloop/internal/domain/card"
	dgh "github.com/wloophq/wloop/internal/domain/github"
	"github.com/wloophq/wloop/internal/domain/round"
	"github.com/wloophq/wloop/internal/domain/unit"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/meter"
	"github.com/wloophq/wloop/internal/procgroup"
	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
)

// ShutdownGrace is how long the daemon waits for open requests to finish when
// it stops.
const ShutdownGrace = 5 * time.Second

// groupsFile, in the state directory, lists the process groups of the
// children still running, for the next start to kill after a crash.
const groupsFile = "groups"

const (
	tokenCookie  = "loop_token"
	fastTimeout  = 10 * time.Minute
	slowTimeout  = 30 * time.Minute
	sweepTimeout = 15 * time.Minute
	sweepSettle  = 5 * time.Second
	briefTimeout = 15 * time.Minute
	usageTimeout = 10 * time.Minute
	minTokenLen  = 26 // rand.Text's length

	retainEvery   = time.Hour
	retainTimeout = 2 * time.Minute

	mcpReadTimeout = 30 * time.Second
	mcpIdleTimeout = 2 * time.Minute
)

var ErrAlreadyRunning = errors.New("daemon already running")

var (
	appStates          = []string{"running"}
	notificationStates = []string{"authorized", "provisional", "denied", "not-determined", ""}
)

// AppState holds the app's last report; nil until the app reports.
type AppState struct {
	mu sync.Mutex
	st *api.AppStatus
}

// Set stores st and reports whether it differs from the last report.
func (a *AppState) Set(st api.AppStatus) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	changed := a.st == nil || *a.st != st
	a.st = &st
	return changed
}

func (a *AppState) Get() *api.AppStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.st == nil {
		return nil
	}
	st := *a.st
	return &st
}

func appWarning(a api.AppStatus) string {
	if a.Notifications == "denied" {
		return "app: notifications are off for " + brand.Name + "; turn them on in System Settings > Notifications > " + brand.Name
	}
	return ""
}

// Pauses keeps the pause of every workflow that does not observe and of board moves.
type Pauses interface {
	Pause(ctx context.Context) (round.Pause, error)
	SetPause(ctx context.Context, p round.Pause) error
}

type Deps struct {
	// Units parses the keys of the unit routes; the providers it knows are
	// the daemon's.
	Units *unit.Registry
	// Ingest are the sources' ingestion jobs.
	Ingest []IngestJob
	// RowProviders build the read model's providers with the daemon's row
	// deps, one per source.
	RowProviders []func(readmodel.RowDeps) readmodel.Provider
	// The inventory, one field per role; a nil role answers as a daemon
	// with no store does.
	Rows      prrow.Store
	PRs       PullRequests
	Events    EventLog
	Overrides Overrides
	Meta      Metas
	Records   Records
	// GitAccess keeps how git network operations went per host; nil keeps
	// them in Store.
	GitAccess *GitAccessLog
	Meter     *meter.Registry // nil counts nothing
	// Fallbacks is how many workspace manager calls went to its CLI because
	// its runtime drifted; nil when it does not count them.
	Fallbacks func() int64
	Pauses    Pauses // nil reads as not paused and refuses a pause
	// Collector runs the slow job; nil registers no fast and slow jobs.
	Collector Collector
	// Track reads a pull request about to be tracked, wrapping a failed read
	// in port.ErrNotFound or port.ErrGitHub when it can tell which; nil
	// tracks without reading.
	Track func(ctx context.Context, u unit.Unit) (port.TrackRead, error)
	// Follows says whether a provider's source keeps its own follows: track
	// and untrack of its units go to Track and Untrack, never to the pull
	// request overrides. Nil follows none.
	Follows func(unit.ProviderID) bool
	Untrack func(ctx context.Context, u unit.Unit) error
	Sweep   Sweeper // nil registers no sweep job
	Actions Actions // nil answers 503 to delegated actions
	// Intents is the registry as the profile defines it now, read on every
	// rows rebuild and action; nil serves the bundled intents.
	Intents func() *intentload.Loaded
	// Plan is the workflows' plan; nil answers 503 to the plan routes.
	Plan Plan
	// Pending are the pending automatic rounds the rows show; nil shows none.
	Pending readmodel.Pending
	Changes *change.Log // nil notes nothing
	// Snapshot pins the read model's loads to one read transaction; nil
	// loads without one.
	Snapshot readmodel.Snapshotter
	// Brief, BriefStatus and BriefViews are the brief queue's roles; nil
	// when brief.enabled is false.
	Brief       BriefQueue
	BriefStatus BriefHealth
	BriefViews  BriefReads
	Usage       *coreusage.Service // nil registers no usage job
	Explorer    Explorer           // nil answers 503 to explore
	// Retain drops the store's rows past their retention and says what it
	// dropped; nil registers no retain job.
	Retain func(ctx context.Context) (summary string, err error)
	// Budget is the GitHub client's budget; nil reads as unknown.
	Budget Budget
	// Turns, Sessions and Views are the turn and view use cases and the
	// sessions turns are typed into; nil answers 503 from the routes that
	// need them.
	Turns    *turns.Service
	Sessions turns.Sessions
	Views    *coreviews.Service
	// Agents are the agents' states as the workspace snapshot last read
	// them, never a call to the manager; nil reads as unknown, so a started
	// turn's session is never told waiting.
	Agents readmodel.Agents
	// Activity reads what the loop spent on each provider's units for their
	// rows; nil shows none.
	Activity readmodel.Activities
	// TurnByHand runs a turn the user asked for in the handle's session, ""
	// outside Orca; nil answers 503 to AgentAPI's by-hand turns.
	TurnByHand func(ctx context.Context, r api.ByHandRequest, handle string) (api.ByHandResult, error)
	// Intakes keeps the turns users ask for: review, address and run are
	// accepted at once and start once the first turn's brief is ready. nil
	// runs them at once and answers when they end.
	Intakes intake.Store
	// IntakeBriefs tells an intake whether its brief is ready; nil starts
	// every intake without one, as with briefs off.
	IntakeBriefs intake.Briefs
	// ByHandRef is the pull request a turn by hand is about; with Intakes
	// it takes the turn through an intake, nil runs it at once.
	ByHandRef func(ctx context.Context, r api.ByHandRequest, handle string) (unit.Unit, error)
	// BriefsUnavailable is why briefs are on in the config but the daemon
	// cannot run them; "" when it can or they are off.
	BriefsUnavailable string
	// Facts reads what a pull request's guide is matched against, for
	// profile show <pr-url>; nil answers 503.
	Facts func(ctx context.Context, u unit.Unit) (api.Facts, error)
	// SessionWait is how long a turn by hand waits for its brief; 0 is
	// brief.SessionWait.
	SessionWait time.Duration
	// LoopMu serializes every loop write: sweep, review, take over, address, run.
	// Shared across restarts of the server in one process; nil makes one.
	LoopMu *sync.Mutex
	// App is the menu bar's status, shared across restarts of the server
	// like LoopMu; nil makes one.
	App    *AppState
	Cfg    config.Config
	Viewer func(ctx context.Context) (login string, teams []string, err error)
	// Forge is the plugin of the pull requests the daemon's viewer,
	// explorer, intakes and budget serve.
	Forge    unit.ProviderID
	Now      func() time.Time
	Version  string
	Log      func(format string, args ...any)
	StateDir string
	Socket   string
	LockPath string
	InfoPath string
	// Restart is the command that restarts the daemon, which a refused
	// client of another API major is told to run.
	Restart string
	// Store is the open store's version and last backup, for the health.
	Store *api.StoreStatus
	// ConfigWarning is the health warning about config.toml drifting from the
	// running config, "" for none; nil never warns.
	ConfigWarning func() string
	// WorkspaceManager names the workspace managers in messages, such as
	// "Claude Desktop or Orca".
	WorkspaceManager string
	// WorkspaceManagers name the enabled managers for the health answer, as
	// displayed; ManagerNames are their registry names, and a hook naming
	// another one is refused.
	WorkspaceManagers []string
	ManagerNames      []string
	// NotifierStart is the command that starts the menu bar; "" when this
	// daemon expects none, and then its absence is no warning.
	NotifierStart string
	// Notices are what the workflows raise for the user, streamed to the
	// event subscribers as they come; nil raises none.
	Notices Notices

	// FailedSteps names the failed steps of a GitHub Actions job; nil names
	// none.
	FailedSteps func(ctx context.Context, owner, repo string, jobID int64) ([]string, error)

	// MCP serves agents' MCP sessions on 127.0.0.1:MCPPort; nil listens on
	// no MCP port.
	MCP http.Handler
	// ReadText words the answers an agent's turn answer command reads, as inbox_read does, read of message
	// id or of every unread answer when 0; nil prints them bare.
	ReadText func(ctx context.Context, convs []dmessage.Conversation, id int64) string
	MCPPort  int

	// Startup reconciles the store and the views; Run holds
	// UserAPI until it returns. nil serves at once with no report.
	Startup func(ctx context.Context) (api.ReconcileReport, error)
	// Homes checks the agent homes; Run checks them at start, the slow job,
	// doctor, a session home no check covered and a health read of a result
	// older than HomesMaxAge check them again, and health reads the last
	// result. nil reports none.
	Homes func(ctx context.Context) ([]api.HomeFinding, error)
	// DryRunTurn stores the review turn of a profile try; nil answers 503.
	DryRunTurn func(ctx context.Context, r api.DryRunTurnRequest) (api.DryRunTurn, error)
	// Doctor runs doctor's checks that read the loop's state; nil runs none
	// but the reconcile's.
	Doctor func(ctx context.Context, q api.DoctorQuery) (api.DoctorReport, error)
	// Plugins are every compiled-in plugin; PluginEnabled nil: all enabled.
	Plugins       []api.Plugin
	PluginEnabled func(unit.ProviderID) bool
	// Runtimes are the enabled plugins' streams, jobs and health.
	Runtimes []PluginRuntime
	// Items is the items ledger; nil answers 503 to the items routes.
	Items ItemsLedger
	// Cards are the units' stored cards; nil serves none.
	Cards card.Store
	// CardsJob is told each published row change; nil runs none.
	CardsJob CardsJob
	// GTD is the entries service; nil answers 503 to the entries routes and runs no Sync. Messages are the
	// Inbox messages, whose entries' verbs it registers on GTD.
	GTD      *coregtd.Service
	Messages *messages.Service
	// Drafts are the outbox's drafts the notifications read; nil: no draft notifies.
	Drafts Drafts
	// GTDMe resolves the user's identity over the enabled plugins and sets it on GTD, saying whether it grew;
	// the Sync worker calls it before each batch until it succeeds. nil: GTD's identity stands.
	GTDMe func(ctx context.Context) (grew bool, err error)
	// GTDUnits are the units a full Sync covers: those with an open item or a live entry; nil: none.
	GTDUnits func(ctx context.Context) ([]unit.Unit, error)
	// Outbox keeps the drafts; nil answers 503 to the draft routes. SendDrafts runs its sender, which
	// publishes the approved drafts.
	Outbox     *outbox.Service
	SendDrafts bool
	// Headless starts headless turns; nil answers 503 to the act route.
	Headless Headless
	// Placer says where a session on a unit of a provider other than GitHub runs; nil offers the
	// session mode unchecked.
	Placer Placer
}

// Placer says where a session on u would run, or why none can.
type Placer interface {
	SessionPlace(ctx context.Context, u unit.Unit) (string, error)
}

// Budget is the GitHub budget as the client keeps it.
type Budget interface {
	Allow(p dgh.Priority) bool
	Budget() map[dgh.Resource]dgh.Limit
}

// background tags a job's GitHub reads below the user's reserve.
func background(run runFunc) runFunc {
	return func(ctx context.Context) (string, string, error) {
		return run(dgh.WithPriority(ctx, dgh.PriorityBackground))
	}
}

type Server struct {
	deps      Deps
	jobs      map[string]*job
	token     string
	warnings  *warnings
	grace     time.Duration
	hub       *hub
	model     *readmodel.Model
	modelOnce sync.Once
	viewer    viewerCache
	app       *AppState
	steps     stepCache
	exploring sync.Mutex
	users     sync.Map // lowercase login -> whether GitHub knows it as a user
	// draftWheres keeps, by draft id, the destination its publisher resolved or when it last failed to.
	draftWheres sync.Map
	// whereLookup and wherePage override draftWhereTimeout and draftWhereBudget; zero keeps them.
	whereLookup, wherePage time.Duration
	// exploreAttempt bounds one GitHub search; exploreRetryPause separates
	// the two tries.
	exploreAttempt, exploreRetryPause time.Duration
	// overrideMu serializes every read-modify-write of an override.
	overrideMu sync.Mutex
	// pauseMu serializes every read-modify-write of the pause.
	pauseMu sync.Mutex
	// track reads a pull request before it is tracked; nil tracks unread.
	track             func(ctx context.Context, u unit.Unit) (port.TrackRead, error)
	reconcile         reconcileState
	homes             homeCache
	situationsFailing atomic.Bool
	mutesFailing      atomic.Bool
	// intakesExpiredAt is the minute the read model's keeper last expired
	// the session intakes; only the keeper touches it.
	intakesExpiredAt time.Time

	// forceable keeps, by pull request, the last remove refusal force gets past.
	forceable sync.Map

	intakes       *intake.Service
	intakeWaiters intakeWaiters
	byHand        sync.Map // intake seq -> byHandOutcome
	sessionAsks   sessionAsks
	sessionWait   time.Duration
	// life ends when Run stops; the intakes are driven on it.
	life     context.Context
	stopLife context.CancelFunc
	driving  sync.WaitGroup
	// streamClock times the plugins' stream restarts.
	streamClock jobClock
	// draftClock times the draft sender's waits.
	draftClock jobClock
	// draftMu holds a draft's revision check and its write together.
	draftMu sync.Mutex
}

func New(d Deps) *Server {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Log == nil {
		d.Log = func(string, ...any) {}
	}
	def := config.Defaults().Sweep
	if d.Cfg.Sweep.Fast <= 0 {
		d.Cfg.Sweep.Fast = def.Fast
	}
	if d.Cfg.Sweep.Slow <= 0 {
		d.Cfg.Sweep.Slow = def.Slow
	}
	if d.Restart == "" {
		d.Restart = "restart " + brand.Command + " serve"
	}
	if d.LoopMu == nil {
		d.LoopMu = &sync.Mutex{}
	}
	if d.App == nil {
		d.App = &AppState{}
	}
	if d.GitAccess == nil && d.Meta != nil {
		d.GitAccess = &GitAccessLog{Store: d.Meta}
	}
	s := &Server{deps: d, jobs: map[string]*job{}, token: rand.Text(), warnings: &warnings{}, grace: ShutdownGrace, hub: newHub(), app: d.App,
		track: d.Track, exploreAttempt: exploreAttempt, exploreRetryPause: exploreRetryPause,
		reconcile: reconcileState{ready: make(chan struct{}), done: make(chan struct{}), wait: ReconcileWait}, sessionWait: cmp.Or(d.SessionWait, brief.SessionWait),
		streamClock: wallClock{}, draftClock: wallClock{}}
	s.life, s.stopLife = context.WithCancel(meter.WithRegistry(context.Background(), d.Meter))
	s.intakes = s.newIntakes(d)
	s.registerMessages()
	s.registerOutbox()
	if d.Startup == nil {
		close(s.reconcile.ready)
		close(s.reconcile.done)
	}
	ingested := make(chan struct{}, 1)
	for _, in := range d.Ingest {
		name := ingestJobName(in.ID)
		s.jobs[name] = newJob(name, in.Job.Every, fastTimeout, background(func(ctx context.Context) (string, string, error) {
			defer func() {
				select {
				case ingested <- struct{}{}:
				default:
				}
			}()
			return ingest.RunJob(ctx, in.Job, s.deps.Now())
		}))
	}
	if c := d.Collector; c != nil {
		every := time.Duration(d.Cfg.Sweep.Fast)
		if len(d.Ingest) > 0 {
			// The end of each ingestion run wakes it; its own period is a watchdog.
			every = time.Duration(d.Cfg.Sweep.Slow)
		}
		s.jobs["fast"] = newJob("fast", every, fastTimeout, background(func(ctx context.Context) (string, string, error) {
			s.noteConfigStale()
			phase(ctx, "turns", s.reconcileTurns)
			phase(ctx, "untrack", s.untrackFinished)
			return "", "", nil
		}))
		s.jobs["fast"].wakeOn(ingested)
		s.jobs["slow"] = newJob("slow", time.Duration(d.Cfg.Sweep.Slow), slowTimeout, background(func(ctx context.Context) (string, string, error) {
			summary, skipped, err := c.SlowJob(ctx)
			s.refreshHomes(ctx)
			s.untrackFinished(ctx)
			return summary, skipped, err
		}))
	}
	if sw := d.Sweep; sw != nil {
		s.jobs["sweep"] = s.sweepJob(sw)
		if d.Plan != nil {
			s.jobs["due"] = s.dueJob(d.Plan)
		}
	}
	if b := d.Brief; b != nil {
		s.jobs["brief"] = newJob("brief", time.Duration(d.Cfg.Sweep.Slow), briefTimeout, background(func(ctx context.Context) (string, string, error) {
			summary, err := b.Run(ctx)
			return summary, "", err
		}))
		s.jobs["brief"].wakeOn(b.Due())
	}
	if u := d.Usage; u != nil {
		s.jobs["usage"] = newJob("usage", time.Duration(d.Cfg.Sweep.Slow), usageTimeout, func(ctx context.Context) (string, string, error) {
			summary, err := u.Run(ctx)
			return summary, "", err
		})
	}
	if r := d.Retain; r != nil {
		s.jobs["retain"] = newJob("retain", retainEvery, retainTimeout, func(ctx context.Context) (string, string, error) {
			summary, err := r(ctx)
			return summary, "", err
		})
	}
	s.registerPluginJobs()
	return s
}

// ingestJobName names the ingestion job of a provider.
func ingestJobName(p unit.ProviderID) string { return "ingest:" + string(p) }

// sweepJob runs sw on every change of its inputs, at its deadline, and
// once per slow period as a watchdog.
func (s *Server) sweepJob(sw Sweeper) *job {
	var planned, seen atomic.Uint64
	skips := 0
	var j *job
	j = newJob("sweep", time.Duration(s.deps.Cfg.Sweep.Slow), sweepTimeout, background(func(ctx context.Context) (string, string, error) {
		defer func() {
			if ctx.Err() == nil {
				s.expireMutesOrLog(ctx)
			}
		}()
		from := s.changeGen()
		summary, skipped, err := sw.Sweep(ctx, s.deps.LoopMu)
		planned.Store(uint64(from))
		seen.Store(uint64(max(from, sw.Seen())))
		due := sw.Due()
		if skipped != "" && err == nil {
			skips++
			due = earliest(due, j.deadline(), j.clock.Now().Add(min(j.retry<<min(skips-1, 10), j.every)))
		} else {
			skips = 0
		}
		j.at(due)
		if errors.Is(err, context.Canceled) && errors.Is(ctx.Err(), context.Canceled) {
			s.logf("sweep aborted (shutdown)")
			return summary, skipped, context.Canceled
		}
		return summary, skipped, err
	}))
	j.retry, j.settle = time.Duration(s.deps.Cfg.Sweep.Fast), sweepSettle
	j.stale = func() bool {
		log := s.deps.Changes
		if log == nil {
			return false
		}
		return log.Since(port.Generation(planned.Load()), port.SweepInputs&^port.TopicAgents).Empty() &&
			log.Since(port.Generation(seen.Load()), port.TopicAgents).Empty()
	}
	return j
}

// earliest is the earliest of the non-zero times, zero when none.
func earliest(ts ...time.Time) time.Time {
	var out time.Time
	for _, t := range ts {
		if !t.IsZero() && (out.IsZero() || t.Before(out)) {
			out = t
		}
	}
	return out
}

// wakeSweep wakes the sweep job on every note of its inputs until the
// returned cancel.
func (s *Server) wakeSweep() (cancel func()) {
	j, ok := s.jobs["sweep"]
	if !ok || s.deps.Changes == nil {
		return func() {}
	}
	poke, cancel := change.Poke(s.deps.Changes, port.SweepInputs)
	j.wakeOn(poke)
	return cancel
}

func (s *Server) changeGen() port.Generation {
	if s.deps.Changes == nil {
		return 0
	}
	return s.deps.Changes.Gen()
}

func (s *Server) expireMutesOrLog(ctx context.Context) {
	if err := s.expireMutes(ctx); err != nil {
		s.logf("expiring mutes: %v", err)
	}
}

// reconcileTurns closes the turns whose sessions stopped without a record
// and brings every view back to the turns.
func (s *Server) reconcileTurns(ctx context.Context) {
	if t := s.deps.Turns; t != nil {
		rep, err := t.Reconcile(ctx, false)
		for _, w := range rep.Warnings {
			s.logf("reconcile: %s", w)
		}
		if err != nil {
			s.logf("reconcile: %v", err)
		}
		s.deliverQueued(ctx, rep.Queued)
	}
	if v := s.deps.Views; v != nil {
		if _, err := v.Reconcile(ctx); err != nil {
			s.logf("views reconcile: %v", err)
		}
	}
}

// deliverQueued routes the queued answers no Stop hook delivered, such as
// those of an agent without the hook. A sweep holding the loop defers them to
// the next run.
func (s *Server) deliverQueued(ctx context.Context, units []unit.Unit) {
	if len(units) == 0 {
		return
	}
	ctx = s.readAheadQueued(ctx, units...)
	if !s.deps.LoopMu.TryLock() {
		return
	}
	defer s.deps.LoopMu.Unlock()
	if err := s.deps.Turns.Deliver(ctx, units...); err != nil {
		s.logf("queued answers: %v", err)
	}
}

func (s *Server) logf(format string, args ...any) { s.deps.Log(format, args...) }

// RunJob queues a run of the named job; false when no such job.
func (s *Server) RunJob(name string) bool {
	j, ok := s.jobs[name]
	if ok {
		j.trigger()
	}
	return ok
}

func (s *Server) jobStatuses() []api.JobStatus {
	out := make([]api.JobStatus, 0, len(s.jobs))
	for _, j := range s.jobs {
		out = append(out, j.status())
	}
	slices.SortFunc(out, func(a, b api.JobStatus) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// AuthHandler is Handler behind the token check, for the TCP port, which
// serves no AgentAPI and no AdminAPI.
func (s *Server) AuthHandler() http.Handler { return authHandler(s.token, s.Handler()) }

// authHandler serves h on the cockpit's TCP listener to a page holding
// token.
func authHandler(token string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, api.AgentPrefix) || strings.HasPrefix(r.URL.Path, api.AdminPrefix) {
			writeError(w, http.StatusNotFound, "no such endpoint "+r.URL.Path)
			return
		}
		if !loopbackHost(r) {
			http.Error(w, "unexpected Host "+r.Host, http.StatusMisdirectedRequest)
			return
		}
		if !safeMethod(r.Method) {
			if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
				http.Error(w, "cross-site request", http.StatusForbidden)
				return
			}
			// A cross-site form cannot set a custom header, and no CORS
			// preflight is answered: the cockpit's requests all carry it.
			if r.Header.Get(api.Header) == "" {
				http.Error(w, "a write needs the "+api.Header+" header", http.StatusForbidden)
				return
			}
		}
		q := r.URL.Query()
		if t := q.Get("token"); t != "" && validToken(token, t) {
			http.SetCookie(w, &http.Cookie{Name: tokenCookie, Value: t, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode}) //nolint:gosec // G124: served over plain HTTP on loopback, where Secure would drop the cookie
			q.Del("token")
			u := url.URL{Path: "/" + strings.TrimLeft(r.URL.Path, `/\`), RawQuery: q.Encode()}
			http.Redirect(w, r, u.RequestURI(), http.StatusSeeOther)
			return
		}
		if !authorized(token, r) {
			http.Error(w, "missing or invalid token", http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// loopbackHost says r names the cockpit as 127.0.0.1 or localhost, on the
// port it reached when the listener's address is known, so a page under
// another name that resolves here (DNS rebinding) is refused.
func loopbackHost(r *http.Request) bool {
	host, port, err := net.SplitHostPort(r.Host)
	if err != nil || (host != "127.0.0.1" && host != "localhost") {
		return false
	}
	local, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr)
	if !ok {
		return true
	}
	_, want, err := net.SplitHostPort(local.String())
	return err == nil && port == want
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

func validToken(token, t string) bool {
	return t != "" && subtle.ConstantTimeCompare([]byte(t), []byte(token)) == 1
}

func authorized(token string, r *http.Request) bool {
	c, err := r.Cookie(tokenCookie)
	return err == nil && validToken(token, c.Value)
}

func (s *Server) Run(ctx context.Context) error {
	d := s.deps
	if err := ensureStateDir(d.StateDir); err != nil {
		return err
	}
	unlock, err := LockFile(d.LockPath, ErrAlreadyRunning)
	if err != nil {
		return err
	}
	defer unlock()
	if err := procgroup.Track(filepath.Join(d.StateDir, groupsFile)); err != nil {
		s.logf("child process groups: %v", err)
	}
	if limit := len(syscall.RawSockaddrUnix{}.Path); len(d.Socket) >= limit {
		return fmt.Errorf("socket path %s is %d bytes, too long for a unix socket (under %d); use a shorter state directory", d.Socket, len(d.Socket), limit)
	}
	if err := claimSocket(d.Socket); err != nil {
		return err
	}

	unixLn, err := net.Listen("unix", d.Socket)
	if err != nil {
		return err
	}
	defer os.Remove(d.Socket)
	if err := os.Chmod(d.Socket, 0o600); err != nil {
		unixLn.Close()
		return err
	}
	tcpLn, err := listenTCP(d.Cfg.HTTP.Port, d.InfoPath)
	if err != nil {
		unixLn.Close()
		return err
	}
	mcpLn, err := s.listenMCP()
	if err != nil {
		unixLn.Close()
		tcpLn.Close()
		return err
	}
	if t := reusableToken(d.InfoPath); t != "" {
		s.token = t
	}
	if err := writeInfo(d.InfoPath, tcpLn.Addr().(*net.TCPAddr).Port, s.token); err != nil {
		unixLn.Close()
		tcpLn.Close()
		if mcpLn != nil {
			mcpLn.Close()
		}
		return err
	}

	ctx = meter.WithRegistry(ctx, d.Meter)
	base := func(net.Listener) context.Context { return meter.WithRegistry(context.Background(), d.Meter) }
	servers := []*http.Server{
		{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second, BaseContext: base},
		{Handler: s.AuthHandler(), ReadHeaderTimeout: 10 * time.Second, BaseContext: base},
	}
	listeners := []net.Listener{unixLn, tcpLn}
	if mcpLn != nil {
		servers = append(servers, &http.Server{Handler: scopedHandler(meter.Route("mcp"), d.MCP), BaseContext: base, ReadHeaderTimeout: 10 * time.Second, ReadTimeout: mcpReadTimeout, IdleTimeout: mcpIdleTimeout})
		listeners = append(listeners, mcpLn)
	}
	for _, srv := range servers {
		srv.RegisterOnShutdown(s.hub.close)
	}
	errc := make(chan error, len(servers))
	for i, ln := range listeners {
		go func() {
			if err := servers[i].Serve(ln); !errors.Is(err, http.ErrServerClosed) {
				errc <- err
			}
		}()
	}

	jctx, stopJobs := context.WithCancel(ctx)
	if d.Notices != nil {
		defer d.Notices.Follow(s.notify)()
	}
	var wg sync.WaitGroup
	if d.Homes != nil {
		wg.Go(func() {
			phase(jctx, meter.Loop("homes"), s.refreshHomes)
			s.note(port.TopicJobs)
		})
	}
	if d.Startup != nil {
		wg.Go(func() { s.runStartup(jctx) })
	}
	if s.intakes != nil {
		wg.Go(func() {
			select {
			case <-s.reconcile.done:
				phase(jctx, meter.Loop("intakes"), s.recoverIntakes)
			case <-jctx.Done():
			}
		})
	}
	defer s.wakeSweep()()
	defer s.wakeBrief()()
	wg.Go(func() { s.feedHealth(jctx) })
	wg.Go(func() {
		select {
		case <-s.reconcile.ready:
			s.feedRows(jctx)
		case <-jctx.Done():
		}
	})
	if d.GTD != nil {
		wg.Go(func() { s.feedEntries(jctx) })
		wg.Go(func() {
			select {
			case <-s.reconcile.ready:
				s.syncEntries(jctx)
			case <-jctx.Done():
			}
		})
	}
	if d.Outbox != nil && d.SendDrafts {
		wg.Go(func() {
			select {
			case <-s.reconcile.done:
				s.sendDrafts(jctx)
			case <-jctx.Done():
			}
		})
	}
	if d.CardsJob != nil {
		wg.Go(func() {
			select {
			case <-s.reconcile.ready:
				d.CardsJob.Run(jctx, s.readModel())
			case <-jctx.Done():
			}
		})
	}
	if os.Getenv(ReadModelCheckEnv) == "1" {
		wg.Go(func() {
			select {
			case <-s.reconcile.done:
				s.checkReadModelEvery(jctx, readModelCheckEvery)
			case <-jctx.Done():
			}
		})
	}
	s.startStreams(jctx, &wg)
	for _, j := range s.jobs {
		j.onEnd = s.jobEnded
		wg.Go(func() {
			select {
			case <-s.reconcile.done:
				j.loop(jctx, s.logf, s.jobDone)
			case <-jctx.Done():
			}
		})
	}
	s.logf("serving on %s and 127.0.0.1:%d", d.Socket, tcpLn.Addr().(*net.TCPAddr).Port)

	var serveErr error
	select {
	case <-ctx.Done():
	case serveErr = <-errc:
	}
	stopJobs()
	s.stopLife()
	sctx, cancel := context.WithTimeout(context.Background(), s.grace)
	defer cancel()
	for _, srv := range servers {
		if err := srv.Shutdown(sctx); err != nil {
			srv.Close()
		}
	}
	jobsDone := make(chan struct{})
	go func() { wg.Wait(); s.driving.Wait(); close(jobsDone) }()
	select {
	case <-jobsDone:
	case <-sctx.Done():
		var stuck []string
		for _, st := range s.jobStatuses() {
			if st.Running {
				stuck = append(stuck, st.Name)
			}
		}
		s.logf("shutdown: jobs still running after %v: %s", s.grace, strings.Join(stuck, ", "))
	}
	return serveErr
}

// listenMCP listens on the MCP port. A port it cannot take fails the start:
// every agent session sends its key to that port, so whoever holds it would
// get the key and the turns.
func (s *Server) listenMCP() (net.Listener, error) {
	d := s.deps
	if d.MCP == nil {
		return nil, nil
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(d.MCPPort)))
	if err != nil {
		return nil, fmt.Errorf("the MCP port 127.0.0.1:%d is taken (%w): stop what holds it, or set mcp.port in the configuration to a free port and run %s install again", d.MCPPort, err, brand.Command)
	}
	s.logf("mcp on 127.0.0.1:%d", d.MCPPort)
	return ln, nil
}

// claimSocket removes a socket left by a daemon that died; a socket that
// still answers belongs to a live daemon.
func claimSocket(path string) error {
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	t := client.UnixTransport(path)
	t.DisableKeepAlives = true
	hc := &http.Client{Timeout: time.Second, Transport: t}
	if resp, err := hc.Get("http://daemon/api/health"); err == nil {
		defer resp.Body.Close()
		var h struct {
			Version string `json:"version"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&h)
		return fmt.Errorf("%w: version %s answers on %s", ErrAlreadyRunning, h.Version, path)
	}
	return os.Remove(path)
}

// listenTCP listens on port; port 0 reuses the earlier run's port when it is
// free, so open cockpit tabs reconnect after a restart.
func listenTCP(port int, infoPath string) (net.Listener, error) {
	if port == 0 {
		if prev := previousPort(infoPath); prev > 0 {
			if ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(prev))); err == nil {
				return ln, nil
			}
		}
	}
	return net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
}

func ReadInfo(path string) (api.Info, error) {
	var info api.Info
	b, err := os.ReadFile(path)
	if err != nil {
		return info, err
	}
	err = json.Unmarshal(b, &info)
	return info, err
}

func previousPort(path string) int {
	info, err := ReadInfo(path)
	if err != nil || info.Port < 0 || info.Port > 65535 {
		return 0
	}
	return info.Port
}

// reusableToken returns the token of an earlier run, so open cockpit tabs
// survive a restart, when the info file is private to this user.
func reusableToken(path string) string {
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Mode().Perm() != 0o600 || !ownedByMe(fi) {
		return ""
	}
	info, err := ReadInfo(path)
	if err != nil || len(info.Token) < minTokenLen {
		return ""
	}
	return info.Token
}

func writeInfo(path string, port int, token string) error {
	b, err := json.Marshal(api.Info{Port: port, Token: token})
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
