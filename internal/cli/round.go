package cli

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/turns"
	dchange "github.com/wloophq/wloop/internal/domain/change"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/gitx"
	"github.com/wloophq/wloop/internal/turn"
	"github.com/wloophq/wloop/internal/workspace"
)

var (
	errBusy       = errors.New("an agent is working or waiting for input there, retry next sweep")
	errUserActive = errors.New("the workspace is open in the app, retry next sweep")
	errWaiting    = errors.New("an agent is waiting for an answer there, retry next sweep")
)

type roundOptions struct {
	// allowUserActive lets a round start while the user has the workspace
	// open: true for `review`, which the user starts by hand.
	allowUserActive bool
	// confirmed is an automated round the user sent from the plan. Unlike
	// allowUserActive it lifts only the open-workspace check: the session
	// choice and a blocked start stay those of an automated round.
	confirmed bool
	// keepLive sends to the live session however long it has been idle.
	keepLive bool
	// resumeOf is the turn the round's turn resumes, 0 for none.
	resumeOf int64
	// trusted is the pull request's verdict from trusts.
	trusted bool
	// busy refuses the round, rather than start a session beside the live
	// one, when the live session cannot take a turn.
	busy bool
	// title is the session title of the round's intent; newSession never
	// types into the live session.
	title      string
	newSession bool
	// policy and intent are the round's manifest's policy and name; ""
	// is the round policy.
	policy string
	intent dintent.Name
	// head is the pull request's head branch the round's fetch also brings,
	// for realign after it; "" for none.
	head string
	// unit is the round's unit, whose dossier the session is granted; zero for none.
	unit unit.Unit
	// pr is the round's pull request as read for it, nil for none; the
	// configuration's project_settings reads its facts.
	pr *github.PullRequest
}

// trusts says whether pr passes the trust check of automated rounds under
// its project's configuration: only then may a session the loop starts run
// unattended, and its preflight fails on a checkout the agent home does not
// trust.
func trusts(cfg config.Config, pr *github.PullRequest) bool {
	return pr != nil && dchange.Untrusted(pr.Facts, []string{pr.Owner}, cfg.For(pr.Owner, pr.Repo).GitHub.TrustedAuthors) == ""
}

// keepBlocked says whether a session blocked on the question reason stays
// open for the user: only in a round the user started by hand, and on the
// trust question only for an untrusted pull request, since a trusted one's
// answer is the trust command.
func (o roundOptions) keepBlocked(reason string) bool {
	return o.allowUserActive && (!o.trusted || reason != workspace.ReasonTrust)
}

// startRound moves an existing review workspace to the PR head and hands the
// prompt to its agent, starting a session when none is alive. The activity
// check comes first: resetting the tree under a working agent changes the
// files it is reading, and typing into a waiting one answers its question.
func (e *Env) startRound(ctx context.Context, cfg config.Config, ws wsdomain.Workspace, ref github.Ref, sent *sentTurn, opts roundOptions) (submitted bool, verb string, err error) {
	activity, err := e.freshActivity(ctx)
	if err != nil {
		return false, "", err
	}
	a := activity[ws.ID]
	switch {
	case a.Busy:
		return false, "", errBusy
	case a.UserActive && !opts.allowUserActive && !opts.confirmed:
		return false, "", errUserActive
	}

	if err := e.Git.FetchPR(ctx, ws.Path, ref.Number, heads(opts.head)...); err != nil {
		return false, "", err
	}
	dirty, err := e.Git.DirtyTracked(ctx, ws.Path)
	if err != nil {
		return false, "", err
	}
	if dirty {
		return false, "", fmt.Errorf("%s has uncommitted changes to tracked files; clean it before a new round", ws.Path)
	}
	if err := e.Git.ResetHard(ctx, ws.Path, gitx.PRRef(ref.Number)); err != nil {
		return false, "", err
	}

	p, verb, err := e.session(ctx, cfg, ws, ref.Unit(), opts)
	if err != nil {
		return false, "", err
	}
	submitted, err = e.deliver(ctx, ref.Unit(), sent, p, false)
	return submitted, verb, err
}

