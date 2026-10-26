// Command factsdoc prints the generated tables of the facts reference page: the
// intents' facts, one table per workflow slot, the draft approval facts, the workflow steps, then the draft effects.
package main

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/intent/builtin"
)

func main() {
	fmt.Fprint(os.Stdout, strings.Join(tables(), "\n"))
}

func tables() []string {
	out := []string{intentTable()}
	for _, slot := range builtin.WorkflowSlots {
		out = append(out, workflowTable(slot))
	}
	return append(out, approvalTable(), stepTable(), effectTable())
}

func intentTable() string {
	var b strings.Builder
	b.WriteString("| Fact | Type | Slots | Meaning |\n|---|---|---|---|\n")
	for _, f := range builtin.Facts {
		if slices.Contains(f.Slots, builtin.WorkflowSlot) || slices.Contains(f.Slots, builtin.ApprovalSlot) {
			continue
		}
		slots := make([]string, len(f.Slots))
		for i, s := range f.Slots {
			slots[i] = string(s)
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", f.Name, f.Type, strings.Join(slots, ", "), cell(f.Doc))
	}
	return b.String()
}

func workflowTable(slot string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "| `%s` fact%s | Type | Meaning |\n|---|---|---|\n", slot, provider(slot))
	for _, f := range builtin.Facts {
		if f.Slot == slot {
			fmt.Fprintf(&b, "| `%s` | %s | %s%s |\n", f.Name, f.Type, cell(f.Doc), untrusted(f))
		}
	}
	return b.String()
}

// untrusted marks a fact no [draft_approval] rule may read.
func untrusted(f builtin.Fact) string {
	if !f.Untrusted {
		return ""
	}
	return " **Untrusted**: no `[draft_approval]` rule reads it."
}

func approvalTable() string {
	var b strings.Builder
	b.WriteString("| Approval fact | Type | Meaning |\n|---|---|---|\n")
	for _, f := range builtin.Facts {
		if slices.Contains(f.Slots, builtin.ApprovalSlot) {
			fmt.Fprintf(&b, "| `%s` | %s | %s |\n", f.Name, f.Type, cell(f.Doc))
		}
	}
	return b.String()
}

func stepTable() string {
	var b strings.Builder
	b.WriteString("| Step | Unit | Does |\n|---|---|---|\n")
	for _, d := range builtin.Steps {
		units := "any"
		if len(d.Providers) > 0 {
			names := make([]string, len(d.Providers))
			for i, p := range d.Providers {
				names[i] = string(p)
			}
			units = strings.Join(names, ", ")
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", d.Name, units, cell(d.Doc))
	}
	return b.String()
}

func effectTable() string {
	var b strings.Builder
	b.WriteString("| Effect | Unit | Does |\n|---|---|---|\n")
	for _, e := range builtin.Effects {
		units := "any"
		if e.Provider != "" {
			units = string(e.Provider)
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", e.Name, units, cell(e.Doc))
	}
	return b.String()
}

func provider(slot string) string {
	for p, s := range builtin.Slots {
		if s == slot {
			return fmt.Sprintf(" (subject `%s`)", p)
		}
	}
	return ""
}

func cell(s string) string { return strings.ReplaceAll(s, "|", `\|`) }
