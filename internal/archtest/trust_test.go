package archtest

import (
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/domain/workflow"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
	"github.com/wloophq/wloop/internal/profile"
	"github.com/wloophq/wloop/internal/turn"
)

// trustPlant is a releasing approval rule reading fact, planted in object,
// and why it did not load; a nil err means it loaded.
type trustPlant struct {
	object, fact string
	err          error
}

// ruleTrustedFacts flags each plant that loaded, or that was refused for a
// reason that does not name its fact.
func ruleTrustedFacts(plants []trustPlant) []string {
	var out []string
	for _, p := range plants {
		if p.err == nil || !strings.Contains(p.err.Error(), p.fact) {
			out = append(out, fmt.Sprintf("%s loads a releasing rule that reads %s, which a third party's text or an agent sets: %v (R-trust)", p.object, p.fact, p.err))
		}
	}
	return out
}

// TestApprovalRulesReadTrustedFacts checks that no approval rule that
// releases (a draft rule of any mode, a run rule of mode run or delay)
// loads when it reads an untrusted fact, in each object that holds one: an
// intent's and a workflow's [drafts.approval], a workflow's [approval],
// and the config's workflows.<name>.drafts.approval and
// workflows.<name>.approval. The untrusted facts are the table's, the
// brief's extra fields and a classification's output, so an agent or a
// thread's text never releases a send or a run.
func TestApprovalRulesReadTrustedFacts(t *testing.T) {
	t.Parallel()
	var plants []trustPlant
	plants = append(plants, plantManifests(t)...)
	plants = append(plants, plantConfig(t)...)
	if len(plants) == 0 {
		t.Fatal("nothing planted")
	}
	for _, b := range ruleTrustedFacts(plants) {
		t.Error(b)
	}
}

// untrustedFacts are the untrusted facts of a workflow of slot, each with
// a predicate that reads it, and a brief's extra field and a
// classification's output.
func untrustedFacts(slot string) map[string]string {
	out := map[string]string{
		builtin.ExtraPrefix + "planted":    builtin.ExtraPrefix + "planted == true",
		builtin.ClassifyPrefix + "planted": builtin.ClassifyPrefix + "planted == true",
	}
	for name, typ := range builtin.WorkflowEnv(slot) {
		if builtin.IsUntrusted(name) {
			out[name] = reading(name, typ)
		}
	}
	return out
}

// reading is a predicate that reads name, of type typ, with no literal of
// its own.
func reading(name string, typ expr.Type) string {
	switch typ {
	case expr.Bool:
		return name
	case expr.Int:
		return name + " > 0"
	case expr.Duration:
		return name + ` > duration("1m")`
	case expr.Time:
		return name + " > timestamp(0)"
	}
	return "size(" + name + ") > 0"
}