// startAddress hands a triage prompt to the agent of an author workspace,
// leaving the checkout as is. The agent takes it whatever it is doing, unless
// it stopped on a question that typed text would answer. Local work not on
// the PR goes in the prompt and keeps the live session, which knows it.
func (e *Env) startAddress(ctx context.Context, cfg config.Config, ws wsdomain.Workspace, r turn.Request, opts roundOptions) (submitted bool, verb, turnID string, err error) {
	ref := prOf(r.Unit)
	if err := e.roundReady(); err != nil {
		return false, "", "", err
	}
	activity, err := e.freshActivity(ctx)
	if err != nil {
		return false, "", "", err
	}
	a := activity[ws.ID]
	if a.Waiting {
		return false, "", "", errWaiting
	}
	if err := e.Git.FetchPR(ctx, ws.Path, ref.Number); err != nil {
		return false, "", "", err
	}
	local, err := e.localWork(ctx, ws.Path, ref.Number)
	if err != nil {
		return false, "", "", err
	}
	if local != "" || a.Busy {
		opts.keepLive = true
	}
	opts.busy = a.Busy
	p, verb, err := e.session(ctx, cfg, ws, r.Unit, opts)
	if err != nil {
		return false, "", "", err
	}
	queued := a.Busy && verb == verbExisting
	if queued {
		verb = "prompt queued in the busy agent session"
	}
	r.Owned, r.Local, r.Queued = true, local, queued
	sent, err := e.sendTurn(ctx, cfg, r)
	if err != nil {
		return false, "", "", err
	}
	sent.resume(opts.resumeOf)
	submitted, err = e.deliver(ctx, r.Unit, &sent, p, queued)
	e.recordGuidance(ctx, ref, r.Guidance)
	if err != nil {
		return false, "", "", err
	}
	if err := e.roundStarted(ctx, r.Unit, func() error { return e.setColumn(ctx, cfg, ws, config.ColumnInProgress) }); err != nil {
		fmt.Fprintf(e.Err, "warning: %s: setting the board status: %v\n", ws.DisplayName, err)
	}
	return submitted, verb, sent.ID, nil
}

// localWork describes what the checkout holds that is not on the PR, or "".
func (e *Env) localWork(ctx context.Context, path string, number int) (string, error) {
	var parts []string
	dirty, err := e.Git.DirtyTracked(ctx, path)
	if err != nil {
		return "", err
	}
	if dirty {
		parts = append(parts, "uncommitted changes")
	}
	head, err := e.Git.Head(ctx, path)
	if err != nil {
		return "", err
	}
	onPR, err := e.Git.IsAncestor(ctx, path, head, gitx.PRRef(number))
	if err != nil {
		return "", err
	}
	if !onPR {
		parts = append(parts, "commits not on the PR")
	}
	return strings.Join(parts, " and "), nil
}

var (
	verbExisting = "prompt sent to the existing agent session"
	verbNew      = "prompt sent to a new agent session"
	verbWoken    = "prompt sent to the agent session, woken"
	verbNoMCP    = "prompt sent to a new agent session: the live one has no " + brand.Name + " MCP tool"
)

// warmSession is how recently a session must have printed for an automated
// round to keep it: past the prompt cache lifetime, rereading its whole
// context costs more than a fresh session that reads the round notes.
const warmSession = time.Hour

// proc is the live process of a round's session: the loop's session id ("" in
// a process that keeps no turns), the manager that runs it and its handle there.
type proc struct {
	id     string
	m      workspace.Manager
	handle string
}

