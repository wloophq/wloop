package archtest

import (
	"fmt"
	"io/fs"
	"maps"
	"path"
	"slices"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/gate"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/intent/expr"
	"github.com/wloophq/wloop/internal/turn"
	"github.com/wloophq/wloop/plugins"
	"github.com/wloophq/wloop/profiles"
)

// ruleFiles are the profile files that hold a [gates.approval] block.
var ruleFiles = []string{"intent.toml", "workflow.toml", "gates.toml"}

// denyListRules flags each [gates.approval] rule of fsys's profile files
// that sends a draft (mode send, run or delay) and reads a draft payload
// fact other than as an allow-list: a fact == "<value>" or fact in
// ["<value>", ...] term of the when's top-level conjunction, with no empty
// value.
func denyListRules(fsys fs.FS, layer string) ([]string, error) {
	env := builtin.WorkflowEnvAll()
	maps.Copy(env, builtin.ApprovalEnv(true, builtin.UnitSlots()...))
	var out []string
	err := fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !slices.Contains(ruleFiles, path.Base(name)) {
			return err
		}
		raw, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		var m struct {
			Gates struct {
				Approval struct {
					Rule []struct{ When, Mode string }
				}
			}
		}
		if err := toml.Unmarshal(raw, &m); err != nil {
			return fmt.Errorf("%s/%s: %w", layer, name, err)
		}
		for i, r := range m.Gates.Approval.Rule {
			if !draft.ModeOf(gate.Mode(r.Mode)).Releases() {
				continue
			}
			p, err := expr.CompileWorkflow(r.When, env)
			if err != nil {
				continue
			}
			for _, f := range p.Reads() {
				if !builtin.PayloadFact(f) {
					continue
				}
				if values, ok := expr.Allowed(p, f); !ok || slices.Contains(values, "") {
					out = append(out, fmt.Sprintf("%s/%s gates.approval.rule[%d] sends a draft and reads %s other than as an allow-list (R-allow-list): %s", layer, name, i, f, r.When))
				}
			}
		}
		return nil
	})
	return out, err
}

// shippedSend is a draft a shipped intent's rules decide: its kind, the
// payload it holds and whether the rules send it with no person approving.
type shippedSend struct {
	kind, payload string
	sends         bool
}

// shippedSends are the drafts the review and address intents send on their
// own: a review that comments or requests changes, a thread reply and a
// resolve; an approval, and a review whose event is absent, wait.
var shippedSends = []shippedSend{
	{"review", `{"event": "COMMENT"}`, true},
	{"review", `{"event": "REQUEST_CHANGES"}`, true},
	{"review", `{"event": "APPROVE"}`, false},
	{"review", `{}`, false},
	{"review", ``, false},
	{"thread_reply", `{}`, true},
	{"resolve", `{}`, true},
}

// shippedDecisions flags each draft of sends whose decision under intent
// def's draft approval is not what sends says.
func shippedDecisions(def dintent.Def, sends []shippedSend) []string {
	if def.DraftApproval == nil {
		return []string{fmt.Sprintf("intent %s has no draft approval", def.Name)}
	}
	var out []string
	for _, c := range sends {
		spec, ok := builtin.DraftKinds.Lookup(string(def.Provider), c.kind)
		if !ok {
			out = append(out, fmt.Sprintf("%s declares no draft kind %s", def.Provider, c.kind))
			continue
		}
		d := draft.Draft{Unit: unit.Unit{Provider: def.Provider, Key: "acme/api#7"}, Kind: draft.Kind(c.kind), Delivery: draft.Delivery(spec.Delivery), Payload: []byte(c.payload)}
		dec, err := def.DraftApproval.Decide(d, draft.Subject{Kind: spec}, builtin.DeliveryPosts(spec.Delivery), time.Time{})
		if got := err == nil && dec.Approval.Releases(); got != c.sends {
			out = append(out, fmt.Sprintf("intent %s sends a %s draft with payload %q: %v, want %v (%+v, %v) (R-allow-list)", def.Name, c.kind, c.payload, got, c.sends, dec, err))
		}
	}
	return out
}

// TestAllowListRuleShipped checks that every shipped approval rule reads a draft payload fact only as an
// allow-list, so an absent fact never releases a draft, and the review and address intents send a review
// only for an event they list.
func TestAllowListRuleShipped(t *testing.T) {
	t.Parallel()
	layers := map[string]fs.FS{"profiles": profiles.Default, "plugins": plugins.Profiles}
	for _, name := range slices.Sorted(maps.Keys(layers)) {
		bad, err := denyListRules(layers[name], name)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range bad {
			t.Error(b)
		}
	}
	l, err := turn.Bundled()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []dintent.Name{"review", "address"} {
		def, ok := l.Registry.Get(name)
		if !ok {
			t.Errorf("no bundled intent %s", name)
			continue
		}
		for _, b := range shippedDecisions(def, shippedSends) {
			t.Error(b)
		}
	}
}
