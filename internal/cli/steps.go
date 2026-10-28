package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/wloophq/wloop/internal/core/automation"
	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/core/port"
	coresweep "github.com/wloophq/wloop/internal/core/sweep"
	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/workspaces"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/round"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/profile"
)

// kindNotifySelf is the event notify.self publishes: Key the workflow and
// the time, Data its text.
const kindNotifySelf = "notify_self"

func notifyKey(workflow string, at time.Time) string {
	return workflow + "@" + strconv.FormatInt(at.UnixNano(), 10)
}

func notifyWorkflow(key string) string {
	name, _, _ := strings.Cut(key, "@")
	return name
}

const stepClassify = "classify"

// stepDeps are what the builtin steps read and act through; newStepSet
// fills the removal ports the Env has.
type stepDeps struct {
	Units automation.Units
	// Planners plan the rounds of their plugin's units, by provider.
	Planners map[unit.ProviderID]roundPlanner
	// Overrides are the host's overrides a plan reads; nil reads none.
	Overrides overrideReader
	// Briefs is nil when briefs are off: every round goes without one.
	Briefs   briefPort
	Rounds   Rounds
	Changes  port.Changes
	Removal  removalPorts
	Classify classifyDeps
	// Drafts are what draft.propose reads; nil refuses it.
	Drafts *draftDeps
	// Props are what task.set writes; nil refuses it.
	Props coretasks.PropStore
	Pub   port.Publisher
	Log   func(format string, args ...any)
	// Headless starts turn.start's headless turns; Turns counts the
	// automated turns turns.per_day caps.
	Headless headlessStarter
	Turns    automatedTurns
}

// briefPort is the brief queue as a round asks it: Peek plans, Ready starts
// the brief a first round waits for.
type briefPort interface {
	Peek(ctx context.Context, u unit.Unit, first bool, need []string) (intake.Readiness, error)
	Ready(ctx context.Context, u unit.Unit, first bool, need []string) (intake.Readiness, error)
}

// removalPorts are the workspace removal's ports but its Rounds, which is
// the bound round state.
type removalPorts struct {
	Manager workspaces.Manager
	Remote  workspaces.Remote
	Tree    workspaces.Tree
	Files   workspaces.Files
	Fetched workspaces.FetchLog
	Guard   workspaces.SessionGuard
	// Snapshot lists the terminals the default guard reads; nil asks the
	// turns' sessions.
	Snapshot   coresweep.Snapshot
	Disposable func() []string
	Viewer     func() string
}

// stepSet is the builtin steps as the engine's Steps. The round state an
// applying sweep, a by-hand action or a due run loads is bound to it until
// saved; the steps write the round state only through it.
type stepSet struct {
	e        *Env
	d        stepDeps
	reg      map[string]stepEntry
	classify *classifier

	mu      sync.Mutex
	cur     *round.Store
	peek    *round.Store
	peekGen port.Generation
	looks   map[string]removeLook
}

var _ automation.Steps = (*stepSet)(nil)

func newStepSet(e *Env, d stepDeps) *stepSet {
	r := &d.Removal
	if r.Manager == nil && e.WS.Len() > 0 {
		r.Manager = e.WS
	}
	if r.Remote == nil || r.Tree == nil || r.Files == nil {
		r.Remote, r.Tree, r.Files = checkouts(e.Git)
		if e.Files != nil {
			r.Files = e.Files
		}
	}
	if r.Guard == nil {
		r.Guard = turnsGuard{e: e, snap: r.Snapshot}
	}
	if r.Disposable == nil {
		r.Disposable = func() []string {
			cfg, _ := e.loadConfig()
			return cfg.Cleanup.Disposable
		}
	}
	if r.Viewer == nil {
		r.Viewer = func() string {
			v, _ := e.storedViewer(context.Background())
			return v
		}
	}
	s := &stepSet{e: e, d: d, reg: stepRegistry(), looks: map[string]removeLook{}}
	s.classify = &classifier{s: s}
	return s
}