// session picks the agent session of u for a round. A round started by hand
// keeps the live session; an automated one keeps it only while warm, and
// otherwise starts a fresh one beside it. A dormant session the manager can
// wake is woken under the same rule. A live session without the loop's MCP
// tool is never typed into. In a workspace u's placement shares with other
// units, the live session is only one of u's own. Without a live session in
// ws's owner, the one in hostOf's manager is kept under the same rule; a
// session the round starts or wakes runs in hostOf's manager.
func (e *Env) session(ctx context.Context, cfg config.Config, ws wsdomain.Workspace, u unit.Unit, opts roundOptions) (proc, string, error) {
	m, err := e.WS.For(ctx, ws)
	if err != nil {
		return proc{}, "", err
	}
	keep := func(last time.Time) bool {
		return !opts.newSession && (opts.allowUserActive || opts.keepLive || e.Now().Sub(last) < warmSession)
	}
	live, ok, err := e.liveSession(ctx, cfg, m, ws, u)
	if err != nil {
		return proc{}, "", err
	}
	var host *wsdomain.Workspace
	if !ok {
		h, err := e.hostOf(ctx, cfg, ws, opts)
		if err != nil {
			return proc{}, "", err
		}
		host = &h
		if h.Manager != ws.Manager {
			hm, err := e.WS.For(ctx, h)
			if err != nil {
				return proc{}, "", err
			}
			if live, ok, err = e.liveSession(ctx, cfg, hm, h, u); err != nil {
				return proc{}, "", err
			}
			if ok {
				ws, m = h, hm
			}
		}
	}
	verb := verbNew
	if ok && keep(live.LastOutput) {
		id, err := e.sessionOf(ctx, m, live.Handle, ws, u)
		if err != nil {
			return proc{}, "", err
		}
		capable, err := e.capable(ctx, id)
		if err != nil {
			return proc{}, "", err
		}
		capable = capable || lazyMCP(m)
		if capable {
			return proc{id, m, live.Handle}, verbExisting, nil
		}
		if opts.busy {
			return proc{}, "", errBusy
		}
		verb = verbNoMCP
	}
	if host == nil {
		h, err := e.hostOf(ctx, cfg, ws, opts)
		if err != nil {
			return proc{}, "", err
		}
		host = &h
	}
	ws = *host
	if m, err = e.WS.For(ctx, ws); err != nil {
		return proc{}, "", err
	}
	if !ok {
		p, woke, err := e.wake(ctx, cfg, m, ws, u, opts, keep)
		if err != nil || woke {
			return p, verbWoken, err
		}
	}
	opts.unit = u
	s, err := e.agentSession(ctx, cfg, ws, opts)
	if err != nil {
		return proc{}, "", err
	}
	p, err := e.startSession(ctx, cfg, ws, u, s, opts.title+" "+e.Now().Format(sessionTime), opts.keepBlocked, opts.trusted)
	if err != nil {
		return proc{}, "", err
	}
	return p, verb, nil
}

// sessionOf is the loop's session of the manager's handle, taking a process no
// session took as a session of u in ws; "" in a process that keeps no turns.
func (e *Env) sessionOf(ctx context.Context, m workspace.Manager, handle string, ws wsdomain.Workspace, u unit.Unit) (string, error) {
	if e.Turns == nil {
		return "", nil
	}
	id, err := e.Turns.Resolve(ctx, m.Name(), handle)
	if err != nil {
		return "", err
	}
	e.Turns.Placed(ctx, id, ws.ID, ws.Path, u)
	return id, nil
}

