// Command factsdoc prints the facts reference page: the intents' facts and
// the workflow facts, each grouped by slot, the draft approval facts, the
// workflow steps, then the draft effects.
package main

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/intent/builtin"
)

func main() {
	p, err := page()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Fprint(os.Stdout, p)
}

// group is a subsection of the page: the facts whose name starts with one
// of its prefixes and a dot.
type group struct {
	title    string
	prefixes []string
}

// intentGroups are the intent facts' subsections, in reading order.
var intentGroups = []group{
	{"The pull request (`pr.*`)", []string{"pr"}},
	{"The row (`row.*`)", []string{"row"}},
	{"The turn (`turn.*`, `unit.*`)", []string{"turn", "unit"}},
	{"A session's name (`session.*`; slot `name`)", []string{"session"}},
	{"A draft's actions (`draft.*`)", []string{"draft"}},
	{"The configuration (`config.*`)", []string{"config"}},
}

// workflowGroups title the workflow facts' subsections, one per slot of
// builtin.WorkflowSlots.
var workflowGroups = map[string]string{
	"pr": "Pull requests", "session": "Sessions", "workspace": "Workspaces", "entry": "GTD entries", "slack": "Slack conversations",
	"brief": "Briefs", "draft": "Drafts", "trigger": "Triggers", "config": "The configuration",
}

func intentFacts() []builtin.Fact {
	var out []builtin.Fact
	for _, f := range builtin.Facts {
		if !slices.Contains(f.Slots, builtin.WorkflowSlot) && !slices.Contains(f.Slots, builtin.ApprovalSlot) {
			out = append(out, f)
		}
	}
	return out
}

// groupedFacts sorts facts into gs, each group keeping the facts' order; a
// fact no group takes, or a group no fact falls in, is an error.
func groupedFacts(facts []builtin.Fact, gs []group) ([][]builtin.Fact, error) {
	out := make([][]builtin.Fact, len(gs))
	for _, f := range facts {
		i := slices.IndexFunc(gs, func(g group) bool {
			return slices.ContainsFunc(g.prefixes, func(p string) bool { return strings.HasPrefix(f.Name, p+".") })
		})
		if i < 0 {
			return nil, fmt.Errorf("the fact %s is in no group of the facts page; add its prefix to intentGroups in factsdoc", f.Name)
		}
		out[i] = append(out[i], f)
	}
	for i, g := range gs {
		if len(out[i]) == 0 {
			return nil, fmt.Errorf("the group %q of the facts page holds no fact", g.title)
		}
	}
	return out, nil
}

func intentTable(facts []builtin.Fact) string {
	var b strings.Builder
	b.WriteString("| Fact | Type | Slots | Meaning |\n|---|---|---|---|\n")
	for _, f := range facts {
		slots := make([]string, len(f.Slots))
		for i, s := range f.Slots {
			slots[i] = string(s)
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", f.Name, f.Type, strings.Join(slots, ", "), cell(f.Doc))
	}
	return b.String()
}

// workflowSections are the workflow facts' tables under their headings; a
// fact of a slot without a title is an error.
func workflowSections(facts []builtin.Fact, titles map[string]string) ([]string, error) {
	for _, f := range facts {
		if slices.Contains(f.Slots, builtin.WorkflowSlot) && titles[f.Slot] == "" {
			return nil, fmt.Errorf("the workflow fact %s is of the slot %q, in no group of the facts page; add it to workflowGroups in factsdoc", f.Name, f.Slot)
		}
	}
	var out []string
	for _, slot := range builtin.WorkflowSlots {
		out = append(out, fmt.Sprintf("### %s (`%s.*`)", titles[slot], slot), "", workflowTable(slot))
	}
	return out, nil
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

// untrusted marks a fact no [drafts.approval] rule, nor a run rule that
// runs, may read.
func untrusted(f builtin.Fact) string {
	if !f.Untrusted {
		return ""
	}
	return " **Untrusted**: no `[drafts.approval]` rule, nor an `[approval]` rule that runs, reads it."
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
			return fmt.Sprintf(" (provider `%s`)", p)
		}
	}
	return ""
}

func cell(s string) string { return strings.ReplaceAll(s, "|", `\|`) }

func page() (string, error) {
	out := []string{
		"---",
		"title: Facts and steps",
		"description: The facts that intent and workflow predicates read, and the steps a workflow can run.",
		"---",
		"",
		"Predicates are CEL expressions; their syntax is on the [intent manifest](/reference/intent-manifest/#predicates) page. A missing fact reads as its type's zero value.",
		"",
		"## Intent facts",
		"",
		"The predicates of an intent manifest read these. The **action** slot (`[action]` predicates) reads the row the daemon serves; the **turn** slot (`[record] when`, headline cases, `also_profile[].when`) reads the turn being built.",
		"",
	}
	parts, err := groupedFacts(intentFacts(), intentGroups)
	if err != nil {
		return "", err
	}
	for i, g := range intentGroups {
		out = append(out, "### "+g.title, "", intentTable(parts[i]))
	}
	out = append(out, "## Workflow facts", "",
		"A workflow's `when` reads the facts of its provider's slot, plus `trigger.*`, `config.*` and `now`; a provider whose units have a brief also reads `brief.*`, and `[drafts.withdraw] when` also reads `draft.*`. `{{command}} workflows facts [entry|github|session|slack|workspace]` prints them. A pull request fact has one name and one spelling in every slot: `pr.repo` is `owner/repo` and `pr.repo_name` the bare name.",
		"")
	sections, err := workflowSections(builtin.Facts, workflowGroups)
	if err != nil {
		return "", err
	}
	out = append(out, sections...)
	out = append(out,
		"## Draft approval facts", "",
		"A `[drafts.approval]` rule's `when` reads these, the facts of the draft, plus the facts of the unit the draft is on (a workflow's: its provider's), never `brief.*`, `trigger.*`, `config.*` or a fact marked **Untrusted**, whose value a third party's text, read by the extraction, or the proposing turn itself sets. A rule over the facts of another provider than the draft's unit's does not apply to it. A rule that releases a draft on its own (`send` or `delay`) applies to a comment draft only when its `when` has `draft.delivery == \"comment\"` as a term of its top-level `&&`.",
		"", approvalTable(),
		"## Workflow steps", "",
		"The steps a `[[step]]` may name with `use`. `{{command}} workflows steps` prints them with their arguments.",
		"", stepTable(),
		"## Draft effects", "",
		"What a sent draft applies, once: the names a `draft.propose` step lists in `effects` and an intent lists in `[drafts] allowed_effects`. A turn's own `effects` are on the [language](/reference/language/) page.",
		"", effectTable(),
		"This page is generated from the binary's fact registry by `make docs-ref`.",
	)
	return strings.Join(out, "\n") + "\n", nil
}
