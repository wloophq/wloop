package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/daemon"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/round"
	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/turn"
)

type handTurn struct {
	def                  dintent.Def
	url, agent, guidance string
	inputs               map[string]any
	// cwd is the caller's directory; handle its terminal, "" outside a
	// workspace manager's terminal.
	cwd, handle string
}

const handInSession = "asked by you in a session"

// byHand runs, in the daemon, a turn the user asked for from an agent
// session, with the inputs the daemon checked and prepared; it waits on the
// loop.
func (e *Env) byHand(loopMu *sync.Mutex) func(ctx context.Context, r api.ByHandRequest, handle string) (api.ByHandResult, error) {
	return func(ctx context.Context, r api.ByHandRequest, handle string) (api.ByHandResult, error) {
		d, err := e.intentDef(r.Intent)
		if err != nil {
			return api.ByHandResult{}, daemon.BadRequest(err.Error())
		}
		if !daemon.LoopHeld(ctx) {
			u, err := e.byHandRef(ctx, r, handle)
			if err != nil {
				return api.ByHandResult{}, err
			}
			r.URL = u.String()
			if isPR(u) {
				ref := prOf(u)
				r.URL = ref.URL()
				if aheadOf(ctx, ref) == nil {
					ctx = e.ReadAhead(ctx, ref, false)
				}
			}
			loopMu.Lock()
			defer loopMu.Unlock()
		}
		var text, notes bytes.Buffer
		q := *e
		home := r.Home
		q.Out, q.Err, q.HomeOverride = &notes, &notes, &home
		err = q.turnByHand(ctx, handTurn{def: d, url: r.URL, agent: r.Agent, guidance: r.Guidance, inputs: r.Inputs, cwd: r.Cwd, handle: handle}, &text)
		res := api.ByHandResult{Text: text.String()}
		if n := strings.TrimRight(notes.String(), "\n"); n != "" {
			res.Notes = strings.Split(n, "\n")
		}
		return res, err
	}
}

// byHandRef is the unit a turn by hand is about, found as the turn finds
// it.
func (e *Env) byHandRef(ctx context.Context, r api.ByHandRequest, _ string) (unit.Unit, error) {
	repos, all := e.workspaceState(ctx)
	ws, repo, inWS := workspaceAt(r.Cwd, repos, all)
	return e.handUnit(ctx, r.URL, r.Cwd, ws, repo, inWS)
}

// turnByHand runs a round from the agent session the user is in: it writes
// the turn's instructions to text, starts the turn on the caller's session
// and reports everything else on e.Out.
func (e *Env) turnByHand(ctx context.Context, h handTurn, text io.Writer) error {
	d, cwd := h.def, h.cwd
	repos, all := e.workspaceState(ctx)
	ws, repo, inWS := workspaceAt(cwd, repos, all)
	u, err := e.handUnit(ctx, h.url, cwd, ws, repo, inWS)
	if err != nil {
		return err
	}
	if !isPR(u) {
		return e.unitTurnByHand(ctx, h, u, text)
	}
	ref := prOf(u)
	cfg, err := e.loadConfig()
	if err != nil {
		return err
	}
	pr, err := e.openPR(ctx, ref)
	if err != nil {
		return err
	}
	takeOver := beforeSet(d, h.inputs, beforeTakeOver)
	r := turn.Request{Intent: turn.Intent(d.Name), Trigger: turn.ByHand, Unit: ref.Unit(), Payload: turnPR(pr), Agent: h.agent, Inputs: h.inputs}
	e.withAddressReason(ctx, cfg, d, &r)
	if d.OwnerOnly {
		owned, err := e.ownedByHand(ctx, cfg.For(ref.Owner, ref.Repo), repos, all, ref, pr, cwd)
		if err != nil {
			return err
		}
		if !owned && !takeOver {
			_, out, err := e.renderTurn(ctx, cfg, r)
			if err != nil {
				return err
			}
			_, err = fmt.Fprint(text, out.Text)
			return err
		}
		r.Owned = true
	}
	if err := e.roundReady(); err != nil {
		return err
	}
	if inWS && (d.Workspace == workspaceReview || d.Workspace == workspaceAuthor) {
		if err := e.prepareWorktree(ctx, cfg.For(ref.Owner, ref.Repo), ws, repo, ref, d.Workspace); err != nil {
			return err
		}
	}
	if takeOver {
		if err := e.takeOverHere(ctx, cfg, ref, cwd); err != nil {
			return err
		}
	}
	return e.handRound(ctx, routeSession, ref, h.guidance, d, false, func(_ *round.Store, sr *sentRound) (bool, error) {
		r.Guidance = sr.guidance
		sent, err := e.sendTurn(ctx, cfg, r)
		if err != nil {
			return false, err
		}
		if sent.send != nil {
			if _, err := e.Turns.StartByHand(ctx, *sent.send, h.handle); err != nil {
				return false, err
			}
		}
		if _, err := fmt.Fprint(text, sent.Text); err != nil {
			return false, err
		}
		sr.pr, sr.cfg, sr.inputs, sr.id = pr, cfg, h.inputs, sent.ID
		runPhase(routeSession, d, sr, sentPhase)
		e.recordGuidance(ctx, ref, sr.guidance)
		return true, nil
	})
}

