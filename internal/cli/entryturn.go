package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/gtd"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/turn"
)

var errGTDUnavailable = errors.New("the entries are read in the daemon alone")

// entryShortID is an entry's id cut to the first six hex digits after its prefix.
func entryShortID(id gtd.ID) string {
	s := strings.TrimPrefix(string(id), "e-")
	return s[:min(6, len(s))]
}

// nameFacts are the name facts of u's entry, none for a unit of another
// provider or an entry the daemon cannot read.
func (e *Env) nameFacts(ctx context.Context, u unit.Unit) map[string]any {
	id, ok := gtd.IDOf(u)
	if !ok || e.gtd == nil {
		return map[string]any{}
	}
	entry, err := e.gtd.Entry(ctx, id)
	if err != nil {
		return map[string]any{}
	}
	return map[string]any{"entry.title": entry.Title, "entry.short_id": entryShortID(id)}
}

// entryPayload is a GTD entry as its turn reads it: the user's title and
// notes, the work's brief.
type entryPayload struct{ entry gtd.Entry }

func (p entryPayload) Unit() unit.Unit { return gtd.UnitOf(p.entry.ID) }

func (p entryPayload) Arg() string { return p.Unit().String() }

func (p entryPayload) Wording() turn.Wording {
	return turn.Wording{Noun: "entry", This: "this entry", Ref: p.Unit().String(), Short: entryShortID(p.entry.ID)}
}

func (p entryPayload) Fact(string) (any, bool) { return nil, false }

func (p entryPayload) Contents() string {
	if strings.TrimSpace(p.entry.Notes) == "" || p.entry.Notes == p.entry.Title {
		return p.entry.Title
	}
	return p.entry.Title + "\n\n" + p.entry.Notes
}

// entryPayloads reads an entry's turn payload from the daemon's entries.
type entryPayloads struct{ e *Env }

func (r entryPayloads) Payload(ctx context.Context, req turn.Request) (turn.Payload, error) {
	id, _ := gtd.IDOf(req.Unit)
	if r.e.gtd == nil {
		return nil, errGTDUnavailable
	}
	entry, err := r.e.gtd.Entry(ctx, id)
	if err != nil {
		return nil, err
	}
	return entryPayload{entry}, nil
}

// PrepareAhead clones, before the round takes the loop mutex, the
// repository an intent's turn runs in when no manager knows it, and trusts
// it in the agent home when its project's trust_clones says so: only a
// clone made in this call is trusted.
func (a envActions) PrepareAhead(ctx context.Context, name string, u unit.Unit, c daemon.Call) error {
	e := a.call(c)
	d, err := e.intentDef(name)
	if err != nil || d.WorkspaceRepo == nil {
		return err
	}
	cfg, err := e.loadConfig()
	if err != nil {
		return err
	}
	if cfg.AgentHome == "" {
		cfg.AgentHome = c.AgentHome
	}
	run := unitRun{def: d, cfg: cfg, r: turn.Request{Inputs: c.Inputs}, manager: c.Manager}
	repo, err := runRepo(run)
	if err != nil || repo == "" {
		return err
	}
	owner, repoName, _ := strings.Cut(repo, "/")
	cfg = scoped(run)
	if _, err := e.chooseManager(ctx, cfg, intentOptions(d, roundOptions{unit: u})); err != nil {
		return err
	}
	checkout, cloned, err := e.repoCheckout(ctx, cfg, repo)
	if err != nil || !cloned || !cfg.TrustsClones(owner, repoName) {
		return err
	}
	return e.trustClone(ctx, cfg, checkout.Path)
}

// trustClone trusts the repository just cloned at path in cfg's agent home.
func (e *Env) trustClone(ctx context.Context, cfg config.Config, path string) error {
	a, err := e.agent(cfg.Agent)
	if err != nil {
		return err
	}
	t, ok := a.(truster)
	if !ok {
		return fmt.Errorf("trust_clones: agent %q has no folder trust %s can record", cfg.Agent, brand.Name)
	}
	lines, err := t.Trust(ctx, path, config.ExpandHome(e.agentHome(cfg)), false)
	for _, l := range lines {
		fmt.Fprintln(e.Out, l)
	}
	return err
}