// wake gives u's newest dormant session in ws a live process again, when its
// manager can and keep holds for its last activity; woke is false when there
// is none to wake.
func (e *Env) wake(ctx context.Context, cfg config.Config, m workspace.Manager, ws wsdomain.Workspace, u unit.Unit, opts roundOptions, keep func(time.Time) bool) (proc, bool, error) {
	w, ok := workspace.Capability[workspace.Waker](m)
	if !ok || e.Turns == nil {
		return proc{}, false, nil
	}
	sess, last, found, err := e.Turns.DormantFor(ctx, u, m.Name(), ws.ID, true)
	if err != nil || !found || !keep(last) {
		return proc{}, false, err
	}
	opts.unit = u
	s, err := e.agentSession(ctx, cfg, ws, opts)
	if err != nil {
		return proc{}, false, err
	}
	s.Resume = sess.AgentSession
	a, err := e.agent(cfg.Agent)
	if err != nil {
		return proc{}, false, err
	}
	cmd, err := a.Command(s)
	if err != nil {
		return proc{}, false, err
	}
	req := workspace.WakeRequest{WorkspaceID: ws.ID, Session: sess.ID, Handle: sess.LastHandle, Agent: cfg.Agent, AgentSession: sess.AgentSession,
		Command: sessionEnv(e.Getenv, cmd), Title: opts.title + " " + e.Now().Format(sessionTime), Model: s.Model}
	if r, ok := a.(turns.SettingsRenderer); ok {
		req.Settings = r.Settings(s.Policy)
	}
	handle, err := w.Wake(ctx, req)
	if handle != "" {
		if werr := e.Turns.Woken(ctx, sess.ID, m.Name(), handle); werr != nil {
			return proc{}, false, werr
		}
	}
	if blocked := (*workspace.BlockedError)(nil); errors.As(err, &blocked) {
		return proc{}, false, fmt.Errorf("waking the session %s: %w", sess.ID, e.explainBlocked(ctx, cfg, ws, blocked, opts.trusted))
	}
	if err != nil {
		fmt.Fprintf(e.Err, "%s waking the session %s failed, starting a new one: %v\n", e.stamp(), sess.ID, err)
		return proc{}, false, nil
	}
	if !lazyMCP(m) {
		if err := e.awaitCapable(ctx, sess.ID, e.agentHome(cfg)); err != nil {
			return proc{}, false, err
		}
	}
	return proc{sess.ID, m, handle}, true, nil
}

// liveSession is the live agent session of ws a round of u may keep: the
// one that printed last, or in a shared workspace u's newest.
func (e *Env) liveSession(ctx context.Context, cfg config.Config, m workspace.Manager, ws wsdomain.Workspace, u unit.Unit) (workspace.Session, bool, error) {
	shared, err := e.shared(ctx, cfg, u)
	if err != nil {
		return workspace.Session{}, false, err
	}
	if !shared {
		return workspace.Direct(m).LiveAgentSession(ctx, ws.ID, cfg.Agent)
	}
	if e.Turns == nil {
		return workspace.Session{}, false, nil
	}
	live, ok, err := e.Turns.SessionFor(ctx, u, ws.ID)
	if err != nil || !ok || live.Session.Manager != m.Name() {
		return workspace.Session{}, false, err
	}
	return workspace.Session{Handle: live.Session.Handle, LastOutput: live.LastOutput}, true, nil
}

// startSession mints a session id and starts an agent session of s under it
// in ws, which the loop will type a turn into, and waits for its MCP tool. A
// session that cannot take the turn is abandoned, but for one blocked on a
// prompt with keepBlocked, which stays for the user to answer. trusted is the
// pull request's verdict from trusts.
func (e *Env) startSession(ctx context.Context, cfg config.Config, ws wsdomain.Workspace, u unit.Unit, s turns.AgentSession, title string, keepBlocked func(reason string) bool, trusted bool) (proc, error) {
	m, err := e.WS.For(ctx, ws)
	if err != nil {
		return proc{}, err
	}
	req, err := e.startRequest(cfg, ws, s, title)
	if err != nil {
		return proc{}, err
	}
	p := proc{id: req.Session, m: m}
	p.handle, err = m.StartAgentSession(ctx, req)
	var blocked *workspace.BlockedError
	switch {
	case errors.As(err, &blocked):
		p.handle = blocked.Handle
		if lerr := e.launched(ctx, cfg, ws, u, p); lerr != nil {
			return proc{}, lerr
		}
		return proc{}, e.blockedStart(ctx, cfg, ws, p, blocked, keepBlocked, trusted)
	case err != nil:
		return proc{}, err
	}
	if e.Turns == nil {
		return p, nil
	}
	if err := e.launched(ctx, cfg, ws, u, p); err != nil {
		return proc{}, err
	}
	if lazyMCP(m) {
		return p, nil
	}
	err = e.awaitCapable(ctx, p.id, e.agentHome(cfg))
	if err == nil || errors.As(err, new(errNoMCP)) {
		if blocked := e.screenBlock(ctx, cfg, p); blocked != nil {
			return proc{}, e.blockedStart(ctx, cfg, ws, p, blocked, keepBlocked, trusted)
		}
	}
	if err != nil {
		return proc{}, e.abandon(ctx, p, err)
	}
	return p, nil
}