// inputList is a list input as JSON or Go left it.
func inputList(v any) []string {
	switch l := v.(type) {
	case []string:
		return l
	case []any:
		out := make([]string, 0, len(l))
		for _, x := range l {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// workspaceState lists the workspace manager's repositories and workspaces;
// a turn by hand goes on without them when the manager does not answer.
func (e *Env) workspaceState(ctx context.Context) ([]wsdomain.Repo, []wsdomain.Workspace) {
	repos, err := e.WS.Repos(ctx)
	if err == nil {
		var all []wsdomain.Workspace
		if all, err = e.WS.List(ctx); err == nil {
			return repos, all
		}
	}
	fmt.Fprintf(e.Err, "warning: listing the %s workspaces: %v; going on without one\n", e.managerDisplay(), err)
	return nil, nil
}

// handUnit is the unit a turn by hand is about: the target given, as any
// <intent> <target> command resolves it; else the pull request the
// workspace holding the caller's directory cwd is linked to; else the
// unit a provider says cwd holds.
func (e *Env) handUnit(ctx context.Context, target, cwd string, ws wsdomain.Workspace, repo wsdomain.Repo, inWS bool) (unit.Unit, error) {
	if target != "" {
		return e.unitTarget(ctx, target)
	}
	if inWS && ws.LinkedPR != 0 {
		if owner, name, ok := repo.OwnerName(); ok {
			return github.Ref{Owner: owner, Repo: name, Number: ws.LinkedPR}.Unit(), nil
		}
	}
	for p, l := range e.locators() {
		key, ok, err := l.UnitAt(ctx, cwd)
		if err != nil {
			return unit.Unit{}, fmt.Errorf("no unit here; pass its target: %w", err)
		}
		if ok {
			return unit.Unit{Provider: p, Key: key}, nil
		}
	}
	return unit.Unit{}, errors.New("no unit here; pass its target")
}

// unitTurnByHand runs a turn by hand on a unit that is no pull request,
// through the path a turn.start on it takes: the provider hands its
// payload, and the turn starts on the caller's session.
func (e *Env) unitTurnByHand(ctx context.Context, h handTurn, u unit.Unit, text io.Writer) error {
	cfg, err := e.loadConfig()
	if err != nil {
		return err
	}
	if !h.def.Serves(u.Provider) {
		return fmt.Errorf("the intent %s serves %s units, not %s", h.def.Name, h.def.ProviderOrDefault(), u.Label())
	}
	r := turn.Request{Intent: turn.Intent(h.def.Name), Trigger: turn.ByHand, Unit: u, Agent: h.agent, Inputs: h.inputs, Guidance: h.guidance}
	sent, err := e.sendTurn(ctx, cfg, r)
	if err != nil {
		return err
	}
	if sent.send != nil && e.Turns != nil {
		if _, err := e.Turns.StartByHand(ctx, *sent.send, h.handle); err != nil {
			return err
		}
	}
	_, err = fmt.Fprint(text, sent.Text)
	return err
}

// workspaceAt is the local workspace whose checkout holds dir, the
// innermost when worktrees nest, with its repository.
func workspaceAt(dir string, repos []wsdomain.Repo, all []wsdomain.Workspace) (wsdomain.Workspace, wsdomain.Repo, bool) {
	dir = realPath(dir)
	var best wsdomain.Workspace
	bestLen := -1
	for _, w := range all {
		if w.HostID != wsdomain.LocalHost || w.Path == "" {
			continue
		}
		if p := realPath(w.Path); within(dir, p) && len(p) > bestLen {
			best, bestLen = w, len(p)
		}
	}
	for _, r := range repos {
		if bestLen >= 0 && r.ID == best.RepoID {
			return best, r, true
		}
	}
	return wsdomain.Workspace{}, wsdomain.Repo{}, false
}

func within(dir, root string) bool {
	rel, err := filepath.Rel(root, dir)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

// ownedByHand says whether the pull request is the user's to address, by
// the rule rounds use: an author workspace, the viewer's own pull request, or
// one taken over. An author workspace elsewhere is an error: the round runs
// there.
func (e *Env) ownedByHand(ctx context.Context, cfg config.Config, repos []wsdomain.Repo, all []wsdomain.Workspace, ref github.Ref, pr *github.PullRequest, cwd string) (bool, error) {
	mine := e.prOf(ctx, pr)
	ws, found, err := findAuthorWorkspace(cfg, repos, all, ref, pr, mine)
	if err != nil {
		return false, err
	}
	if found {
		if !within(realPath(cwd), realPath(ws.Path)) {
			return false, fmt.Errorf("%s is developed in %s; run this command there", ref, ws.Path)
		}
		return true, nil
	}
	if ok, err := mine(); err != nil || ok {
		return ok, err
	}
	overrides, err := e.Rec.Overrides(ctx)
	if err != nil {
		return false, err
	}
	return overrides[ref].Adopted, nil
}

// prepareWorktree links the worktree the turn runs in to the pull request
// when its role allows; it never moves the checkout, never relinks a worktree
// and never links a main checkout.
func (e *Env) prepareWorktree(ctx context.Context, cfg config.Config, ws wsdomain.Workspace, repo wsdomain.Repo, ref github.Ref, role string) error {
	if !strings.EqualFold(repo.RemoteKey, ref.RemoteKey()) {
		return fmt.Errorf("this worktree is a checkout of %s, not of %s/%s", repo.RemoteKey, ref.Owner, ref.Repo)
	}
	switch {
	case ws.LinkedPR == ref.Number:
		return nil
	case ws.LinkedPR != 0:
		return fmt.Errorf("this worktree is linked to %s/%s#%d; run the turn for %s in its own worktree", ref.Owner, ref.Repo, ws.LinkedPR, ref)
	case ws.IsMain:
		return nil
	}
	other, why := workspaceReview, "carries"
	if role == workspaceReview {
		other, why = workspaceAuthor, "lacks"
	}
	if strings.HasPrefix(ws.DisplayName, cfg.GitHub.ReviewPrefix) != (role == workspaceReview) {
		fmt.Fprintf(e.Err, "this worktree is not linked to %s: its name %s the review prefix %q, so linking it would make it the %s workspace\n", ref, why, cfg.GitHub.ReviewPrefix, other)
		return nil
	}
	if err := e.WS.LinkUnit(ctx, ws, ref.Unit()); err != nil {
		return err
	}
	fmt.Fprintf(e.Err, "this worktree is now the %s workspace of %s; "+brand.Name+" did not move its checkout\n", role, ref)
	return nil
}

// takeOverHere takes the pull request over and refuses to go on when the
// author workspace the loop settled on is not the current directory.
func (e *Env) takeOverHere(ctx context.Context, cfg config.Config, ref github.Ref, cwd string) error {
	ws, err := adoptPR(ctx, e, cfg, ref, e.callerAgentHome(cfg))
	if err != nil {
		return err
	}
	if err := e.markAdopted(ctx, ref); err != nil {
		return err
	}
	if !within(realPath(cwd), realPath(ws.Path)) {
		return fmt.Errorf("took %s over in %s; run this command there", ref, ws.Path)
	}
	return nil
}

func (e *Env) recordEvent(ctx context.Context, ref github.Ref, kind, data string) {
	e.recordTurnEvent(ctx, ref, kind, "", data)
}

// recordTurnEvent records an event of turn, keyed by its id.
func (e *Env) recordTurnEvent(ctx context.Context, ref github.Ref, kind, turn, data string) {
	if e.Rec == nil {
		return
	}
	ev := dinv.Event{PR: ref, Kind: kind, Key: turn, At: e.Now(), Data: data}
	if err := e.Rec.Record(context.WithoutCancel(ctx), ev); err != nil {
		fmt.Fprintf(e.Err, "warning: %s: recording %s: %v\n", ref, kind, err)
	}
}

// handRound runs a round asked for by hand on route rt, with the phases of
// the effects d names around it: guide first, unsent when the round
// does not go out, state on the round state once it went out. send gets the
// round state when the route writes it or keepsHome, which remembers the
// caller's agent home in it, and the round with its guidance,
// fills in what the round sent, and reports whether the round went out.
func (e *Env) handRound(ctx context.Context, rt route, ref github.Ref, guidance string, d dintent.Def, keepsHome bool, send func(st *round.Store, r *sentRound) (bool, error)) error {
	r := &sentRound{env: e, ctx: ctx, ref: ref, guidance: guidance}
	runPhase(rt, d, r, guidePhase)
	sent := false
	defer func() {
		if !sent {
			runPhase(rt, d, r, unsentPhase)
		}
	}()
	if !keepsHome && !writesRounds(rt, d, r) {
		var err error
		sent, err = send(nil, r)
		return err
	}
	rounds, err := e.rounds()
	if err != nil {
		return err
	}
	st, err := rounds.Load(ctx)
	if err != nil {
		return err
	}
	if sent, err = send(st, r); err != nil {
		return err
	}
	runPhase(rt, d, r, statePhase(st))
	return rounds.Save(ctx, st)
}

// recordAddressed marks the reviews so far and the checks and the conflict
// of head as covered by a round asked for by hand.
func (e *Env) recordAddressed(st *round.Store, ref github.Ref, head string) {
	st.SetMark(ref.String(), round.AddressedReviews, e.Now().UTC().Format(time.RFC3339))
	st.SetHeads(ref.String(), head, head)
}
