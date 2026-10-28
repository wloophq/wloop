package task

import (
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
