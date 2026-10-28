package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/automation"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	"github.com/wloophq/wloop/internal/profile"
	"github.com/wloophq/wloop/internal/termfmt"
	"github.com/wloophq/wloop/internal/turn"
)

// agentModelChecks names each agent_models entry no agent reads.
func (e *Env) agentModelChecks(cfg config.Config) []check {
	var out []check
	for _, agent := range slices.Sorted(maps.Keys(cfg.AgentModels)) {
		if _, ok := e.Agents[agent]; !ok {
			out = append(out, check{markInfo, "agent_models." + agent, "names no supported agent; its pins are not read",
				"use one of " + strings.Join(slices.Sorted(maps.Keys(e.Agents)), ", ") + " in " + e.ConfigPath})
		}
	}
	return out
}

// profileChecks covers the review profile directory the config names.
func (e *Env) profileChecks(cfg config.Config) []check {
	type named struct{ name, dir string }
	dirs := []named{{"review profile", cfg.Profile}}
	var out []check
	for _, d := range dirs {
		if d.dir == "" {
			continue
		}
		if fi, err := os.Stat(d.dir); err != nil || !fi.IsDir() {
			out = append(out, check{markMissing, d.name, d.dir + " not found; rounds it covers do not go out", "create it, or fix profile in " + e.ConfigPath})
			continue
		}
		out = append(out, check{markOK, d.name, d.dir, ""})
	}
	if len(out) == 0 {
		out = append(out, check{markInfo, "review profile", "none; rounds read the bundled guides ('" + e.command() + " profile show <owner/repo>' shows them)", ""})
	}
	return out
}

// runProfileShow lists the profile's blocks per intent for a repository or
// a pull request, their conditions and which apply there.
func runProfileShow(ctx context.Context, e *Env, args []string) error {
	fs := flag.NewFlagSet("profile show", flag.ContinueOnError)
	fs.SetOutput(e.Err)
	kind := fs.String("intent", "", "an intent with judgment blocks (default: all)")
	show := fs.Bool("show", false, "print the instructions a round there would read")
	var color termfmt.Mode
	fs.Var(&color, "color", "lay the report out for a terminal: auto, always or never")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New(profileShowUsage)
	}
	ref, err := parseRepoArg(pos[0])
	if err != nil {
		return err
	}
	intents := doctorIntents(e.loadedIntents().Registry, *show, ref.Unit().Provider)
	if *kind != "" {
		d, err := e.intentDef(*kind)
		if err != nil {
			return err
		}
		if d.Profile == "" {
			return fmt.Errorf("%s has no judgment blocks", d.Name)
		}
		intents = []dintent.Def{d}
	}
	cfg, err := e.loadConfig()
	if err != nil {
		return err
	}
	cfg = cfg.For(ref.Owner, ref.Repo)
	c, err := e.daemonClient(ctx)
	if err != nil && (ref.Number == 0 || !errors.Is(err, errDaemonDown)) {
		return err
	}
	var st api.DoctorReport
	base := profile.Facts{Values: map[string]any{"pr.owner": ref.Owner, "pr.repo_name": ref.Repo}}
	var read *api.Facts
	if c != nil {
		q := api.DoctorQuery{Repo: ref.Owner + "/" + ref.Repo}
		if ref.Number != 0 {
			q.PR = ref.URL()
		}
		if st, err = c.Doctor(ctx, q); err != nil {
			return err
		}
	}
	var v *repoView
	if t := e.term(color); t.TTY {
		v = &repoView{doctorView: e.doctorView(t)}
		v.layers(profile.Chain(cfg))
	}
	if ref.Number != 0 {
		read, err = e.briefFacts(ctx, c, ref)
		switch {
		case v != nil:
			v.facts(ref, read, err)
		case err != nil:
			fmt.Fprintf(e.Out, "brief facts unavailable: %v\n", err)
		default:
			fmt.Fprintf(e.Out, "facts of %s: %s\n", ref, describeFacts(*read))
		}
		if err == nil {
			base = profileFacts(*read)
			e.facts = factsTurn{*read}
		}
	}
	failed := 0
	l := e.loadedIntents()
	tree, treeErr := profile.Load(profile.Chain(cfg), l.Registry, l.BrokenNames())
	for _, d := range intents {
		k := d.Name
		if treeErr != nil {
			if v != nil {
				v.broken(string(k), treeErr)
			} else {
				fmt.Fprintf(e.Out, "%-8s %v\n", k, treeErr)
			}
			failed++
			continue
		}
		if d.Profile != "" {
			list, g, changed := tree.List(profile.Kind(d.Profile), base), tree.Compose(profile.Kind(d.Profile), base), slices.Contains(st.BundledChanged, string(k))
			if v != nil {
				v.blocks(string(k), list, g, changed)
			} else {
				e.printBlocks(string(k), list, g, changed)
			}
		} else if v != nil && *show {
			v.section(string(k), nil)
		}
		if *show {
			_, out, err := e.renderTurn(ctx, cfg, placeholderTurnOf(d, ref, read))
			if err != nil {
				return err
			}
			if v != nil {
				v.show(out.Sources, out.Text)
			} else {
				fmt.Fprintf(e.Out, "\nsources: %s\n\n%s\n", strings.Join(out.Sources, "; "), out.Text)
			}
		}
	}
	if *kind == "" {
		for _, name := range slices.Sorted(maps.Keys(l.Broken)) {
			if v != nil {
				v.broken(string(name), l.Broken[name])
			} else {
				fmt.Fprintf(e.Out, "%-8s does not load:\n  %s\n", name, strings.ReplaceAll(l.Broken[name].Error(), "\n", "\n  "))
			}
			failed++
		}
		for _, name := range slices.Sorted(maps.Keys(l.Reverted)) {
			if v != nil {
				v.reverted(string(name), l.Reverted[name])
			} else {
				fmt.Fprintf(e.Out, "%-8s runs as bundled, the override does not load:\n  %s\n", name, strings.ReplaceAll(l.Reverted[name].Error(), "\n", "\n  "))
			}
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d kind(s) cannot run in %s/%s", failed, ref.Owner, ref.Repo)
	}
	return nil
}

