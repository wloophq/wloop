package task

import (
	"cmp"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

const (
	OriginDerived = "derived"
	OriginUser    = "user"
	OriginMessage = "message"
	OriginDraft   = "draft"
	OriginGate    = "gate"
)

// DerivedID is the id of the task item of u calls for, so a re-derivation finds it again. It hashes as the
// action entries of schema version 6 did, so the tasks they became keep matching their items.
func DerivedID(u unit.Unit, item string) ID {
	sum := sha256.Sum256([]byte("action|" + string(u.Provider) + "|" + u.Key + "|" + item))
	return ID("t-" + hex.EncodeToString(sum[:])[:16])
}

// NewID is a random entry id.
func NewID() ID {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return ID("t-" + hex.EncodeToString(b[:]))
}

// Open says whether the task is still to do: its category is neither done nor dropped.
func (e Task) Open() bool {
	return e.Category != CategoryDone && e.Category != CategoryDropped
}

// States are the profile's states as the rules read them: the category of a state, and the first state of a
// category, which a task moved into that category takes.
type States interface {
	CategoryOf(state string) (Category, bool)
	FirstOf(c Category) string
}

// ErrNoStateOf is a move into a category the profile declares no state of.
var ErrNoStateOf = errors.New("the profile declares no state of the category")

// MoveTo moves the task into category c, in the first state st declares of it; ErrNoStateOf, naming c, when
// st declares none, and the task is left as it was.
func (e *Task) MoveTo(c Category, st States) error {
	s := st.FirstOf(c)
	if s == "" {
		return fmt.Errorf("%w %s", ErrNoStateOf, c)
	}
	e.State, e.Category = State(s), c
	return nil
}

// Become moves the task to state s, in the category st declares of it; an undeclared state keeps the
// task's category.
func (e *Task) Become(s State, st States) {
	e.State = s
	if c, ok := st.CategoryOf(string(s)); ok {
		e.Category = c
	}
}

// Snoozed says whether the task is deferred under a snooze: a date, activity or a mute wakes it.
func (e Task) Snoozed() bool {
	return e.Category == CategoryDeferred && (!e.Snooze.Until.IsZero() || e.Snooze.OnActivity || e.Snooze.Mute)
}

// FirstUnit is the unit the entry is about: its first unit, else its
// first item's.
func (e Task) FirstUnit() (unit.Unit, bool) {
	if len(e.Units) > 0 {
		return e.Units[0], true
	}
	if len(e.Items) > 0 {
		return e.Items[0].Unit, true
	}
	return unit.Unit{}, false
}

// CapturedByUser says whether the user made the task, rather than the loop
// deriving it from a source's items or opening it for a gate.
func (e Task) CapturedByUser() bool { return e.Origin == OriginUser }

// IsProject says whether the task is a project, as its kind property says.
func (e Task) IsProject() bool { return e.Prop(PropKind).Text == KindProject }

// Same says whether e and o hold the same values, Updated aside.
func (e Task) Same(o Task) bool {
	a, b := e.canonical(), o.canonical()
	a.Updated, b.Updated = time.Time{}, time.Time{}
	return reflect.DeepEqual(a, b)
}

func (e Task) canonical() Task {
	t := func(x time.Time) time.Time {
		if x.IsZero() {
			return time.Time{}
		}
		return x.UTC().Round(0)
	}
	c := e
	c.Items = slices.Clone(e.Items)
	sortRefs(c.Items)
	if len(c.Items) == 0 {
		c.Items = nil
	}
	c.Resolved = slices.Clone(e.Resolved)
	sortRefs(c.Resolved)
	if len(c.Resolved) == 0 {
		c.Resolved = nil
	}
	c.Units = slices.Clone(e.Units)
	slices.SortFunc(c.Units, func(a, b unit.Unit) int { return cmp.Compare(a.String(), b.String()) })
	if len(c.Units) == 0 {
		c.Units = nil
	}
	c.Props, c.Written = nil, nil
	for name, v := range e.Props {
		if v.IsZero() {
			continue
		}
		if c.Props == nil {
			c.Props, c.Written = Props{}, map[string]Written{}
		}
		v.Time = t(v.Time)
		v.Actors, v.List = slices.Clone(v.Actors), slices.Clone(v.List)
		for i := range v.Actors {
			if len(v.Actors[i].Aliases) == 0 {
				v.Actors[i].Aliases = nil
			}
		}
		c.Props[name] = v
		w := e.Written[name]
		w.At = t(w.At)
		c.Written[name] = w
	}
	c.Snooze.Until = t(e.Snooze.Until)
	c.SeenAt, c.ReviewedAt, c.Created, c.Updated = t(e.SeenAt), t(e.ReviewedAt), t(e.Created), t(e.Updated)
	return c
}

func sortRefs(rs []ItemRef) {
	slices.SortFunc(rs, func(a, b ItemRef) int {
		return cmp.Or(cmp.Compare(a.Unit.String(), b.Unit.String()), strings.Compare(a.Item, b.Item))
	})
}

// isMe says whether p is the user.
func isMe(p items.Person, me Me) bool { return p.Is(me) }