// stepCall is one step of an item on its unit.
type stepCall struct {
	st  workflow.Step
	u   unit.Unit
	why workflow.Why
}

func (c stepCall) arg(name string) string {
	v, _ := c.st.Args[name].(string)
	return v
}

// file is the file argument name of c's step names beside its workflow's
// manifest, its default when unset, read through the profile layers.
func (s *stepSet) file(c stepCall, name string) (string, error) {
	r, _ := builtin.StepArg(c.st.Use, name)
	rel := path.Join("workflows", c.why.Workflow, cmp.Or(c.arg(name), r.Default))
	f, err := profile.Resolve(profile.Chain(s.e.currentConfig()), rel)
	if err != nil {
		return "", fmt.Errorf("%s: %w", c.st.Use, err)
	}
	return string(f.Body), nil
}

// list is a list argument: []string as loaded, []any once stored.
func (c stepCall) list(name string) []string {
	switch v := c.st.Args[name].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, e := range v {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// stepEntry adapts one builtin; a nil note or dismiss writes nothing.
type stepEntry struct {
	check   func(s *stepSet, ctx context.Context, c stepCall, o automation.CheckOpts) (string, error)
	note    func(s *stepSet, ctx context.Context, c stepCall, refusal string) error
	run     func(s *stepSet, ctx context.Context, c stepCall, o automation.RunOpts) (string, error)
	dismiss func(s *stepSet, ctx context.Context, c stepCall) error
}

// stepRegistry adapts every step of builtin.Steps.
func stepRegistry() map[string]stepEntry {
	return map[string]stepEntry{
		"session.close":          {check: (*stepSet).closeCheck, run: (*stepSet).closeRun},
		"session.mark_dormant":   {check: (*stepSet).markDormantCheck, run: (*stepSet).markDormantRun},
		"turn.dismiss":           {check: (*stepSet).dismissTurnCheck, run: (*stepSet).dismissTurnRun},
		"answer.refuse":          {check: (*stepSet).refuseAnswerCheck, run: (*stepSet).refuseAnswerRun},
		"workspace.remove":       {check: (*stepSet).removeCheck, note: (*stepSet).removeNote, run: (*stepSet).removeRun},
		"workspace.unregister":   {check: (*stepSet).unregisterCheck, run: (*stepSet).unregisterRun},
		builtin.StepTurnStart:    {check: (*stepSet).startCheck, note: (*stepSet).startNote, run: (*stepSet).startRun, dismiss: (*stepSet).startDismiss},
		builtin.StepDraftPropose: draftProposeStep,
		stepClassify:             {check: (*stepSet).classifyCheck, run: (*stepSet).classifyRun},
		"notify.self":            {check: (*stepSet).notifyCheck, run: (*stepSet).notifyRun},
		builtin.StepTaskVerb:     {check: (*stepSet).taskVerbCheck, run: (*stepSet).taskVerbRun},
		builtin.StepTaskSet:      {check: (*stepSet).taskSetCheck, run: (*stepSet).taskSetRun},
		builtin.StepGateOpen:     {check: (*stepSet).gateOpenCheck, run: (*stepSet).gateOpenRun},
	}
}

func (s *stepSet) entry(use string) (stepEntry, error) {
	if e, ok := s.reg[use]; ok {
		return e, nil
	}
	return stepEntry{}, fmt.Errorf("no builtin step %q", use)
}

func (s *stepSet) Check(ctx context.Context, st workflow.Step, u unit.Unit, why workflow.Why, o automation.CheckOpts) (string, error) {
	e, err := s.entry(st.Use)
	if err != nil {
		return "", err
	}
	return e.check(s, ctx, stepCall{st, u, why}, o)
}

func (s *stepSet) Note(ctx context.Context, st workflow.Step, u unit.Unit, why workflow.Why, refusal string) error {
	e, err := s.entry(st.Use)
	if err != nil || e.note == nil {
		return err
	}
	return e.note(s, ctx, stepCall{st, u, why}, refusal)
}

func (s *stepSet) Run(ctx context.Context, st workflow.Step, u unit.Unit, why workflow.Why, o automation.RunOpts) (string, error) {
	e, err := s.entry(st.Use)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", automation.Transient{Err: err}
	}
	return e.run(s, ctx, stepCall{st, u, why}, o)
}

func (s *stepSet) Dismiss(ctx context.Context, st workflow.Step, u unit.Unit, why workflow.Why) error {
	e, err := s.entry(st.Use)
	if err != nil || e.dismiss == nil {
		return err
	}
	return e.dismiss(s, ctx, stepCall{st, u, why})
}

// states loads the round state the steps write through.
func (s *stepSet) states() automation.StateLoader { return roundStates{s} }

func (s *stepSet) bind(st *round.Store) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cur = st
}

