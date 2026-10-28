package source

import (
	"fmt"

	"github.com/wloophq/wloop/internal/domain/draft"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// Units converts between the protocol's units and the domain's for the
// plugin whose codecs it holds.
type Units struct {
	// Codecs read every plugin's keys, the core's included.
	Codecs *unit.Registry
}

// Of is the domain unit u names: a unit of a provider the codecs know, with
// a key its codec takes.
func (c Units) Of(u plugin.Unit) (unit.Unit, error) {
	return c.Codecs.Parse(u.Provider, u.Key)
}

// Own is the domain unit u names, which must be plugin id's own.
func (c Units) Own(id string, u plugin.Unit) (unit.Unit, error) {
	if u.Provider != id {
		return unit.Unit{}, fmt.Errorf("plugin %s reported a unit of %s", id, u.Provider)
	}
	return c.Of(u)
}

// Wire is u as the protocol names it.
func Wire(u unit.Unit) plugin.Unit { return plugin.Unit{Provider: string(u.Provider), Key: u.Key} }

// Wires are us as the protocol names them.
func Wires(us []unit.Unit) []plugin.Unit {
	out := make([]plugin.Unit, len(us))
	for i, u := range us {
		out[i] = Wire(u)
	}
	return out
}

// TaskID is the task id s names; an id that is not a task's is an error.
func TaskID(s string) (task.ID, error) {
	if s == "" {
		return "", nil
	}
	if err := (task.Codec{}).Valid(s); err != nil {
		return "", err
	}
	return task.ID(s), nil
}

// WireDraft is d as the protocol carries it.
func WireDraft(d draft.Draft) src.Draft {
	return src.Draft{ID: int64(d.ID), Unit: Wire(d.Unit), Items: d.Items, Task: d.Task, Kind: string(d.Kind), Purpose: d.Purpose,
		Delivery: string(d.Delivery), Effects: d.Effects, Target: d.Target, Text: d.Text, Reaction: d.Reaction, Replaced: d.Replaced,
		Origin: src.DraftOrigin{Kind: string(d.Origin.Kind), TurnSeq: d.Origin.TurnSeq, Intent: d.Origin.Intent, Workflow: d.Origin.Workflow,
			Rule: d.Origin.Rule, Key: d.Origin.Key, Unit: wireOrZero(d.Origin.Unit)},
		State: string(d.State), Evidence: src.Evidence(d.Evidence)}
}

func wireOrZero(u unit.Unit) plugin.Unit {
	if u.IsZero() {
		return plugin.Unit{}
	}
	return Wire(u)
}

// Draft is the domain draft a plugin's d proposes or reports. The unit and
// the task are checked; what only the host sets (approval, state, origin of
// a session's draft) is the host's to fill.
func (c Units) Draft(d src.Draft) (draft.Draft, error) {
	u, err := c.Of(d.Unit)
	if err != nil {
		return draft.Draft{}, err
	}
	if _, err := TaskID(d.Task); err != nil {
		return draft.Draft{}, err
	}
	out := draft.Draft{ID: draft.ID(d.ID), Unit: u, Items: d.Items, Task: d.Task, Kind: draft.Kind(d.Kind), Purpose: d.Purpose,
		Delivery: draft.Delivery(d.Delivery), Effects: d.Effects, Target: d.Target, Text: d.Text, Reaction: d.Reaction, Replaced: d.Replaced,
		Origin: draft.Origin{Kind: draft.OriginKind(d.Origin.Kind), TurnSeq: d.Origin.TurnSeq, Intent: d.Origin.Intent,
			Workflow: d.Origin.Workflow, Rule: d.Origin.Rule, Key: d.Origin.Key},
		Evidence: draft.Evidence(d.Evidence)}
	if d.Origin.Unit != (plugin.Unit{}) {
		if out.Origin.Unit, err = c.Of(d.Origin.Unit); err != nil {
			return draft.Draft{}, err
		}
	}
	return out, nil
}
