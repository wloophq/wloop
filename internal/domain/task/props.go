package task

import (
	"time"

	"github.com/wloophq/wloop/internal/domain/items"
)

// Value is a property's value: the field its PropType names is set, the
// others are zero. Text holds string and enum, Int int, Time date, Dur
// duration, Actors actor and actor[] (one person for actor), List string[].
type Value struct {
	Text   string
	Int    int64
	Time   time.Time
	Dur    time.Duration
	Actors []items.Person
	List   []string
}

// Props are a task's properties by name; a property never set is absent.
type Props map[string]Value

// Get is the value of the property name, false when it was never set.
func (p Props) Get(name string) (Value, bool) {
	v, ok := p[name]
	return v, ok
}