// placeholderTurn is a turn asked for by hand on a pull request of ref's
// repository, with nothing read from GitHub.
func placeholderTurn(d dintent.Def, ref pr.Ref) turn.Request {
	inputs := map[string]any{}
	for _, in := range d.Inputs {
		if in.Type == "text" {
			inputs[in.Name] = "<" + in.Name + ">"
		}
	}
	return turn.Request{Intent: turn.Intent(d.Name), Trigger: turn.ByHand, Unit: ref.Unit(), Payload: &turn.PR{}, Owned: true, Inputs: inputs}
}

// doctorIntents are the intents a round on a pull request can run, in name
// order: the ones that read judgment blocks, and with --show every
// user-facing one whose context is a pull request and that serves units of p.
func doctorIntents(r *dintent.Registry, show bool, p unit.ProviderID) []dintent.Def {
	var out []dintent.Def
	for _, d := range r.All() {
		if d.Profile != "" || (show && d.UserFacing && d.Context == contextPR && d.Serves(p)) {
			out = append(out, d)
		}
	}
	return out
}

// placeholderTurnOf is placeholderTurn with the facts the daemon read of
// the pull request, when it did.
func placeholderTurnOf(d dintent.Def, ref pr.Ref, f *api.Facts) turn.Request {
	r := placeholderTurn(d, ref)
	if f != nil {
		r.Payload = &turn.PR{Author: f.Author, Association: f.Association, Base: f.Base, Labels: f.Labels}
	}
	return r
}

