package cli

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/builtin"
)

const workflowsUsage = `usage:
  workflows facts [<provider>]
  workflows steps
manifest reference: the workflow manifest page of the documentation
editor schema: profile schema --workflow`

func runWorkflows(_ context.Context, e *Env, args []string) error {
	switch {
	case len(args) == 2 && args[0] == "facts":
		return printWorkflowFacts(e, args[1])
	case len(args) == 1 && args[0] == "facts":
		return printWorkflowFacts(e, "")
	case len(args) == 1 && args[0] == "steps":
		return printWorkflowSteps(e)
	}
	return usageError{usage: workflowsUsage}
}

func printWorkflowFacts(e *Env, subject string) error {
	slots := builtin.WorkflowSlots
	if subject != "" {
		slot, ok := builtin.Slots[unit.ProviderID(subject)]
		if !ok {
			subjects := slices.Sorted(maps.Keys(builtin.Slots))
			names := make([]string, len(subjects))
			for i, s := range subjects {
				names[i] = string(s)
			}
			return usageError{usage: workflowsUsage, why: fmt.Sprintf("unknown subject %s: one of %s", subject, strings.Join(names, ", "))}
		}
		slots = []string{slot}
		if slices.Contains(builtin.BriefSubjects, slot) {
			slots = append(slots, "brief")
		}
		slots = append(slots, builtin.DraftSlot, "trigger", "config")
	}
	w := tabwriter.NewWriter(e.Out, 0, 4, 2, ' ', 0)
	for i, slot := range slots {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintln(w, slotHeading(slot))
		for _, f := range builtin.Facts {
			if f.Slot == slot {
				fmt.Fprintf(w, "  %s\t%s\t%s\n", f.Name, f.Type, f.Doc)
			}
		}
	}
	return w.Flush()
}

func slotHeading(slot string) string {
	for p, s := range builtin.Slots {
		if s == slot {
			return fmt.Sprintf("%s (subject %s)", slot, p)
		}
	}
	switch slot {
	case "brief":
		return slot + " (subjects whose units have a brief)"
	case builtin.DraftSlot:
		return slot + " ([withdraw] when, of each draft)"
	}
	return slot + " (every workflow)"
}

func printWorkflowSteps(e *Env) error {
	w := tabwriter.NewWriter(e.Out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "STEP\tSUBJECT\tARGUMENTS\tDOES")
	for _, d := range builtin.Steps {
		subjects := "any"
		if len(d.Providers) > 0 {
			names := make([]string, len(d.Providers))
			for i, p := range d.Providers {
				names[i] = string(p)
			}
			subjects = strings.Join(names, ", ")
		}
		args := "-"
		if a := builtin.StepArgs(d.Name); len(a) > 0 {
			args = strings.Join(a, ", ")
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", d.Name, subjects, args, d.Doc)
	}
	return w.Flush()
}