func (s *stepSet) unbind(st *round.Store) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cur == st {
		s.cur = nil
	}
}

// bound is the round state loaded under the loop lock; nil outside.
func (s *stepSet) bound() *round.Store {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cur
}

var errNoRoundState = errors.New("no round state is loaded")

// rounds is the round state a check reads: the bound one for a by-hand
// action, which holds the loop lock, else a copy only read, loaded again
// once the round state changed.
func (s *stepSet) rounds(ctx context.Context, byHand bool) (*round.Store, error) {
	if st := s.bound(); byHand && st != nil {
		return st, nil
	}
	if s.d.Rounds == nil {
		return round.New(), nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.peek != nil && s.d.Changes != nil && s.d.Changes.Since(s.peekGen, port.TopicRounds).Empty() {
		return s.peek, nil
	}
	var gen port.Generation
	if s.d.Changes != nil {
		gen = s.d.Changes.Gen()
	}
	st, err := s.d.Rounds.Load(ctx)
	if err != nil {
		return nil, err
	}
	s.peek, s.peekGen = st, gen
	return st, nil
}

func (s *stepSet) facts(ctx context.Context, u unit.Unit) (workflow.Facts, error) {
	if s.d.Units == nil {
		return workflow.Facts{}, nil
	}
	return s.d.Units.Facts(ctx, u)
}

func (s *stepSet) logf(format string, args ...any) {
	if s.d.Log != nil {
		s.d.Log(format, args...)
	}
}

func factString(f workflow.Facts, name string) string {
	v, _ := f[name].(string)
	return v
}

func factBool(f workflow.Facts, name string) bool {
	v, _ := f[name].(bool)
	return v
}

func factInt(f workflow.Facts, name string) int64 {
	switch v := f[name].(type) {
	case int:
		return int64(v)
	case int64:
		return v
	}
	return 0
}

// guardRefusal is a refusal a use case returned at run time as the
// engine's: blocked and planned again, its attempts untouched.
func guardRefusal(err error) error {
	var tr turns.Refusal
	var wr workspaces.Refusal
	switch {
	case errors.As(err, &tr):
		return automation.Refused{Reason: tr.Reason}
	case errors.As(err, &wr):
		return automation.Refused{Reason: wr.Error()}
	}
	return err
}

func (s *stepSet) turns() (*turns.Service, error) {
	if s.e.Turns == nil {
		return nil, errors.New("no turns service: the daemon runs the steps")
	}
	return s.e.Turns, nil
}

func (s *stepSet) closeCheck(ctx context.Context, c stepCall, _ automation.CheckOpts) (string, error) {
	t, err := s.turns()
	if err != nil {
		return "", err
	}
	return t.CloseRefusal(ctx, c.u.Key)
}

func (s *stepSet) closeRun(ctx context.Context, c stepCall, _ automation.RunOpts) (string, error) {
	t, err := s.turns()
	if err != nil {
		return "", err
	}
	if err := t.CloseSession(ctx, c.u.Key, "closed by the workflow "+c.why.Workflow); err != nil {
		return "", guardRefusal(err)
	}
	return "closed " + c.u.Key, nil
}

func (s *stepSet) markDormantCheck(ctx context.Context, c stepCall, _ automation.CheckOpts) (string, error) {
	f, err := s.facts(ctx, c.u)
	if err != nil {
		return "", err
	}
	if !factBool(f, "session.process_gone") {
		return "the session's process is not gone", nil
	}
	return "", nil
}

func (s *stepSet) markDormantRun(ctx context.Context, c stepCall, _ automation.RunOpts) (string, error) {
	t, err := s.turns()
	if err != nil {
		return "", err
	}
	if err := t.MarkDormant(ctx, c.u.Key); err != nil {
		return "", guardRefusal(err)
	}
	return "marked " + c.u.Key + " dormant", nil
}

// openTurn is the turn turn.dismiss closes: the pull request's open turn,
// or the newest open or stopped turn sent to or started on the session.
func (s *stepSet) openTurn(ctx context.Context, c stepCall) (unit.Unit, int64, workflow.Facts, error) {
	f, err := s.facts(ctx, c.u)
	if err != nil {
		return unit.Unit{}, 0, nil, err
	}
	if !dturn.IsSession(c.u) {
		return c.u, factInt(f, "pr.open_turn_seq"), f, nil
	}
	t, err := s.turns()
	if err != nil {
		return unit.Unit{}, 0, nil, err
	}
	b, found, err := t.Store.BookOf(ctx, turns.Key{Session: c.u.Key})
	if err != nil || !found {
		return unit.Unit{}, 0, f, err
	}
	for i := len(b.Turns) - 1; i >= 0; i-- {
		tn := b.Turns[i]
		if (tn.State.Open() || tn.State.Stopped()) && (tn.Session == c.u.Key || tn.SentTo == c.u.Key) {
			return b.Unit, tn.Seq, f, nil
		}
	}
	return b.Unit, 0, f, nil
}

func (s *stepSet) dismissTurnCheck(ctx context.Context, c stepCall, _ automation.CheckOpts) (string, error) {
	_, seq, _, err := s.openTurn(ctx, c)
	if err != nil || seq == 0 {
		return "no open turn", err
	}
	return "", nil
}

func (s *stepSet) dismissTurnRun(ctx context.Context, c stepCall, _ automation.RunOpts) (string, error) {
	u, seq, f, err := s.openTurn(ctx, c)
	if err != nil {
		return "", err
	}
	if seq == 0 {
		return "", automation.Refused{Reason: "no open turn"}
	}
	reason := c.arg("reason")
	for _, name := range []string{"pr.state", "session.pr_state"} {
		if reason == "" {
			reason = strings.ToLower(factString(f, name))
		}
	}
	if reason == "" {
		reason = "dismissed by the workflow " + c.why.Workflow
	}
	t, err := s.turns()
	if err != nil {
		return "", err
	}
	if err := t.DismissTurn(ctx, u, seq, reason); err != nil {
		return "", guardRefusal(err)
	}
	return fmt.Sprintf("dismissed turn %d: %s", seq, reason), nil
}

func (s *stepSet) refuseAnswerCheck(ctx context.Context, c stepCall, _ automation.CheckOpts) (string, error) {
	f, err := s.facts(ctx, c.u)
	if err != nil || factInt(f, "pr.orphan_answer_id") == 0 {
		return "no queued answer", err
	}
	return "", nil
}

func (s *stepSet) refuseAnswerRun(ctx context.Context, c stepCall, _ automation.RunOpts) (string, error) {
	f, err := s.facts(ctx, c.u)
	if err != nil {
		return "", err
	}
	id := factInt(f, "pr.orphan_answer_id")
	if id == 0 {
		return "", automation.Refused{Reason: "no queued answer"}
	}
	reason := c.arg("reason")
	if reason == "" {
		reason = "its route is gone"
	}
	t, err := s.turns()
	if err != nil {
		return "", err
	}
	if err := t.RefuseAnswer(ctx, c.u, id, reason); err != nil {
		return "", guardRefusal(err)
	}
	return fmt.Sprintf("refused answer %d: %s", id, reason), nil
}

// target is the workspace a workspace step acts on and its pull request,
// read now.
func (s *stepSet) target(ctx context.Context, c stepCall) (wsdomain.Workspace, workspaces.PRInfo, error) {
	m := s.d.Removal.Manager
	if m == nil {
		return wsdomain.Workspace{}, workspaces.PRInfo{}, errors.New("no workspace manager")
	}
	all, err := m.List(ctx)
	if err != nil {
		return wsdomain.Workspace{}, workspaces.PRInfo{}, automation.Transient{Err: err}
	}
	var w wsdomain.Workspace
	for _, x := range all {
		if wsdomain.UnitOf(x.Path) == c.u {
			w = x
		}
	}
	s.forgetLeft(all)
	if w.ID == "" {
		return w, workspaces.PRInfo{}, fmt.Errorf("workspace %s: %w", c.u.Key, automation.ErrUnitGone)
	}
	f, err := s.facts(ctx, c.u)
	if err != nil {
		return w, workspaces.PRInfo{}, err
	}
	key := factString(f, "workspace.pr")
	if key == "" {
		return w, workspaces.PRInfo{}, nil
	}
	ref, err := pr.ParseKey(key)
	if err != nil {
		return w, workspaces.PRInfo{}, err
	}
	info := workspaces.PRInfo{Unit: ref.Unit(), Number: ref.Number, Open: strings.EqualFold(factString(f, "workspace.pr_state"), "open")}
	if s.e.Store != nil {
		row, found, err := s.e.Store.PR(ctx, ref)
		if err != nil {
			return w, info, err
		}
		if found {
			info.Head = row.HeadOid
		}
	}
	return w, info, nil
}

// removal is the workspace removal use case with the bound round state.
func (s *stepSet) removal(guard workspaces.SessionGuard, pub port.Publisher) *workspaces.Service {
	r := s.d.Removal
	return &workspaces.Service{Manager: r.Manager, Remote: r.Remote, Tree: r.Tree, Sessions: guard, Files: r.Files,
		Rounds: boundRounds{s}, Fetched: r.Fetched, Disposable: r.Disposable, Viewer: r.Viewer, Pub: pub, Clock: clockFunc(s.e.Now)}
}

func refusalText(refusal, backup string) string {
	return workspaces.Refusal{Reason: refusal, Backup: backup}.Error()
}

// removeCheck skips the fetch day in observe, which never notes and so
// never fetches. It asks the sessions every time and reuses the rest of its
// last look at the workspace while the workspace's facts, the pull
// request's head and the last fetch hold, for at most sweep.slow.
func (s *stepSet) removeCheck(ctx context.Context, c stepCall, o automation.CheckOpts) (string, error) {
	w, info, err := s.target(ctx, c)
	if err != nil {
		return "", err
	}
	if why, err := s.d.Removal.Guard.Refusal(ctx, w.ID); err != nil || why != "" {
		return why, err
	}
	key, err := s.lookKey(ctx, c, w, info, o.Observe)
	if err != nil {
		return "", err
	}
	cfg, _ := s.e.loadConfig()
	now := s.e.Now()
	s.mu.Lock()
	l, ok := s.looks[w.ID]
	s.mu.Unlock()
	if ok && l.key == key && now.Sub(l.at) < time.Duration(cfg.Sweep.Slow) {
		return l.refusal, nil
	}
	svc := s.removal(s.d.Removal.Guard, nil)
	check := svc.RemoveRefusal
	if o.Observe {
		check = svc.RemoveRefusalAsFetched
	}
	refusal, backup, err := check(ctx, w, info)
	if err != nil {
		return "", err
	}
	if refusal != "" {
		refusal = refusalText(refusal, backup)
	}
	s.mu.Lock()
	s.looks[w.ID] = removeLook{key: key, at: now, refusal: refusal}
	s.mu.Unlock()
	return refusal, nil
}

// removeLook is removeCheck's last look at a workspace.
type removeLook struct {
	key     string
	at      time.Time
	refusal string
}

// lookKey is what moves when a workspace's removal check may answer
// otherwise, its sessions aside: the facts its local reads give, the pull
// request's head, the last fetch, the day and the disposable list.
func (s *stepSet) lookKey(ctx context.Context, c stepCall, w wsdomain.Workspace, info workspaces.PRInfo, observe bool) (string, error) {
	f, err := s.facts(ctx, c.u)
	if err != nil {
		return "", err
	}
	var fetched time.Time
	if s.d.Removal.Fetched != nil {
		if fetched, err = s.d.Removal.Fetched.Last(ctx, w.ID); err != nil {
			return "", err
		}
	}
	var facts []string
	for _, name := range []string{"workspace.fingerprint", "workspace.dirty", "workspace.untracked", "workspace.unpushed", "workspace.last_activity", "workspace.path_exists"} {
		facts = append(facts, fmt.Sprint(f[name]))
	}
	return fmt.Sprint(observe, w.Path, info, fetched.UnixNano(), s.e.Now().Format(time.DateOnly), facts, s.d.Removal.Disposable()), nil
}

// forgetLeft drops the looks of the workspaces the listing no longer has.
func (s *stepSet) forgetLeft(listed []wsdomain.Workspace) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id := range s.looks {
		if !slices.ContainsFunc(listed, func(w wsdomain.Workspace) bool { return w.ID == id }) {
			delete(s.looks, id)
		}
	}
}