// briefFacts asks the daemon what the pull request's guide is matched
// against.
func (e *Env) briefFacts(ctx context.Context, c *client.Client, ref pr.Ref) (*api.Facts, error) {
	if c == nil {
		return nil, errors.New("the daemon is not running")
	}
	f, err := c.Facts(ctx, ref.URL())
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func profileFacts(f api.Facts) profile.Facts {
	return profile.Facts{Values: map[string]any{"pr.owner": f.Owner, "pr.repo_name": f.Repo, "pr.author": f.Author, "pr.association": f.Association, "pr.base": f.Base,
		"pr.requested_teams": f.Teams, "pr.labels": f.Labels, "brief.kind": f.Kind, "brief.size": f.Size, "brief.risk": f.Risk},
		Tags: f.Tags, Files: f.Files, HasBrief: f.HasBrief}
}

func describeFacts(f api.Facts) string {
	parts := []string{"author " + orDash(f.Author), "association " + orDash(f.Association), "base " + orDash(f.Base)}
	if len(f.Teams) > 0 {
		parts = append(parts, "teams "+strings.Join(f.Teams, ", "))
	}
	if len(f.Labels) > 0 {
		parts = append(parts, "labels "+strings.Join(f.Labels, ", "))
	}
	if !f.HasBrief {
		return strings.Join(append(parts, "no brief"), "; ")
	}
	parts = append(parts, fmt.Sprintf("brief: %s, size %s, risk %s", f.Kind, f.Size, f.Risk))
	if len(f.Tags) > 0 {
		parts = append(parts, "tags "+strings.Join(f.Tags, ", "))
	}
	if len(f.Files) > 0 {
		parts = append(parts, fmt.Sprintf("%d files", len(f.Files)))
	}
	return strings.Join(parts, "; ")
}

var profileShowUsage = "usage: " + brand.Command + " profile show <owner/repo|pr-url> [--intent <intent>] [--show] [--color auto|always|never]"

func parseRepoArg(v string) (pr.Ref, error) {
	if strings.HasPrefix(v, "https://") {
		return pr.ParseURL(v)
	}
	owner, repo, ok := strings.Cut(v, "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return pr.Ref{}, errors.New(profileShowUsage)
	}
	return pr.Ref{Owner: owner, Repo: repo}, nil
}

// printBlocks lists the blocks of kind with their conditions and what each
// does in the repository; bundledChanged says the bundled guide changed since
// the repository's last round of kind.
func (e *Env) printBlocks(kind string, blocks []profile.Listed, g profile.Guide, bundledChanged bool) {
	label := kind
	for _, l := range blocks {
		state := l.State
		if l.Block.FM.Override {
			state += ", replaces the guidance above it"
			if bundledChanged && l.Block.Path.Root() {
				state += " (bundled changed since the last round here)"
			}
		}
		fmt.Fprintf(e.Out, "%-8s %-8s %s   %s   %s\n", label, l.Block.Layer.Name, l.Block.File, l.Conditions, state)
		label = ""
	}
	agents := make([]string, 0, len(g.Delegates))
	for a := range g.Delegates {
		agents = append(agents, a)
	}
	sort.Strings(agents)
	for _, a := range agents {
		if len(g.Delegates[a]) > 0 {
			fmt.Fprintf(e.Out, "%-8s delegates %s: %s   (%s)\n", "", a, strings.Join(g.Delegates[a], ", "), g.DelegatesFrom)
		}
	}
}

// doctorState runs, in the daemon, doctor's checks that read the loop's state,
// and the state `profile show` reads for q.
func (e *Env) doctorState(ctx context.Context, q api.DoctorQuery) (api.DoctorReport, error) {
	rep := api.DoctorReport{Checks: []api.DoctorCheck{}}
	rounds, err := e.rounds()
	if err != nil {
		return rep, err
	}
	st, err := rounds.Load(ctx)
	if err != nil {
		return rep, err
	}
	if held := st.HeldToday(e.Now()); len(held) > 0 {
		rep.Checks = append(rep.Checks, api.DoctorCheck{Mark: api.DoctorMissing, Name: "automated rounds held by a cap today", Info: strings.Join(held, ", "),
			Fix: "raise turns.per_unit_per_day in " + e.ConfigPath + ", or run " + e.command() + " <intent> <url> by hand"})
	}
	p, err := rounds.Pause(ctx)
	if err != nil {
		return rep, err
	}
	if p.Any() {
		info := "every workflow that does not observe and board moves paused"
		if !p.On {
			info += " on " + strings.Join(p.Repos, ", ")
		}
		info += " since " + p.At.Local().Format("2006-01-02 15:04")
		rep.Checks = append(rep.Checks, api.DoctorCheck{Mark: api.DoctorInfo, Name: "paused", Info: info + "; " + e.command() + " resume restarts them"})
	}
	if q.Repo == "" {
		return rep, nil
	}
	store, err := e.store()
	if err != nil {
		return rep, err
	}
	for _, d := range doctorIntents(e.loadedIntents().Registry, false, "") {
		last, ok, err := store.LastBundledSHA(ctx, string(d.Name), q.Repo)
		if err != nil {
			return rep, err
		}
		if ok && last != "" && last != profile.BundledSHA(profile.Kind(d.Profile)) {
			rep.BundledChanged = append(rep.BundledChanged, string(d.Name))
		}
	}
	return rep, nil
}

// staleCursor is how long an event may wait for its workflow before doctor
// calls the workflow's cursor stuck: the engine holds one at most an hour.
const staleCursor = time.Hour

// workflowReads are the store reads doctor's workflow checks make.
type workflowReads interface {
	WorkflowActivity(ctx context.Context, since time.Time) (map[string]sqlite.WorkflowActivity, error)
	Events() automation.Events
	Ledger() automation.Ledger
}

// doctorWith is doctorState with the checks of the workflows, read from db.
func (e *Env) doctorWith(db workflowReads) func(context.Context, api.DoctorQuery) (api.DoctorReport, error) {
	return func(ctx context.Context, q api.DoctorQuery) (api.DoctorReport, error) {
		rep, err := e.doctorState(ctx, q)
		if err != nil || q.Repo != "" || q.PR != "" {
			return rep, err
		}
		rep.Workflows, err = e.workflowChecks(ctx, db)
		if err != nil {
			rep.Workflows = append(rep.Workflows, api.DoctorCheck{Mark: api.DoctorMissing, Name: "workflows", Info: "reading what they did: " + err.Error(),
				Fix: "see " + filepath.Join(e.StateDir, "serve.err.log")})
		}
		return rep, nil
	}
}

// workflowChecks are a line per workflow with its mode, last run and the
// refusals of the last day; a stuck cursor, a broken workflow and a config
// entry that names no workflow are each missing.
func (e *Env) workflowChecks(ctx context.Context, db workflowReads) ([]api.DoctorCheck, error) {
	l := e.loadedIntents()
	out := []api.DoctorCheck{}
	cfg, err := e.loadConfig()
	if err != nil {
		out = append(out, api.DoctorCheck{Mark: api.DoctorMissing, Name: "workflows config",
			Info: "not read, so the modes below are the profile's modes and workflows.<name> entries go unchecked: " + err.Error(), Fix: e.configFix(err)})
	}
	now := e.Now()
	activity, err := db.WorkflowActivity(ctx, now.Add(-24*time.Hour))
	if err != nil {
		return out, err
	}
	known := map[string]bool{}
	for _, w := range l.Workflows {
		known[w.Name] = true
		name := "workflow " + w.Name
		if wc := cfg.Workflows[w.Name]; wc.Enabled != nil && !*wc.Enabled {
			out = append(out, api.DoctorCheck{Mark: api.DoctorInfo, Name: name, Info: "disabled by workflows." + w.Name + ".enabled in " + e.ConfigPath})
			continue
		}
		w = applyConfigWith([]workflow.Workflow{w}, cfg, briefExtrasOf(l))[0]
		c, err := e.workflowLine(ctx, db, w, activity[w.Name], now)
		if err != nil {
			return out, err
		}
		c.Name = name
		out = append(out, c)
		if w.Trigger.Kind != workflow.TriggerEvent {
			continue
		}
		if c, err := stuckCursor(ctx, db.Events(), w, now); err != nil {
			return out, err
		} else if c != nil {
			c.Fix = "see " + filepath.Join(e.StateDir, "serve.err.log") + " for why it fails"
			out = append(out, *c)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(l.BrokenWorkflows)) {
		known[name] = true
		out = append(out, api.DoctorCheck{Mark: api.DoctorMissing, Name: "workflow " + name,
			Info: "disabled, every other workflow runs: " + oneLine(l.BrokenWorkflows[name].Error()), Fix: brand.Command + " profile check"})
	}
	for _, name := range slices.Sorted(maps.Keys(l.RevertedWorkflows)) {
		out = append(out, api.DoctorCheck{Mark: api.DoctorMissing, Name: "workflow " + name + " override",
			Info: "runs as bundled, the override does not load: " + oneLine(l.RevertedWorkflows[name].Error()), Fix: brand.Command + " profile check"})
	}
	for _, err := range configWorkflowProblems(cfg, l) {
		out = append(out, api.DoctorCheck{Mark: api.DoctorMissing, Name: "config workflows",
			Info: oneLine(err.Error()), Fix: "fix it in " + e.ConfigPath})
	}
	for _, name := range slices.Sorted(maps.Keys(cfg.Workflows)) {
		if !known[name] {
			out = append(out, api.DoctorCheck{Mark: api.DoctorMissing, Name: "config workflows." + name,
				Info: "names no workflow, so it changes nothing", Fix: "rename or remove workflows." + name + " in " + e.ConfigPath})
		}
	}
	return out, nil
}

// workflowLine is w's trigger and what it did: an observe workflow
// the items it plans, which it never runs, and the config entry that makes
// it run them; a workflow whose items wait for the user the entry that
// runs them without the user.
func (e *Env) workflowLine(ctx context.Context, db workflowReads, w workflow.Workflow, a sqlite.WorkflowActivity, now time.Time) (api.DoctorCheck, error) {
	var info string
	mark := api.DoctorOK
	if w.Observe {
		items, err := db.Ledger().Items(ctx, automation.Filter{Workflow: w.Name})
		if err != nil {
			return api.DoctorCheck{}, err
		}
		mark = api.DoctorInfo
		info = fmt.Sprintf("observe, %s, %d item(s) planned and none run; see %s plan --workflow %s; set %s in %s to run them",
			w.Trigger.Kind, len(items), brand.Command, w.Name, workflowTask(w.Name, "observe", "false"), e.ConfigPath)
	} else {
		last := "never ran"
		if !a.LastRun.IsZero() {
			last = "last ran " + shortAge(now.Sub(a.LastRun), false) + " ago"
		}
		info = fmt.Sprintf("%s, %s, %d refusal(s) in the last day", w.Trigger.Kind, last, a.Refusals)
	}
	if w.Approval.Mode == workflow.ApprovalWait {
		info += fmt.Sprintf("; each item waits for you to apply it; set %s in %s to run them without you",
			workflowTask(w.Name, "approval.mode", `"run"`), e.ConfigPath)
	}
	return api.DoctorCheck{Mark: mark, Info: info}, nil
}

// workflowTask is the config.toml line that sets field of workflow name to value.
func workflowTask(name, field, value string) string {
	return config.Key("workflows", name) + "." + field + " = " + value
}

// stuckCursor is a check when an event w handles has waited past
// staleCursor; nil otherwise.
func stuckCursor(ctx context.Context, evs automation.Events, w workflow.Workflow, now time.Time) (*api.DoctorCheck, error) {
	cursor, _, ok, err := evs.Cursor(ctx, w.Name)
	if err != nil || !ok {
		return nil, err
	}
	waiting, err := evs.Since(ctx, cursor, w.Trigger.On)
	if err != nil || len(waiting) == 0 || now.Sub(waiting[0].At) <= staleCursor {
		return nil, err
	}
	first := waiting[0]
	return &api.DoctorCheck{Mark: api.DoctorMissing, Name: "workflow " + w.Name + " cursor",
		Info: fmt.Sprintf("stuck at event %d: %d event(s) not handled, the oldest (%s) %s ago", cursor, len(waiting), first.Kind, shortAge(now.Sub(first.At), false))}, nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
