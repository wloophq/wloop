package task

import (
	"maps"
	"reflect"
	"time"

	"github.com/wloophq/wloop/internal/domain/items"
)

// Value is a property's value: the field its PropType names is set, the
// others are zero. Text holds string and enum, Int int, Time date, Dur
// duration, Actors actor and actor[] (one person for actor), List string[].
type Value struct {
	Text   string         `json:"text,omitempty"`
	Int    int64          `json:"int,omitempty"`
	Time   time.Time      `json:"time,omitzero"`
	Dur    time.Duration  `json:"dur,omitempty"`
	Actors []items.Person `json:"actors,omitempty"`
	List   []string       `json:"list,omitempty"`
}

// IsZero says whether v holds no value, which unsets a property.
func (v Value) IsZero() bool {
	return v.Text == "" && v.Int == 0 && v.Time.IsZero() && v.Dur == 0 && len(v.Actors) == 0 && len(v.List) == 0
}

// Props are a task's properties by name; a property never set is absent.
type Props map[string]Value

// Get is the value of the property name, false when it was never set.
func (p Props) Get(name string) (Value, bool) {
	v, ok := p[name]
	return v, ok
}

// The properties the loop's own rules read, as the bundled tasks.toml declares them.
const (
	PropFocus    = "focus"
	PropArea     = "area"
	PropContexts = "contexts"
	PropDue      = "due"
	PropKind     = "kind"
	// PropOwner is who owns the task; the waiting verb's on names it.
	PropOwner = "owner"
	// PropCheckOn is when to check on a task someone else owes; the waiting verb's resurface sets it.
	PropCheckOn = "check_on"
	// PropLastNudge is when a nudge last went to whoever owes the task; the task.nudged effect writes it.
	PropLastNudge = "last_nudge"
)

// FocusOn stars a task in Focus; FocusOff is a star the user cleared.
const (
	FocusOn  = "on"
	FocusOff = "off"
)

// Prop is the task's property name; a zero Value when it is unset.
func (e Task) Prop(name string) Value { return e.Props[name] }

// SetProp sets the property name to v as by wrote it at at; a zero v unsets it. Setting the value it holds,
// by the writer that wrote it, changes nothing.
func (e *Task) SetProp(name string, v Value, by Writer, at time.Time) {
	old, had := e.Props[name]
	if v.IsZero() {
		if had {
			e.Props, e.Written = maps.Clone(e.Props), maps.Clone(e.Written)
			delete(e.Props, name)
			delete(e.Written, name)
		}
		return
	}
	if had && reflect.DeepEqual(old, v) && e.Written[name].By == by {
		return
	}
	e.Props, e.Written = maps.Clone(e.Props), maps.Clone(e.Written)
	if e.Props == nil {
		e.Props = Props{}
	}
	if e.Written == nil {
		e.Written = map[string]Written{}
	}
	e.Props[name], e.Written[name] = v, Written{By: by, At: at}
}

// Starred says whether the task is starred in Focus.
func (e Task) Starred() bool { return e.Prop(PropFocus).Text == FocusOn }

// Due is when the task is due; zero when it has no date.
func (e Task) Due() time.Time { return e.Prop(PropDue).Time }