func (s *stepSet) forgetLook(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.looks, id)
}

func (s *stepSet) removeNote(ctx context.Context, c stepCall, refusal string) error {
	if refusal != workspaces.NotFetchedToday || s.d.Removal.Fetched == nil {
		return nil
	}
	w, info, err := s.target(ctx, c)
	if err != nil {
		return err
	}
	return s.removal(s.d.Removal.Guard, nil).Fetch(ctx, w, info)
}

func (s *stepSet) removeRun(ctx context.Context, c stepCall, _ automation.RunOpts) (string, error) {
	w, info, err := s.target(ctx, c)
	if err != nil {
		return "", err
	}
	defer s.forgetLook(w.ID)
	guard := &closingGuard{SessionGuard: s.d.Removal.Guard}
	pub := &keptEvents{next: s.d.Pub}
	err = s.removal(guard, pub).Remove(ctx, w, info, false)
	var r workspaces.Refusal
	switch {
	case errors.As(err, &r) && guard.closed:
		return "", automation.Refused{Reason: r.Error() + "; its sessions were closed before this last look"}
	case err != nil:
		return "", guardRefusal(err)
	}
	return "removed " + w.Path + ": " + pub.data(workspaces.KindRemoved), nil
}

func (s *stepSet) unregisterCheck(ctx context.Context, c stepCall, _ automation.CheckOpts) (string, error) {
	w, _, err := s.target(ctx, c)
	if err != nil {
		return "", err
	}
	switch {
	case w.IsMain:
		return "the main checkout", nil
	case !w.Local():
		return "not a local workspace", nil
	case s.d.Removal.Files.Exists(w.Path):
		return "its path still exists", nil
	}
	return "", nil
}