// startRequest is the start of s in ws under a minted session id: the command
// a terminal manager runs and what a manager that takes no command needs.
func (e *Env) startRequest(cfg config.Config, ws wsdomain.Workspace, s turns.AgentSession, title string) (workspace.StartRequest, error) {
	a, err := e.agent(cfg.Agent)
	if err != nil {
		return workspace.StartRequest{}, err
	}
	s.SessionID = turns.NewSessionID()
	if e.Turns != nil {
		s.SessionID = e.Turns.MintID()
	}
	cmd, err := a.Command(s)
	if err != nil {
		return workspace.StartRequest{}, err
	}
	req := workspace.StartRequest{WorkspaceID: ws.ID, Session: s.SessionID, Agent: a.Name(), Command: sessionEnv(e.Getenv, cmd), Title: title, Model: s.Model}
	if r, ok := a.(turns.SettingsRenderer); ok {
		req.Settings = r.Settings(s.Policy)
	}
	return req, nil
}

// blockedStart explains a session that waits on a prompt and abandons it,
// unless keepBlocked leaves it open for the user to answer.
func (e *Env) blockedStart(ctx context.Context, cfg config.Config, ws wsdomain.Workspace, p proc, blocked *workspace.BlockedError, keepBlocked func(reason string) bool, trusted bool) error {
	err := e.explainBlocked(ctx, cfg, ws, blocked, trusted)
	if keepBlocked(blocked.Reason) {
		return fmt.Errorf("%w; it stays open for you to decide, then run the round again", err)
	}
	return e.abandon(ctx, p, err)
}

// explainBlocked has the agent say what the user does about the question
// a session waits on. Only a trusted pull request's session suggests the
// command that trusts its repository: for any other, the dialog is the
// user's check on that checkout.
func (e *Env) explainBlocked(ctx context.Context, cfg config.Config, ws wsdomain.Workspace, blocked *workspace.BlockedError, trusted bool) error {
	a, err := e.agent(cfg.Agent)
	p, ok := a.(turns.Questions)
	if err != nil || !ok {
		return blocked
	}
	question := blocked.Reason
	if question == workspace.ReasonTrust {
		question = turns.QuestionTrust
	}
	explained := p.Explain(ctx, turns.AgentSession{Worktree: ws.Path, Home: cfg.AgentHome, Trusted: trusted}, question)
	var untrusted *turns.UntrustedError
	var asked *turns.QuestionError
	switch {
	case errors.As(explained, &untrusted):
		untrusted.Terminal, untrusted.Cause = blocked.Handle, blocked
		if trusted && untrusted.Home != "" {
			untrusted.Fix = e.trustCommand(untrusted.Home, cmp.Or(untrusted.Root, untrusted.Folder))
		}
	case errors.As(explained, &asked):
		asked.Cause = blocked
	default:
		return blocked
	}
	return explained
}

// explainStart is err, explained when the start is blocked on a question.
func (e *Env) explainStart(ctx context.Context, cfg config.Config, ws wsdomain.Workspace, err error, trusted bool) error {
	if blocked := (*workspace.BlockedError)(nil); errors.As(err, &blocked) {
		return e.explainBlocked(ctx, cfg, ws, blocked, trusted)
	}
	return err
}