// plantManifests plants every untrusted fact in a releasing rule of each
// manifest object, one workflow per provider and the bundled intent that
// serves every provider, and loads the profile once.
func plantManifests(t *testing.T) []trustPlant {
	t.Helper()
	root := t.TempDir()
	type planted struct{ file, field, object, fact string }
	var want []planted
	for _, p := range slices.Sorted(maps.Keys(builtin.Slots)) {
		slot := builtin.Slots[p]
		facts := slices.Sorted(maps.Keys(untrustedFacts(slot)))
		name := "plant-" + string(p)
		var run, drafts strings.Builder
		for i, f := range facts {
			fmt.Fprintf(&run, "[[approval.rule]]\nwhen = '%s'\nmode = %q\n", untrustedFacts(slot)[f], workflow.ApprovalRun)
			fmt.Fprintf(&drafts, "[[drafts.approval.rule]]\nwhen = '%s'\nmode = %q\n", untrustedFacts(slot)[f], draft.Send)
			file := filepath.Join(root, "workflows", name, "workflow.toml")
			want = append(want,
				planted{file, fmt.Sprintf("approval.rule[%d].when", i), "workflow " + name + " [approval]", f},
				planted{file, fmt.Sprintf("drafts.approval.rule[%d].when", i), "workflow " + name + " [drafts.approval]", f})
		}
		manifest := fmt.Sprintf("summary = \"Plant\"\nprovider = %q\nwhen = 'true'\n\n[[step]]\nuse = %q\n\n[approval]\nmode = %q\n%s\n[drafts.approval]\nmode = %q\n%s",
			p, builtin.StepNotifySelf, workflow.ApprovalWait, run.String(), draft.Wait, drafts.String())
		write(t, filepath.Join(root, "workflows", name, "workflow.toml"), manifest)
	}

	host, base := anyIntent(t)
	var rules strings.Builder
	facts := map[string]string{}
	for _, slot := range builtin.Slots {
		maps.Copy(facts, untrustedFacts(slot))
	}
	file := filepath.Join(root, "intents", host, "intent.toml")
	for i, f := range slices.Sorted(maps.Keys(facts)) {
		fmt.Fprintf(&rules, "[[drafts.approval.rule]]\nwhen = '%s'\nmode = %q\n", facts[f], draft.Send)
		want = append(want, planted{file, fmt.Sprintf("drafts.approval.rule[%d].when", i), "intent " + host + " [drafts.approval]", f})
	}
	write(t, file, base+fmt.Sprintf("\n[drafts.approval]\nmode = %q\n%s", draft.Wait, rules.String()))

	b, err := turn.Base()
	if err != nil {
		t.Fatal(err)
	}
	_, problems := intentload.Load([]profile.Layer{{Name: "user", Source: root, Dir: root}}, turn.BundledFS(), b, turn.CheckTemplate)
	out := make([]trustPlant, 0, len(want))
	for _, w := range want {
		var err error
		for _, p := range problems {
			if p.File == w.file && p.Field == w.field {
				err = fmt.Errorf("%s: %s", p.Field, p.Msg)
				break
			}
		}
		out = append(out, trustPlant{object: w.object, fact: w.fact, err: err})
	}
	return out
}

// anyIntent is a bundled intent that serves every provider and its
// manifest.
func anyIntent(t *testing.T) (string, string) {
	t.Helper()
	l, err := turn.Bundled()
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range l.Registry.All() {
		if d.Builds != "" || !allProviders(d.Serves) {
			continue
		}
		raw, err := fs.ReadFile(turn.BundledFS(), filepath.ToSlash(filepath.Join("intents", string(d.Name), "intent.toml")))
		if err != nil {
			t.Fatal(err)
		}
		return string(d.Name), string(raw)
	}
	t.Fatal("no bundled intent serves every provider")
	return "", ""
}

func allProviders(serves func(unit.ProviderID) bool) bool {
	for p := range builtin.Slots {
		if !serves(p) {
			return false
		}
	}
	return true
}

// plantConfig plants each untrusted fact in a releasing rule of the
// config's two approval blocks of a workflow, one config file each.
func plantConfig(t *testing.T) []trustPlant {
	t.Helper()
	facts := map[string]string{}
	for _, slot := range builtin.Slots {
		maps.Copy(facts, untrustedFacts(slot))
	}
	base := config.Defaults()
	if len(base.WorkspaceManagers) == 0 {
		base.WorkspaceManagers = []string{"planted"}
	}
	dir := t.TempDir()
	var out []trustPlant
	for i, f := range slices.Sorted(maps.Keys(facts)) {
		rule := func(mode string) string { return fmt.Sprintf(`[{when = %q, mode = %q}]`, facts[f], mode) }
		for object, raw := range map[string]string{
			"config workflows.<name>.approval":        "workflows.w.approval.rule = " + rule(string(workflow.ApprovalRun)),
			"config workflows.<name>.drafts.approval": "workflows.w.drafts.approval.rule = " + rule(string(draft.Send)),
		} {
			file := filepath.Join(dir, fmt.Sprintf("%d-%d.toml", i, len(out)))
			write(t, file, raw)
			_, err := config.LoadFile(file, base)
			out = append(out, trustPlant{object: object, fact: f, err: err})
		}
	}
	return out
}

func write(t *testing.T, file, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}