func (s *stepSet) unregisterRun(ctx context.Context, c stepCall, _ automation.RunOpts) (string, error) {
	w, _, err := s.target(ctx, c)
	if err != nil {
		return "", err
	}
	if err := s.removal(s.d.Removal.Guard, s.d.Pub).Unregister(ctx, w); err != nil {
		return "", guardRefusal(err)
	}
	return "unregistered " + w.ID, nil
}

func (s *stepSet) notifyCheck(_ context.Context, c stepCall, _ automation.CheckOpts) (string, error) {
	t, err := s.notifyTemplate(c)
	switch {
	case err != nil:
		return "", err
	case t == nil:
		return "no text to notify", nil
	}
	return "", nil
}

func (s *stepSet) notifyRun(ctx context.Context, c stepCall, _ automation.RunOpts) (string, error) {
	t, err := s.notifyTemplate(c)
	if err != nil || t == nil {
		return "", cmp.Or(err, errors.New("no text to notify"))
	}
	if s.d.Pub == nil {
		return "", errors.New("no notifier")
	}
	facts, err := s.facts(ctx, c.u)
	switch {
	case errors.Is(err, automation.ErrUnitGone):
		facts = workflow.Facts{}
	case err != nil:
		return "", err
	}
	var b strings.Builder
	if err := t.Execute(&b, notifyTextData{Workflow: c.why.Workflow, Unit: c.u.String(), Facts: facts}); err != nil {
		return "", fmt.Errorf("%s: rendering %s: %w", c.st.Use, t.Name(), err)
	}
	text := b.String()
	now := s.e.Now()
	s.d.Pub.Publish(port.Event{Unit: c.u, Kind: kindNotifySelf, Key: notifyKey(c.why.Workflow, now), Data: text, At: now})
	return text, nil
}

