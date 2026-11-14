package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/domain/pr"

	psource "github.com/wloophq/wloop/internal/plugins/source"
	src "github.com/wloophq/wloop/pkg/plugin/source"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/daemon"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
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
// session, with the inputs the daemon checked; the turn's start waits on the
// loop and prepares them.
func (e *Env) byHand() func(ctx context.Context, r api.ByHandRequest, handle string) (api.ByHandResult, error) {
	return func(ctx context.Context, r api.ByHandRequest, handle string) (api.ByHandResult, error) {
		d, err := e.intentDef(r.Intent)
		if err != nil {
			return api.ByHandResult{}, daemon.BadRequest(err.Error())
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

// turnByHand runs a turn from the agent session the user is in: it writes
// the turn's instructions to text, starts the turn on the caller's session
// and reports everything else on e.Out.
func (e *Env) turnByHand(ctx context.Context, h handTurn, text io.Writer) error {
	if e.Turns == nil || e.Turns.Starter == nil {
		return errors.New("no turn starts here: the daemon runs a turn by hand")
	}
	u, err := e.byHandRef(ctx, api.ByHandRequest{URL: h.url, Cwd: h.cwd}, h.handle)
	if err != nil {
		return err
	}
	_, err = e.Turns.Starter.Start(ctx, turns.Request{Unit: u, Intent: dturn.Intent(h.def.Name), Guidance: h.guidance, Inputs: h.inputs,
		Trigger: dturn.ByHand, Route: src.RouteSession, ByPerson: true}, &handRunner{e: e, h: h, u: u, text: text})
	if errors.Is(err, turns.ErrAnswered) {
		return nil
	}
	return err
}

// handRunner runs a turn by hand on the caller's session: its guard reads
// and readies the unit, refusing before any effect applies, and its run
// renders the turn and starts it there.
type handRunner struct {
	e    *Env
	h    handTurn
	u    unit.Unit
	text io.Writer

	cfg config.Config
	req turn.Request
	pr  *pr.PullRequest
}

func (x *handRunner) Guard(ctx context.Context, t turns.Turn) error {
	e, d := x.e, x.h.def
	cfg, err := e.loadConfig()
	if err != nil {
		return err
	}
	x.cfg = cfg
	if !isPR(x.u) {
		if !d.Serves(x.u.Provider) {
			return fmt.Errorf("the intent %s serves %s units, not %s", d.Name, d.ProviderOrDefault(), x.u.Label())
		}
		x.req = turn.Request{Intent: turn.Intent(d.Name), Trigger: turn.ByHand, Unit: x.u, Agent: x.h.agent}
		return nil
	}
	ref := prOf(x.u)
	if x.pr, err = e.openPR(ctx, ref); err != nil {
		return err
	}
	inputs := t.Inputs()
	takeOver := beforeSet(d, inputs, beforeTakeOver)
	x.req = turn.Request{Intent: turn.Intent(d.Name), Trigger: turn.ByHand, Unit: ref.Unit(), Payload: turnPR(x.pr), Agent: x.h.agent}
	e.withAddressReason(ctx, cfg, d, &x.req)
	repos, all := e.workspaceState(ctx)
	ws, repo, inWS := workspaceAt(x.h.cwd, repos, all)
	if d.OwnerOnly {
		owned, err := e.ownedByHand(ctx, cfg.For(ref.Owner, ref.Repo), repos, all, ref, x.pr, x.h.cwd)
		if err != nil {
			return err
		}
		if !owned && !takeOver {
			return x.askTakeOver(ctx, inputs)
		}
		x.req.Owned = true
	}
	if inWS && (d.Workspace == workspaceReview || d.Workspace == workspaceAuthor) {
		if err := e.prepareWorktree(ctx, cfg.For(ref.Owner, ref.Repo), ws, repo, ref, d.Workspace); err != nil {
			return err
		}
	}
	if takeOver {
		return e.takeOverHere(ctx, cfg, ref, x.h.cwd)
	}
	return nil
}

// askTakeOver writes the question an owner-only turn asks on a pull request
// the user does not own, and starts no turn.
func (x *handRunner) askTakeOver(ctx context.Context, inputs map[string]any) error {
	r := x.req
	r.Inputs = inputs
	_, out, err := x.e.renderTurn(ctx, x.cfg, r)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprint(x.text, out.Text); err != nil {
		return err
	}
	return turns.ErrAnswered
}

func (x *handRunner) KeptBack(string) {
	if isPR(x.u) {
		x.e.noteKeptBack(prOf(x.u))
	}
}

func (x *handRunner) Run(ctx context.Context, t turns.Turn) (turns.Outcome, error) {
	e := x.e
	r := x.req
	r.Guidance, r.Inputs = t.Guidance(), t.Inputs()
	sent, err := e.sendTurn(ctx, x.cfg, t, r)
	if err != nil {
		return turns.Outcome{}, err
	}
	o := turns.Outcome{ID: sent.ID, Submitted: true}
	if x.pr != nil {
		o.Head = x.pr.HeadRefOid
	}
	if sent.send != nil {
		if _, err := e.Turns.StartByHand(ctx, sent.turn, *sent.send, x.h.handle); err != nil {
			return turns.Outcome{}, err
		}
	}
	if _, err := fmt.Fprint(x.text, sent.Text); err != nil {
		return o, err
	}
	return o, nil
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
			return pr.Ref{Owner: owner, Repo: name, Number: ws.LinkedPR}.Unit(), nil
		}
	}
	var errs []error
	for _, l := range e.locators() {
		u, ok, err := l.Locate(ctx, cwd)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if ok {
			return u, nil
		}
	}
	if err := errors.Join(errs...); err != nil {
		return unit.Unit{}, fmt.Errorf("no unit here; pass its target: %w", err)
	}
	return unit.Unit{}, errors.New("no unit here; pass its target")
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
func (e *Env) ownedByHand(ctx context.Context, cfg config.Config, repos []wsdomain.Repo, all []wsdomain.Workspace, ref pr.Ref, pr *pr.PullRequest, cwd string) (bool, error) {
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
func (e *Env) prepareWorktree(ctx context.Context, cfg config.Config, ws wsdomain.Workspace, repo wsdomain.Repo, ref pr.Ref, role string) error {
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
func (e *Env) takeOverHere(ctx context.Context, cfg config.Config, ref pr.Ref, cwd string) error {
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

func (e *Env) recordEvent(ctx context.Context, ref pr.Ref, kind, data string) {
	e.recordTurnEvent(ctx, ref, kind, "", data)
}

// recordTurnEvent records an event of turn, keyed by its id.
func (e *Env) recordTurnEvent(ctx context.Context, ref pr.Ref, kind, turn, data string) {
	if e.Rec == nil {
		return
	}
	ev := dinv.Event{PR: ref, Kind: kind, Key: turn, At: e.Now(), Data: data}
	if err := e.Rec.Record(context.WithoutCancel(ctx), ev); err != nil {
		fmt.Fprintf(e.Err, "warning: %s: recording %s: %v\n", ref, kind, err)
	}
}

// locators know the unit a checkout holds: each source plugin's this
// process runs.
func (e *Env) locators() []unit.Locator {
	var out []unit.Locator
	if e.pluginKind == nil {
		return out
	}
	units := psource.Units{Codecs: e.unitCodecs()}
	for _, p := range e.plugins {
		if slices.Contains(p.Manifest.Kinds, src.Kind) {
			out = append(out, psource.Checkouts{ID: p.ID(), Units: units, Kind: e.pluginKind})
		}
	}
	return out
}