// screenBlock reads a new session's screen once and returns the question it
// waits on that the workspace manager does not report, or nil, so no prompt
// is typed into the agent's question. A manager that cannot read a screen
// reports every question it knows of.
func (e *Env) screenBlock(ctx context.Context, cfg config.Config, p proc) *workspace.BlockedError {
	a, aerr := e.agent(cfg.Agent)
	q, ok := a.(turns.Questions)
	r, readable := workspace.Capability[workspace.ScreenReader](p.m)
	if aerr != nil || !ok || !readable {
		return nil
	}
	screen, err := r.ReadScreen(ctx, p.handle)
	if err != nil {
		return nil
	}
	if reason := q.BlockedOn(screen); reason != "" {
		return &workspace.BlockedError{Handle: p.handle, Reason: reason}
	}
	return nil
}

// abandon ends the process of a session the loop started for a round that
// cannot go on, and closes the session so no later round types into it.
func (e *Env) abandon(ctx context.Context, p proc, cause error) error {
	var errs []error
	if c, ok := workspace.Capability[workspace.TerminalCloser](p.m); ok {
		if err := c.CloseTerminal(ctx, p.handle); err != nil {
			errs = append(errs, fmt.Errorf("closing its terminal %s: %w", p.handle, err))
		}
	}
	if e.Turns != nil && p.id != "" {
		if err := e.Turns.Abandoned(ctx, p.id); err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return fmt.Errorf("%w; %w", cause, err)
	}
	return fmt.Errorf("%w; closed its session %s", cause, cmp.Or(p.id, p.handle))
}

// launched registers a session the loop started, so cleanup knows it is
// the loop's. The agent took the session's id as its own conversation's, so
// the session can be resumed before any hook reports it.
func (e *Env) launched(ctx context.Context, cfg config.Config, ws wsdomain.Workspace, u unit.Unit, p proc) error {
	if e.Turns == nil {
		return nil
	}
	if err := e.Turns.Launched(ctx, dturn.Session{ID: p.id, Manager: p.m.Name(), Handle: p.handle, Agent: cfg.Agent, AgentSession: p.id,
		WorkspaceID: ws.ID, Worktree: ws.Path, Home: cfg.AgentHome, Unit: u}); err != nil {
		return err
	}
	e.grantLaunched(ctx, u, p.id)
	return nil
}

// lazyMCP says whether m's sessions show the loop's MCP tool only after their
// first prompt: the loop then types without waiting for it, and a turn falls
// back to `turn end` when the tool never comes.
func lazyMCP(m workspace.Manager) bool {
	l, ok := workspace.Capability[workspace.LazyMCP](m)
	return ok && l.ConnectsMCPOnFirstPrompt()
}

func (e *Env) capable(ctx context.Context, id string) (bool, error) {
	if e.Turns == nil {
		return true, nil
	}
	return e.Turns.Capable(ctx, id)
}

// mcpWait is how long a new session has to initialize the loop's MCP server.
var mcpWait = 10 * time.Second

const mcpPoll = 250 * time.Millisecond

type errNoMCP struct{ home string }

func (e errNoMCP) Error() string {
	return "the new agent session has no " + brand.Name + " MCP tool; check `" + brand.Command + " doctor` for " + e.home
}