// notifyTextData is what a notify.self text renders: the workflow, the
// unit and the unit's facts, by name, none when the unit is gone.
type notifyTextData struct {
	Workflow, Unit string
	Facts          workflow.Facts
}

// notifyTemplate is c's text file parsed as a template; nil when the file
// holds no text.
func (s *stepSet) notifyTemplate(c stepCall) (*template.Template, error) {
	text, err := s.file(c, "text")
	if err != nil || strings.TrimSpace(text) == "" {
		return nil, err
	}
	t, err := template.New(c.st.Use).Option("missingkey=error").Parse(text)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", c.st.Use, err)
	}
	return t, nil
}

// boundRounds opts a pull request out of its first round in the bound round
// state.
type boundRounds struct{ s *stepSet }

func (b boundRounds) SkipFirstRound(_ context.Context, u unit.Unit) error {
	st := b.s.bound()
	if st == nil {
		return errNoRoundState
	}
	st.SkipFirstRound(u.Key)
	return nil
}

// closeCounter is a session guard that says how many sessions CloseAll
// closed.
type closeCounter interface {
	closeCount(ctx context.Context, id, reason string) (int, error)
}

// closingGuard notes whether CloseAll closed any session; a guard that does
// not count is taken to have closed none.
type closingGuard struct {
	workspaces.SessionGuard
	closed bool
}

