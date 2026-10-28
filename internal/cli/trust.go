package cli

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/daemon"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type truster interface {
	Trust(ctx context.Context, folder, home string, exact bool) ([]string, error)
}

// runTrust records, in an agent home, the trust the agent's own dialog
// records for a folder's repository. It is the only trust the loop writes,
// and only the user runs it.
func runTrust(ctx context.Context, e *Env, args []string) error {
	const usage = "usage: trust [--home <dir>] [--exact] <folder|workspace>"
	fs := flag.NewFlagSet("trust", flag.ContinueOnError)
	fs.SetOutput(e.Err)
	home := fs.String("home", "", "the agent home that trusts it (default: the configured agent home)")
	exact := fs.Bool("exact", false, "trust the folder itself rather than its repository's main checkout")
	rest, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(rest) != 1 {
		return errors.New(usage)
	}
	cfg, err := e.loadConfig()
	if err != nil {
		return err
	}
	kind := cfg.Agent
	if *home != "" {
		kind = e.kindOfHome(cfg, *home)
	}
	folder, err := e.trustFolder(ctx, rest[0])
	if err != nil {
		return err
	}
	return e.trustIn(ctx, kind, folder, []string{config.ExpandHome(cmp.Or(*home, e.agentHome(cfg)))}, *exact)
}

// trustIn trusts folder in each of homes with the agent kind's own trust,
// printing what it wrote: with exact, the folder alone, else its
// repository's main checkout.
func (e *Env) trustIn(ctx context.Context, kind, folder string, homes []string, exact bool) error {
	a, err := e.agent(kind)
	if err != nil {
		return err
	}
	t, ok := a.(truster)
	if !ok {
		return fmt.Errorf("agent %q has no folder trust "+brand.Name+" can record", kind)
	}
	var errs []error
	for _, home := range homes {
		lines, err := t.Trust(ctx, folder, home, exact)
		for _, l := range lines {
			fmt.Fprintln(e.Out, l)
		}
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// trustFolder is arg as a folder, or the checkout of the workspace whose id
// or name it is.
func (e *Env) trustFolder(ctx context.Context, arg string) (string, error) {
	if fi, err := os.Stat(config.ExpandHome(arg)); err == nil && fi.IsDir() {
		return filepath.Abs(config.ExpandHome(arg))
	}
	if e.WS == nil {
		return "", fmt.Errorf("%s is not a folder", arg)
	}
	all, err := e.WS.List(ctx)
	if err != nil {
		return "", fmt.Errorf("%s is not a folder, and listing the %s workspaces: %w", arg, e.managerDisplay(), err)
	}
	for _, w := range all {
		if w.ID == arg || strings.EqualFold(w.DisplayName, arg) {
			return w.Path, nil
		}
	}
	return "", fmt.Errorf("%s is neither a folder nor %s workspace", arg, withArticle(e.managerDisplay()))
}

func withArticle(name string) string {
	if name != "" && strings.ContainsRune("AEIOUaeiou", rune(name[0])) {
		return "an " + name
	}
	return "a " + name
}

// TrustAndRun trusts the folder the round of ref is blocked on, lifts the
// block and sends the round now. Only the user's action calls it.
func (a envActions) TrustAndRun(ctx context.Context, u unit.Unit, c daemon.Call) error {
	ref := prOf(u)
	e, cfg, profile, err := a.prepare(ctx, ref, c)
	if err != nil {
		return err
	}
	b, err := e.startBlockOf(ctx, u)
	if err != nil {
		return err
	}
	if b.Question != turns.QuestionTrust {
		return fmt.Errorf("the round of %s is not blocked on an untrusted folder", ref)
	}
	pcfg := prConfig(cfg, ref, profile)
	pr, _, err := e.readPR(ctx, pcfg, ref)
	if err != nil {
		pr = nil
	}
	if err := e.trustBlocked(ctx, pcfg, b, !pcfg.ProjectSettingsFor(string(e.blockedIntent(b)), prFacts(pr))); err != nil {
		return err
	}
	ev := dinv.Event{PR: ref, Kind: dinv.KindRoundUnblocked, At: e.Now(), Data: "you trusted its folder"}
	if err := e.Rec.Record(ctx, ev); err != nil {
		return err
	}
	return runHeldRound(ctx, e, cfg, ref, profile)
}

// Unblock marks handled the trigger of the round blocked on ref, so no sweep
// starts it again before a new one.
func (a envActions) Unblock(ctx context.Context, u unit.Unit, c daemon.Call) error {
	ref := prOf(u)
	e := a.call(c)
	b, err := e.startBlockOf(ctx, u)
	if err != nil {
		return err
	}
	err = e.withState(ctx, func(s *sweep) error {
		setMarks(s.st, u.Key, b.Skip)
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(e.Out, "%s: the blocked round waits for a new trigger\n", ref)
	return nil
}

func (e *Env) startBlockOf(ctx context.Context, u unit.Unit) (inventory.StartBlock, error) {
	if e.Rec == nil {
		return inventory.StartBlock{}, errNoStore
	}
	blocks, err := e.Rec.FirstRoundBlocks(ctx)
	if err != nil {
		return inventory.StartBlock{}, err
	}
	b, ok := inventory.StartBlockOf(blocks[u])
	if !ok {
		return inventory.StartBlock{}, fmt.Errorf("no round of %s is blocked on the agent's question", u.Label())
	}
	return b, nil
}

// trustHomes are the agent homes trusting a folder writes in for a session
// that ran with home: home itself, or, for the terminal's own home, the
// trust command's default home and the workspace manager's profile homes
// that exist.
func (e *Env) trustHomes(cfg config.Config, home string) []string {
	if home != "" {
		return []string{home}
	}
	homes := []string{e.agentHome(cfg)}
	for _, m := range e.enabledEntries(cfg) {
		if m.Homes != nil {
			homes = append(homes, m.Homes(e.Home, cfg.Agent)...)
		}
	}
	present, _ := existingHomes(cleanHomes(homes))
	return present
}

// trustBlocked trusts b's folder in the agent homes the block names: with
// exact, the worktree alone, else its repository's main checkout.
func (e *Env) trustBlocked(ctx context.Context, cfg config.Config, b inventory.StartBlock, exact bool) error {
	if len(b.Homes) == 0 {
		return errors.New("no agent home to trust the folder in")
	}
	kind := cfg.Agent
	if b.Home != "" {
		kind = e.kindOfHome(cfg, b.Home)
	}
	folder := cmp.Or(b.Root, b.Folder)
	if exact {
		folder = cmp.Or(b.Folder, b.Root)
	}
	return e.trustIn(ctx, kind, folder, b.Homes, exact)
}