// awaitCapable waits until the session initialized the loop's MCP server, for
// up to mcpWait.
func (e *Env) awaitCapable(ctx context.Context, id, home string) error {
	deadline := time.Now().Add(mcpWait)
	for {
		ok, err := e.capable(ctx, id)
		if err != nil || ok {
			return err
		}
		left := time.Until(deadline)
		if left <= 0 {
			return errNoMCP{home}
		}
		t := time.NewTimer(min(mcpPoll, left))
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}

// deliver stores the round's turn, types its prompt into the process and
// names the session it went to. A turn the agent started before SentTo ran
// stays started; one whose prompt could not be typed is withdrawn.
func (e *Env) deliver(ctx context.Context, u unit.Unit, sent *sentTurn, p proc, queued bool) (bool, error) {
	if sent.send != nil {
		t, err := e.Turns.Send(ctx, *sent.send)
		if err != nil {
			return false, err
		}
		sent.Seq = t.Seq
	}
	typed := func() {}
	if e.Turns != nil {
		typed = e.Turns.Typing(p.id)
	}
	submitted, line, err := e.sendPrompt(ctx, p, sent.Prompt, queued)
	if err == nil && e.Turns != nil {
		if terr := e.Turns.Typed(ctx, p.id, line); terr != nil {
			fmt.Fprintf(e.Err, "warning: %s: noting the line typed into %s: %v\n", u.Label(), p.id, terr)
		}
	}
	typed()
	if sent.Seq == 0 {
		return submitted, err
	}
	if err != nil {
		if werr := e.Turns.Withdraw(context.WithoutCancel(ctx), u, sent.ID, "typing the prompt: "+err.Error()); werr != nil {
			fmt.Fprintf(e.Err, "warning: %s: withdrawing the turn of round %s: %v\n", u.Label(), sent.ID, werr)
		}
		return submitted, err
	}
	var refused turns.Refusal
	if err := e.Turns.SentTo(ctx, u, sent.ID, p.id); err != nil && !errors.As(err, &refused) {
		fmt.Fprintf(e.Err, "warning: %s: naming the session of round %s: %v\n", u.Label(), sent.ID, err)
	}
	return submitted, nil
}

// sendPrompt types prompt into the process and returns the line the agent
// received: a manager that delivers a prompt as a message rewrites it.
func (e *Env) sendPrompt(ctx context.Context, p proc, prompt string, queued bool) (bool, string, error) {
	if r, ok := workspace.Capability[workspace.PromptRewriter](p.m); ok {
		prompt = r.RewritePrompt(prompt)
	}
	submitted, err := p.m.SendPrompt(ctx, p.handle, prompt, queued)
	return submitted, prompt, err
}

// alignWithPR lets the workspace manager show the pull request on a review
// workspace even after it drops the link, by the local branch name. A main
// checkout is left alone: it is the PR's workspace only while on its branch.
func (e *Env) alignWithPR(ctx context.Context, ws wsdomain.Workspace, pr *github.PullRequest) {
	e.align(ctx, ws, pr, false)
}

// realign is alignWithPR after a round whose fetch brought trackedHead(ws,
// pr); it writes the link only when ws, as last listed, lost it.
func (e *Env) realign(ctx context.Context, ws wsdomain.Workspace, pr *github.PullRequest) {
	e.align(ctx, ws, pr, true)
}

// trackedHead is the head branch align tracks in ws, "" for none.
func trackedHead(ws wsdomain.Workspace, pr *github.PullRequest) string {
	if pr == nil || ws.IsMain || pr.IsCrossRepository {
		return ""
	}
	return pr.HeadRefName
}

func heads(branch string) []string {
	if branch == "" {
		return nil
	}
	return []string{branch}
}

func (e *Env) align(ctx context.Context, ws wsdomain.Workspace, pr *github.PullRequest, afterRound bool) {
	if ws.IsMain {
		return
	}
	if !pr.IsCrossRepository {
		track := e.Git.TrackBranch
		if afterRound {
			track = e.Git.TrackFetchedBranch
		}
		if _, err := track(ctx, ws.Path, pr.HeadRefName); err != nil {
			fmt.Fprintf(e.Err, "warning: %s: tracking %s: %v\n", ws.DisplayName, pr.HeadRefName, err)
		}
	}
	if afterRound && ws.LinkedPR == pr.Number {
		return
	}
	if err := e.WS.LinkUnit(ctx, ws, pr.Unit()); err != nil {
		fmt.Fprintf(e.Err, "warning: %s: linking PR #%d: %v\n", ws.DisplayName, pr.Number, err)
	}
}

// setReviewStatus writes the review card through the views; without them it
// falls back when the board has no `reviewing` column. unread also marks the
// workspace unread, in the same write when the manager can.
func (e *Env) setReviewStatus(ctx context.Context, cfg config.Config, ref github.Ref, ws wsdomain.Workspace, unread bool) error {
	if e.Views != nil {
		e.Views.RoundStarted(ctx, pr.Ref(ref).Unit())
		return e.setCard(ctx, ws, "", unread)
	}
	working, fallback := e.column(cfg, config.ColumnWorking), e.column(cfg, config.ColumnWorkingFallback)
	err := e.setCard(ctx, ws, working, unread)
	if working == "" || !errors.Is(err, workspace.ErrUnknownStatus) || fallback == "" {
		return err
	}
	fmt.Fprintf(e.Out, "board column '%s' not found, using '%s'\n", working, fallback)
	return e.setCard(ctx, ws, fallback, unread)
}

// setCard moves a workspace's card to status and marks it unread, in one
// write; an empty status or a false unread leaves that part as it is. A
// manager with no board has no card to write.
func (e *Env) setCard(ctx context.Context, ws wsdomain.Workspace, status string, unread bool) error {
	m, err := e.WS.For(ctx, ws)
	if err != nil {
		return err
	}
	b, ok := workspace.Capability[workspace.Board](m)
	if !ok || (status == "" && !unread) {
		return nil
	}
	return b.SetCard(ctx, ws.ID, status, "", unread)
}

// freshActivity rereads the agents' activity past the snapshot: a round decides on it
// right before it resets the tree or types into a session.
func (e *Env) freshActivity(ctx context.Context) (map[string]wsdomain.Activity, error) {
	e.WS.Invalidate(wsdomain.PartPanes)
	return e.WS.AgentActivity(ctx)
}

// roundStarted moves the unit's cards for a round that went out: through
// the views, whatever the user placed them in, or with noViews in a process
// that has none.
func (e *Env) roundStarted(ctx context.Context, u unit.Unit, noViews func() error) error {
	if e.Views == nil {
		return noViews()
	}
	e.Views.RoundStarted(ctx, u)
	return nil
}

func (e *Env) stamp() string { return e.Now().Format(time.DateTime) }

// handGuidance is the guidance of a round asked for by hand: the user's, else
// the guidance kept from a round that did not go out. Either way the kept one
// is used up.
func (e *Env) handGuidance(ctx context.Context, ref github.Ref, guidance string) string {
	if e.Rec == nil {
		return guidance
	}
	kept, err := e.Rec.TakeNextGuidance(ctx, ref)
	if err != nil {
		fmt.Fprintf(e.Err, "warning: %s: reading the guidance kept for this round: %v\n", ref, err)
	}
	return cmp.Or(guidance, kept)
}

// recordGuidance records the guidance a round's prompt carried, so the
// text survives a prompt that never reached the agent.
func (e *Env) recordGuidance(ctx context.Context, ref github.Ref, guidance string) {
	if e.Rec == nil || guidance == "" {
		return
	}
	now := e.Now()
	ev := dinv.Event{PR: ref, Kind: dinv.KindGuidance, Key: now.UTC().Format(time.RFC3339Nano), At: now, Data: guidance}
	if err := e.Rec.Record(context.WithoutCancel(ctx), ev); err != nil {
		fmt.Fprintf(e.Err, "warning: %s: recording the guidance: %v\n", ref, err)
	}
}

// keepGuidance keeps the guidance of a round that did not go out, or whose
// prompt was not seen landing, for the next round of the pull request.
func (e *Env) keepGuidance(ctx context.Context, ref github.Ref, guidance string) {
	if e.Rec == nil || guidance == "" {
		return
	}
	if err := e.Rec.KeepNextGuidance(context.WithoutCancel(ctx), ref, guidance); err != nil {
		fmt.Fprintf(e.Err, "warning: %s: keeping your guidance for the next round: %v\n", ref, err)
		return
	}
	fmt.Fprintf(e.Out, "%s: your guidance is kept for the next round\n", ref)
}
