package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"sync"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/instructions"
	"github.com/wloophq/wloop/internal/core/turns"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/pr"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/turn"
	"github.com/wloophq/wloop/internal/workspace"
)

// probeModels is the model each probe's first launch asked for, which its
// resumes reuse; the daemon outlives a probe, so memory is enough.
var probeModels = struct {
	sync.Mutex
	m map[pr.Ref]string
}{m: map[pr.Ref]string{}}

func rememberProbeModel(ref pr.Ref, model string) {
	probeModels.Lock()
	defer probeModels.Unlock()
	probeModels.m[ref] = model
}

func probeModel(ref pr.Ref) string {
	probeModels.Lock()
	defer probeModels.Unlock()
	return probeModels.m[ref]
}

// probe sends a probe turn in a new session: a first launch in l.WorkspaceID
// with l.Home and l.Model, a resume in the workspace and home of the session
// the resumed turn was sent to. It reads nothing from GitHub.
func (c cliLauncher) probe(ctx context.Context, d dintent.Def, l turns.Launch) error {
	e := c.e
	if e.Turns == nil {
		return errors.New("a probe runs in the daemon")
	}
	ref, err := githubRef(l.Unit)
	if err != nil {
		return err
	}
	r := turn.Request{Intent: turn.Intent(d.Name), Trigger: requestTrigger(l.Trigger), Unit: ref.Unit()}
	wsID, home, model := l.WorkspaceID, l.Home, l.Model
	books, err := e.Turns.Store.Books(ctx, l.Unit)
	if err != nil {
		return err
	}
	b := books[l.Unit]
	b.Unit = l.Unit
	if l.ResumeOf != 0 {
		prev, ok := turnOf(b, l.ResumeOf)
		if !ok {
			return fmt.Errorf("%s has no turn %d to resume", ref, l.ResumeOf)
		}
		r.Trigger = requestTrigger(prev.Trigger)
		r.Resume = instructions.ResumeOf(b, l.ResumeOf, l.Answer, "", "")
		if wsID == "" {
			s, ok, err := e.Turns.Registry.Session(ctx, prev.SentTo)
			if err != nil {
				return err
			}
			if !ok {
				return fmt.Errorf("%w: %s: the session %q turn %d was sent to is unknown, so the probe has no workspace to resume in", turns.ErrWorkspaceGone, ref, prev.SentTo, l.ResumeOf)
			}
			wsID, home = s.WorkspaceID, s.Home
		}
		if model == "" {
			model = probeModel(prOf(l.Unit))
		}
	} else {
		rememberProbeModel(prOf(l.Unit), model)
	}
	if wsID == "" {
		return errors.New("a probe needs the workspace to run in")
	}
	if r.History, err = e.turnHistory(ctx, l.Unit, b); err != nil {
		return err
	}
	loaded, err := e.loadConfig()
	if err != nil {
		return err
	}
	cfg := loaded.For(ref.Owner, ref.Repo)
	cfg.AgentHome = home
	if home != "" {
		cfg.Agent = e.kindOfHome(loaded, home)
	}
	r.Agent = cfg.Agent
	ws, ok, err := e.findFresh(ctx, wsID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: no workspace %s in %s", turns.ErrWorkspaceGone, wsID, e.managerDisplay())
	}
	a, err := e.agent(cfg.Agent)
	if err != nil {
		return err
	}
	m, err := e.WS.For(ctx, ws)
	if err != nil {
		return err
	}
	s := turns.AgentSession{Worktree: ws.Path, Home: home, Model: model, Policy: policies[d.Policy](e, d.Name, e.controlSurface(loaded)), Trusted: true}
	if err := workspace.Honors(m, needs(a, cfg, s.Policy)); err != nil {
		return fmt.Errorf("%s owns %s and cannot launch the probe's session: %w", e.displayOf([]string{m.Name()}), ws.DisplayName, err)
	}
	if err := e.preflight(ctx, a, s, m); err != nil {
		return err
	}
	sent, err := e.storeTurn(ctx, cfg, r)
	if err != nil {
		return err
	}
	sent.resume(l.ResumeOf)
	name, err := e.sessionName(dintent.Name(r.Intent), r.Unit)
	if err != nil {
		return err
	}
	p, err := e.startSession(ctx, cfg, ws, r.Unit, s, name, func(string) bool { return true }, true)
	if err != nil {
		return fmt.Errorf("%s: starting the agent: %w", ws.DisplayName, err)
	}
	if _, err := e.deliver(ctx, ref.Unit(), &sent, p, false); err != nil {
		return fmt.Errorf("%s: sending the prompt: %w", ws.DisplayName, err)
	}
	return nil
}

// findFresh finds the workspace in WS's list, and asks the managers again
// before it says the workspace is gone.
func (e *Env) findFresh(ctx context.Context, id string) (wsdomain.Workspace, bool, error) {
	for range 2 {
		all, err := e.WS.List(ctx)
		if err != nil {
			return wsdomain.Workspace{}, false, err
		}
		if ws, ok := findWorkspace(all, id); ok || !e.WS.Snapshots() {
			return ws, ok, nil
		}
		e.WS.Invalidate(wsdomain.PartWorkspaces)
	}
	return wsdomain.Workspace{}, false, nil
}

// findWorkspace finds the workspace by its manager's id or its worktree path.
func findWorkspace(all []wsdomain.Workspace, id string) (wsdomain.Workspace, bool) {
	for _, w := range all {
		if w.ID == id || w.Path != "" && w.Path == id {
			return w, true
		}
	}
	return wsdomain.Workspace{}, false
}

// runProbe has the daemon send a probe turn into a new session of a
// workspace and prints where it went.
func runProbe(ctx context.Context, e *Env, args []string) error {
	const usage = "usage: probe --workspace <workspace-id-or-path> [--home <dir>] [--model <name>]"
	fs := flag.NewFlagSet("probe", flag.ContinueOnError)
	fs.SetOutput(e.Err)
	ws := fs.String("workspace", "", "the workspace the probe's session runs in: its "+e.managerDisplay()+" id or its worktree path")
	home := fs.String("home", "", "the agent home the session runs with (default: the agent's own)")
	model := fs.String("model", "", "the agent's model (default: the agent's own)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *ws == "" || fs.NArg() > 0 {
		return errors.New(usage)
	}
	c, err := e.daemonClient(ctx)
	if err != nil {
		return err
	}
	res, err := c.Probe(ctx, api.ProbeRequest{WorkspaceID: *ws, Home: *home, Model: *model})
	if err != nil {
		return err
	}
	enc := json.NewEncoder(e.Out)
	enc.SetIndent("", "  ")
	return enc.Encode(res)
}
