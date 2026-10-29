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
	PropKind = "kind"
	// PropOwner is who owns the task; the waiting verb's on names it.
	PropOwner = "owner"
	// PropCheckOn is when to check on a task someone else owes; the waiting verb's resurface sets it.
	PropCheckOn = "check_on"
	// PropLastNudge is when a nudge last went to whoever owes the task; the task.nudged effect writes it.
	PropLastNudge = "last_nudge"
	// PropUnfollowed is when reconcile dropped a unit's own task because nobody followed the unit any more, so
	// following it again brings the task back; a person's drop never sets it.
	PropUnfollowed = "unfollowed"
)

// Role is a mechanism of the loop that reads or writes a task property: the profile's tasks.toml gives it to
// the property it acts on, so Go names the role and never the property.
type Role string

// The roles, a closed set.
const (
	// RoleArea is the property the area filter of the lists and their counters reads, which the area handler
	// sets and a new parent takes from the task it starts with: a string.
	RoleArea Role = "area"
	// RoleContexts is the property the contexts filter reads, which the context handler sets and a task takes
	// from the item or the request that opens it: a string[].
	RoleContexts Role = "contexts"
	// RoleStar is the property the star handler toggles between its first value, starred, and its second: an
	// enum.
	RoleStar Role = "star"
	// RoleDeadline is the property a task takes from its items' deadline, and a draft's task from its gate's
	// window: a date.
	RoleDeadline Role = "deadline"
)

// Roles are every role.
var Roles = []Role{RoleArea, RoleContexts, RoleStar, RoleDeadline}

// RoleTypes are the type of the property each role acts on.
var RoleTypes = map[Role]PropType{RoleArea: PropString, RoleContexts: PropStrings, RoleStar: PropEnum, RoleDeadline: PropDate}

// Profile is the profile as the rules read it: its states, and the property each role acts on with its
// values, "" for a role no property takes.
type Profile interface {
	States
	PropOf(r Role) (name string, values []string)
}

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

// Role is the value of the property role acts on in p; a zero Value when no property takes it or it is unset.
func (e Task) Role(p Profile, r Role) Value {
	name, _ := p.PropOf(r)
	if name == "" {
		return Value{}
	}
	return e.Prop(name)
}

// SetRole sets the property role acts on in p, as SetProp does; nothing when no property takes it.
func (e *Task) SetRole(p Profile, r Role, v Value, by Writer, at time.Time) {
	if name, _ := p.PropOf(r); name != "" {
		e.SetProp(name, v, by, at)
	}
}
