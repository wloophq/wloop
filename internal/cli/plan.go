package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
)

var planUsage = "usage: " + brand.Command + " plan [--workflow <name>] [--provider <id>] [--stored] | plan --unit <pr-url|owner/repo#n|provider:key|id> | plan apply <id|pr-url>... | plan dismiss <id|pr-url>..."

// planOrder is the order plan prints its decisions in, those that need the
// user first.
var planOrder = []workflow.Decision{workflow.DecisionPending, workflow.DecisionBlocked, workflow.DecisionRun, workflow.DecisionSkipped}

// shortID is how many characters of an item's key plan prints as its id.
const shortID = 8

func runPlan(ctx context.Context, e *Env, args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "apply":
			if len(args) < 2 {
				return errors.New(planUsage)
			}
			return runPlanAct(ctx, e, "apply", args[1:])
		case "dismiss":
			if len(args) < 2 {
				return errors.New(planUsage)
			}
			return runPlanAct(ctx, e, "dismiss", args[1:])
		}
	}
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	fs.SetOutput(e.Err)
	wf := fs.String("workflow", "", "only this workflow's items")
	provider := fs.String("provider", "", "only the units of this provider: github, session or workspace")
	stored := fs.Bool("stored", false, "the plan the daemon holds, of every trigger, instead of evaluating the sweep now")
	of := fs.String("unit", "", "what every workflow decides for this unit now, a workflow that gives it no item included")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 || (*of != "" && (*wf != "" || *provider != "" || *stored)) {
		return errors.New(planUsage)
	}
	c, err := e.daemonClient(ctx)
	if err != nil {
		return err
	}
	if *of != "" {
		u, err := planUnitOf(ctx, c, *of)
		if err != nil {
			return err
		}
		return printUnitPlan(ctx, e, c, u)
	}
	q := api.PlanQuery{Workflow: *wf, Provider: *provider}
	if !*stored {
		q.Trigger = string(workflow.TriggerSweep)
	}
	return printPlan(ctx, e, c, q, *wf == "" && *provider == "")
}

// printPlan prints the plan q asks for, grouped by decision, then the
// sessions closed in the last day when withClosed, and its errors on
// stderr.
func printPlan(ctx context.Context, e *Env, c *client.Client, q api.PlanQuery, withClosed bool) error {
	q.Closed = withClosed && q.Trigger == ""
	res, err := c.Plan(ctx, q)
	if err != nil {
		return err
	}
	if withClosed && q.Trigger != "" {
		// The daemon serves the closed sessions with the stored plan only.
		if stored, err := c.Plan(ctx, api.PlanQuery{Closed: true}); err != nil {
			res.Errors = append(res.Errors, "closed sessions: "+err.Error())
		} else {
			res.Closed = stored.Closed
			res.Errors = append(res.Errors, stored.Errors...)
		}
	}
	writePlan(e.Out, res.Items, e.Now())
	writeClosed(e.Out, res.Closed, e.Now())
	for _, msg := range res.Errors {
		fmt.Fprintln(e.Err, "error: "+msg)
	}
	if n := len(res.Errors); n > 0 {
		return fmt.Errorf("the plan has %d error(s)", n)
	}
	return nil
}

// planUnitArg is the unit arg names: a pull request's URL or owner/repo#n,
// or provider:key, whose provider the daemon checks.
func planUnitArg(arg string) (unit.Unit, error) {
	if strings.Contains(arg, "://") {
		ref, err := pr.ParseURL(arg)
		return ref.Unit(), err
	}
	if p, key, ok := strings.Cut(arg, ":"); ok && unit.ValidProvider(p) && key != "" {
		return unit.Unit{Provider: unit.ProviderID(p), Key: key}, nil
	}
	if ref, err := pr.ParseKey(arg); err == nil {
		return ref.Unit(), nil
	}
	return unit.Unit{}, fmt.Errorf("%s names no unit: give a pull request URL, owner/repo#n, provider:key such as session:<session-id>, or an item's id as plan prints it", arg)
}

// itemID is the shape of an item's id as plan prints it, the start of its
// key; a whole id has its workflow, provider, key and value apart by "/".
var itemID = regexp.MustCompile(`^[0-9a-f]+$`)

// planUnitOf is the unit plan --unit's arg names: the unit planUnitArg
// reads, or the unit of the item an id names, in the daemon's plan or, when
// it holds none, in the sweep's.
func planUnitOf(ctx context.Context, c *client.Client, arg string) (unit.Unit, error) {
	u, err := planUnitArg(arg)
	if err == nil || (!itemID.MatchString(arg) && strings.Count(arg, "/") < 3) {
		return u, err
	}
	var items []api.PlanItem
	for _, q := range []api.PlanQuery{{}, {Trigger: string(workflow.TriggerSweep)}} {
		res, err := c.Plan(ctx, q)
		if err != nil {
			return unit.Unit{}, err
		}
		if items = res.Items; slices.ContainsFunc(items, func(it api.PlanItem) bool { return namesItem(it, arg) }) {
			break
		}
	}
	id, err := resolvePlanID(items, arg)
	if err != nil {
		return unit.Unit{}, err
	}
	return items[slices.IndexFunc(items, func(it api.PlanItem) bool { return it.ID == id })].Unit, nil
}