func (g *closingGuard) CloseAll(ctx context.Context, id, reason string) error {
	c, ok := g.SessionGuard.(closeCounter)
	if !ok {
		return g.SessionGuard.CloseAll(ctx, id, reason)
	}
	n, err := c.closeCount(ctx, id, reason)
	g.closed = n > 0
	return err
}

// keptEvents keeps the events a use case published and passes them on.
type keptEvents struct {
	next port.Publisher
	evs  []port.Event
}

func (k *keptEvents) Publish(evs ...port.Event) {
	k.evs = append(k.evs, evs...)
	if k.next != nil {
		k.next.Publish(evs...)
	}
}

func (k *keptEvents) data(kind string) string {
	for _, ev := range k.evs {
		if ev.Kind == kind {
			return ev.Data
		}
	}
	return ""
}

// turnsGuard is the removal's session guard over the turn sessions: a
// session.close refusal of any live session of the workspace refuses its
// removal, and removal closes them as session.close does.
type turnsGuard struct {
	e *Env
	// snap lists the workspace's terminals; nil asks the turns' sessions.
	snap coresweep.Snapshot
}

func (g turnsGuard) live(ctx context.Context, id string) ([]string, error) {
	if g.e.Turns == nil {
		return nil, errors.New("its sessions cannot be read")
	}
	sessions, err := g.e.Turns.Registry.Sessions(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, sess := range sessions {
		if sess.WorkspaceID == id {
			out = append(out, sess.ID)
		}
	}
	return out, nil
}

// noAgent is the close refusal of a session with no agent in its terminal,
// or with no terminal.
const noAgent = "no agent"

// listed says whether the manager still lists the handle's terminal in the
// workspace: a session with no agent and no terminal holds nothing a
// removal could lose.
func (g turnsGuard) listed(ctx context.Context, id, handle string) (bool, error) {
	terms, err := g.e.Turns.Sessions.Terminals(ctx, id)
	if err != nil {
		return false, err
	}
	return slices.ContainsFunc(terms, func(t turns.Terminal) bool { return t.Session == handle }), nil
}

func (g turnsGuard) Refusal(ctx context.Context, id string) (string, error) {
	handles, err := g.live(ctx, id)
	if err != nil {
		return "", err
	}
	for _, h := range handles {
		why, err := g.e.Turns.CloseRefusal(ctx, h)
		if err != nil {
			return "", err
		}
		if why == noAgent {
			if listed, err := g.listed(ctx, id, h); err != nil || !listed {
				if err != nil {
					return "", err
				}
				continue
			}
		}
		if why != "" {
			return "session " + h + ": " + why, nil
		}
	}
	return g.unregistered(ctx, id, handles)
}

// unregistered refuses a live agent terminal of the workspace that no open
// session of the loop holds, which session.close refuses too.
func (g turnsGuard) unregistered(ctx context.Context, id string, registered []string) (string, error) {
	handles, complete, err := g.agentTerminals(ctx, id)
	if err != nil || !complete {
		return notAllListed, err
	}
	for _, h := range handles {
		if slices.Contains(registered, h) {
			continue
		}
		why, err := g.e.Turns.CloseRefusal(ctx, h)
		if err != nil || why != "" {
			return "session " + h + ": " + why, err
		}
	}
	return "", nil
}

// notAllListed refuses a removal while an agent terminal of the workspace
// may be missing from the listing.
const notAllListed = "its terminals are not all listed"

// agentTerminals are the handles of the workspace's usable terminals with an
// agent in them, read from the workspace snapshot when there is one;
// complete is false when its terminal listing is not.
func (g turnsGuard) agentTerminals(ctx context.Context, id string) (_ []string, complete bool, _ error) {
	var out []string
	if g.snap == nil {
		terms, err := g.e.Turns.Sessions.Terminals(ctx, id)
		for _, t := range terms {
			if t.Agent != "" {
				out = append(out, t.Session)
			}
		}
		return out, true, err
	}
	snap, err := g.snap.Read(ctx, wsdomain.PartWorkspaces|wsdomain.PartTerminals)
	if err != nil {
		return nil, false, err
	}
	manager := ""
	if w, ok := snap.Workspace(id); ok {
		manager = w.Manager
	}
	if !snap.TerminalsComplete(manager) {
		return nil, false, nil
	}
	handles, _ := g.e.Turns.Registry.(turns.Handles)
	for _, t := range snap.Terminals {
		if t.WorkspaceID != id || !t.Usable || t.Agent == "" {
			continue
		}
		// A process no session took stays its handle: closing it is refused,
		// so the guard never removes a workspace an agent runs in.
		session := t.Handle
		if handles != nil {
			if sid, ok, err := handles.SessionOf(ctx, t.Manager, t.Handle); err != nil {
				return nil, false, err
			} else if ok {
				session = sid
			}
		}
		out = append(out, session)
	}
	return out, true, nil
}

func (g turnsGuard) CloseAll(ctx context.Context, id, reason string) error {
	_, err := g.closeCount(ctx, id, reason)
	return err
}

func (g turnsGuard) closeCount(ctx context.Context, id, reason string) (int, error) {
	handles, err := g.live(ctx, id)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, h := range handles {
		why, err := g.e.Turns.CloseRefusal(ctx, h)
		if err != nil {
			return n, err
		}
		if why != "" {
			continue
		}
		if err := g.e.Turns.CloseSession(ctx, h, reason); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
