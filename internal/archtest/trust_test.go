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
	"time"

	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/unit"
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

// TestApprovalRulesReadTrustedFacts checks that no [gates.approval] rule
// that releases a gate (a draft gate's rule of any mode, any other gate's
// of mode send, run or delay) loads when it reads an untrusted fact, in
// each object that holds one: an intent's and a workflow's manifest, for
// its plan gates and its draft gates, and the config's
// workflows.<name>.gates.approval. The untrusted facts are the table's,
// the brief's extra fields, a classification's output and a task property
// an agent or a plugin writes, so an agent or a thread's text never
// releases a send or a run. A draft kind's payload facts are trusted, as
// its plugin posts the payload verbatim, and a rule that releases reads one
// only as an allow-list, so a draft that lacks it is never released.
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
	for _, b := range payloadFactRules(t) {
		t.Error(b)
	}
}

// payloadFactRules flags a payload fact a releasing allow-list rule of an
// intent cannot read, or that the rule reads to release a draft that lacks
// it or holds a value it does not list, and a releasing deny-list rule
// that loads.
func payloadFactRules(t *testing.T) []string {
	t.Helper()
	var out []string
	host, base := anyIntent(t)
	b, err := turn.Base()
	if err != nil {
		t.Fatal(err)
	}
	load := func(rules string) (*intentload.Loaded, []string) {
		root := t.TempDir()
		write(t, filepath.Join(root, "intents", host, "intent.toml"), base+fmt.Sprintf("\n[gates.approval]\nmode = %q\n%s", gate.ModeWait, rules))
		l, problems := intentload.Load([]profile.Layer{{Name: "user", Source: root, Dir: root}}, turn.BundledFS(), b, turn.CheckTemplate)
		var msgs []string
		for _, p := range problems {
			msgs = append(msgs, p.Field+": "+p.Msg)
		}
		return l, msgs
	}
	n := 0
	for _, p := range slices.Sorted(maps.Keys(builtin.PluginDraftKinds)) {
		for _, k := range builtin.PluginDraftKinds[p].Kinds {
			spec := draft.KindSpec{Provider: string(p), Name: k.Name, Delivery: k.Delivery}
			for _, f := range k.Facts {
				spec.Facts = append(spec.Facts, draft.FactSpec{Name: f.Name, Pointer: f.Pointer})
			}
			for _, f := range k.Facts {
				n++
				on := fmt.Sprintf("draft.delivery == %q", k.Delivery)
				allow := fmt.Sprintf("[[gates.approval.rule]]\nwhen = '%s && %s in [\"listed\"]'\nmode = %q\n", on, f.Name, gate.ModeSend)
				l, problems := load(allow)
				if len(problems) > 0 || l == nil {
					out = append(out, fmt.Sprintf("intent %s does not load an allow-list rule that sends and reads %s, a trusted payload fact: %v (R-trust)", host, f.Name, problems))
					continue
				}
				def, _ := l.Registry.Get(dintent.Name(host))
				if def.DraftApproval == nil {
					out = append(out, fmt.Sprintf("intent %s has no draft approval", host))
					continue
				}
				d := draft.Draft{Unit: unit.Unit{Provider: p, Key: "acme/api#7"}, Kind: draft.Kind(k.Name), Delivery: draft.Delivery(k.Delivery)}
				for payload, sends := range map[string]bool{
					payloadAt(f.Pointer, "listed"): true, payloadAt(f.Pointer, "other"): false, `{}`: false, ``: false,
				} {
					d.Payload = []byte(payload)
					dec, err := def.DraftApproval.Decide(d, draft.Subject{Kind: spec}, true, time.Time{})
					if got := dec.Approval.Releases() && err == nil; got != sends {
						out = append(out, fmt.Sprintf("%s in [\"listed\"] over payload %q sends %v, want %v: %+v, %v (R-trust)", f.Name, payload, got, sends, dec, err))
					}
				}
				deny := fmt.Sprintf("[[gates.approval.rule]]\nwhen = '%s && %s != \"other\"'\nmode = %q\n", on, f.Name, gate.ModeSend)
				if _, problems := load(deny); !slices.ContainsFunc(problems, func(m string) bool { return strings.Contains(m, f.Name) && strings.Contains(m, "allow-list") }) {
					out = append(out, fmt.Sprintf("intent %s loads a deny-list rule that sends and reads %s, which a draft that lacks it satisfies: %v (R-trust)", host, f.Name, problems))
				}
			}
		}
	}
	if n == 0 {
		t.Fatal("no bundled draft kind declares a payload fact")
	}
	return out
}

// payloadAt is a JSON object that holds value at the one-token pointer ptr.
func payloadAt(ptr, value string) string {
	return fmt.Sprintf(`{%q: %q}`, strings.TrimPrefix(ptr, "/"), value)
}

// untrustedFacts are the untrusted facts of a workflow of slot, each with
// a predicate that reads it, a brief's extra field, a classification's
// output and, on a task, the bundled risk property, which an agent writes.
func untrustedFacts(slot string) map[string]string {
	out := map[string]string{
		builtin.ExtraPrefix + "planted":    builtin.ExtraPrefix + "planted == true",
		builtin.ClassifyPrefix + "planted": builtin.ClassifyPrefix + "planted == true",
	}
	if slot == builtin.TaskSlot {
		out[builtin.PropPrefix+"risk"] = builtin.PropPrefix + `risk == "high"`
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
		var rules strings.Builder
		for i, f := range facts {
			for j, k := range []gate.Kind{gate.KindPlan, gate.KindDraft} {
				fmt.Fprintf(&rules, "[[gates.approval.rule]]\nwhen = 'gate.kind == \"%s\" && %s'\nmode = %q\n", k, untrustedFacts(slot)[f], gate.ModeSend)
				file := filepath.Join(root, "workflows", name, "workflow.toml")
				want = append(want, planted{file, fmt.Sprintf("gates.approval.rule[%d].when", 2*i+j), "workflow " + name + " [gates.approval] of a " + string(k) + " gate", f})
			}
		}
		manifest := fmt.Sprintf("summary = \"Plant\"\nprovider = %q\nwhen = 'true'\n\n[[step]]\nuse = %q\n\n[gates.approval]\nmode = %q\n%s",
			p, builtin.StepNotifySelf, gate.ModeWait, rules.String())
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
		fmt.Fprintf(&rules, "[[gates.approval.rule]]\nwhen = '%s'\nmode = %q\n", facts[f], gate.ModeSend)
		want = append(want, planted{file, fmt.Sprintf("gates.approval.rule[%d].when", i), "intent " + host + " [gates.approval]", f})
	}
	write(t, file, base+fmt.Sprintf("\n[gates.approval]\nmode = %q\n%s", gate.ModeWait, rules.String()))

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
			"config workflows.<name>.gates.approval, mode run":  "workflows.w.gates.approval.rule = " + rule(string(gate.ModeRun)),
			"config workflows.<name>.gates.approval, mode send": "workflows.w.gates.approval.rule = " + rule(string(gate.ModeSend)),
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