// namesItem says whether arg is it's id or the start of its key.
func namesItem(it api.PlanItem, arg string) bool {
	return it.ID == arg || strings.HasPrefix(it.ID[strings.LastIndex(it.ID, "/")+1:], arg)
}

// printUnitPlan prints u's items as plan does, then each workflow of u's
// provider that gives it no item, with why and the facts its when read.
func printUnitPlan(ctx context.Context, e *Env, c *client.Client, u unit.Unit) error {
	res, err := c.Plan(ctx, api.PlanQuery{Unit: u.String()})
	if err != nil {
		return err
	}
	if res.Unit == "" {
		return errors.New("the daemon does not explain a unit; " + restartHint())
	}
	fmt.Fprintln(e.Out, unitHeader(res))
	writePlan(e.Out, res.Items, e.Now())
	if len(res.Workflows) > 0 {
		fmt.Fprintf(e.Out, "no item (%d)\n", len(res.Workflows))
	}
	for _, w := range res.Workflows {
		line := w.Trigger + ", " + w.Outcome
		if w.Reason != "" {
			line += ": " + oneLine(w.Reason)
		}
		fmt.Fprintf(e.Out, "  %s  %s\n", w.Workflow, line)
		if len(w.Facts) > 0 {
			facts := make([]string, 0, len(w.Facts))
			for _, f := range w.Facts {
				facts = append(facts, f.Name+" = "+oneLine(f.Value))
			}
			fmt.Fprintf(e.Out, "  %s  %s\n", strings.Repeat(" ", shortID), strings.Join(facts, " · "))
		}
	}
	for _, msg := range res.Errors {
		fmt.Fprintln(e.Err, "error: "+msg)
	}
	if n := len(res.Errors); n > 0 {
		return fmt.Errorf("the plan of %s has %d error(s)", res.Unit, n)
	}
	return nil
}

// unitHeader names the unit res explains as its row does, with its
// provider:key when the label does not say it.
func unitHeader(res api.PlanResponse) string {
	label := oneLine(res.Label)
	if label == "" {
		return res.Unit
	}
	head := label
	if title := oneLine(res.Title); title != "" {
		head += "  " + title
	}
	p, key, _ := strings.Cut(res.Unit, ":")
	if label != (unit.Unit{Provider: unit.ProviderID(p), Key: key}).Label() {
		head += "  (" + res.Unit + ")"
	}
	return head
}

// writePlan prints items under a header per decision: a line per item
// with its id, workflow, unit and reason, then a line of its details.
func writePlan(w io.Writer, items []api.PlanItem, now time.Time) {
	if len(items) == 0 {
		fmt.Fprintln(w, "nothing planned")
		return
	}
	for _, d := range planOrder {
		var group []api.PlanItem
		for _, it := range items {
			if it.Decision == string(d) {
				group = append(group, it)
			}
		}
		if len(group) == 0 {
			continue
		}
		fmt.Fprintf(w, "%s (%d)\n", d, len(group))
		for _, it := range group {
			head := fmt.Sprintf("  %s  %s  %s", planShortID(it.ID), it.Workflow, oneLine(it.UnitLabel()))
			if it.Reason != "" {
				head += ": " + oneLine(it.Reason)
			}
			fmt.Fprintln(w, head)
			if more := planDetails(it, now); more != "" {
				fmt.Fprintf(w, "  %s  %s\n", strings.Repeat(" ", shortID), more)
			}
		}
	}
}

// writeClosed lists the closed sessions with the line that resumes each.
func writeClosed(w io.Writer, closed []api.ClosedSession, now time.Time) {
	if len(closed) == 0 {
		return
	}
	fmt.Fprintf(w, "closed in the last day (%d)\n", len(closed))
	for _, c := range closed {
		fmt.Fprintf(w, "  %s  %s ago: %s\n", c.Session, span(now.Sub(c.At)), oneLine(c.Reason))
		if c.Resume != "" {
			fmt.Fprintf(w, "  %s  resume: %s\n", strings.Repeat(" ", len(c.Session)), oneLine(c.Resume))
		}
	}
}

// planDetails is the title of the item's unit, its window, mode, steps,
// gain and last error; it is indented, so it never reads as a header line.
func planDetails(it api.PlanItem, now time.Time) string {
	var parts []string
	if it.Title != "" {
		parts = append(parts, oneLine(it.Title))
	}
	if when := planWhen(it, now); when != "" {
		parts = append(parts, when)
	}
	if it.Observe {
		parts = append(parts, "observe")
	}
	if len(it.Steps) > 0 {
		parts = append(parts, "steps "+strings.Join(it.Steps, ", "))
	}
	if g := planGain(it.Gain); g != "" {
		parts = append(parts, "frees "+g)
	}
	if it.Error != "" {
		parts = append(parts, fmt.Sprintf("attempt %d failed: %s", it.Attempts, oneLine(it.Error)))
	}
	return strings.Join(parts, " · ")
}

// planWhen is when a pending item runs: at its window's end, or when the
// user applies it.
func planWhen(it api.PlanItem, now time.Time) string {
	if it.Decision != string(workflow.DecisionPending) {
		return ""
	}
	switch {
	case it.Default != string(workflow.OutcomeRun):
		return "waits for you"
	case it.Deadline.IsZero() || !it.Deadline.After(now):
		return "runs now"
	}
	return "runs in " + it.Deadline.Sub(now).Round(time.Second).String()
}

// planGain is the memory or disk an item frees, in the largest unit that
// keeps a whole number.
func planGain(g api.Gain) string {
	var parts []string
	for _, b := range []struct {
		n    int64
		what string
	}{{g.MemoryBytes, "memory"}, {g.DiskBytes, "disk"}} {
		if b.n > 0 {
			parts = append(parts, byteSize(b.n)+" of "+b.what)
		}
	}
	return strings.Join(parts, " and ")
}

func byteSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// planShortID is the start of the item's key, which plan apply and plan
// dismiss take for the whole id.
func planShortID(id string) string {
	v := id[strings.LastIndex(id, "/")+1:]
	if len(v) > shortID {
		v = v[:shortID]
	}
	return v
}

// runPlanAct applies or dismisses the stored plan's items, each named by
// its id, the start of its key, or its unit's pull request.
func runPlanAct(ctx context.Context, e *Env, action string, ids []string) error {
	c, err := e.daemonClient(ctx)
	if err != nil {
		return err
	}
	stored, err := c.Plan(ctx, api.PlanQuery{})
	if err != nil {
		return err
	}
	full := make([]string, 0, len(ids))
	for _, id := range ids {
		f, err := resolvePlanArg(stored.Items, id)
		if err != nil {
			return err
		}
		full = append(full, f)
	}
	res, err := c.ActOnPlan(ctx, api.PlanActRequest{IDs: full, Action: action})
	if err != nil {
		return err
	}
	for _, it := range res.Items {
		fmt.Fprintf(e.Out, "%s  %s  %s: %s %s\n", planShortID(it.ID), it.Workflow, oneLine(it.UnitLabel()), it.Decision, oneLine(it.Reason))
	}
	for _, msg := range res.Errors {
		fmt.Fprintln(e.Err, "error: "+msg)
	}
	if n := len(res.Errors); n > 0 {
		return fmt.Errorf("%d of %d item(s) not %s", n, len(full), map[string]string{"apply": "applied", "dismiss": "dismissed"}[action])
	}
	return nil
}

// resolvePlanArg is the stored item arg names: a pull request's one
// pending item when arg names a pull request, else the item resolvePlanID
// resolves.
func resolvePlanArg(items []api.PlanItem, arg string) (string, error) {
	if !strings.ContainsAny(arg, ":/#") || slices.ContainsFunc(items, func(it api.PlanItem) bool { return it.ID == arg }) {
		return resolvePlanID(items, arg)
	}
	u, err := planUnitArg(arg)
	if err != nil {
		return "", err
	}
	var pending []api.PlanItem
	for _, it := range items {
		if it.Unit.Provider == u.Provider && strings.EqualFold(it.Unit.Key, u.Key) && it.Decision == string(workflow.DecisionPending) {
			pending = append(pending, it)
		}
	}
	switch len(pending) {
	case 0:
		return "", fmt.Errorf("%s has no pending item; '%s plan --unit %s' says why", u.Label(), brand.Command, arg)
	case 1:
		return pending[0].ID, nil
	}
	found := make([]string, 0, len(pending))
	for _, it := range pending {
		found = append(found, planShortID(it.ID)+" ("+it.Workflow+")")
	}
	slices.Sort(found)
	return "", fmt.Errorf("%s has %d pending items: %s; give the id of one", u.Label(), len(found), strings.Join(found, ", "))
}

// resolvePlanID is the stored item id names: the whole id, or the start of
// one key alone, at least as long as the id plan prints, so an item the
// daemon does not hold never resolves to another one.
func resolvePlanID(items []api.PlanItem, id string) (string, error) {
	if slices.ContainsFunc(items, func(it api.PlanItem) bool { return it.ID == id }) {
		return id, nil
	}
	if len(id) < shortID {
		return "", fmt.Errorf("%s is shorter than the %d characters plan prints; give the id as plan prints it", id, shortID)
	}
	var found []string
	for _, it := range items {
		if strings.HasPrefix(it.ID[strings.LastIndex(it.ID, "/")+1:], id) {
			found = append(found, it.ID)
		}
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("no item %s in the daemon's plan; '%s plan --stored' lists the items it holds, and a new item is held once a sweep plans it", id, brand.Command)
	case 1:
		return found[0], nil
	}
	slices.Sort(found)
	return "", fmt.Errorf("%s names %d items: %s; give the whole id", id, len(found), strings.Join(found, ", "))
}
