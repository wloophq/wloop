package gtd

import (
	"cmp"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
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
)

// DerivedID is the id of the entry of kind derived from u's items for group, so a re-derivation finds it again.
func DerivedID(kind Kind, u unit.Unit, group string) ID {
	sum := sha256.Sum256([]byte(string(kind) + "|" + string(u.Provider) + "|" + u.Key + "|" + group))
	return ID("e-" + hex.EncodeToString(sum[:])[:16])
}

// NewID is a random entry id.
func NewID() ID {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return ID("e-" + hex.EncodeToString(b[:]))
}

// Open says whether the entry is still to do: not done, trash or reference.
func (e Entry) Open() bool {
	return e.State != Done && e.State != Trash && e.State != Reference
}

// CapturedByUser says whether the user captured the entry, rather than the
// loop deriving it from a third party's message or draft.
func (e Entry) CapturedByUser() bool { return e.Origin == OriginUser && e.Kind == KindCapture }

// Live says whether the entry is neither done nor trash.
func (e Entry) Live() bool { return e.State != Done && e.State != Trash }

// Same says whether e and o hold the same values, Updated aside.
func (e Entry) Same(o Entry) bool {
	a, b := e.canonical(), o.canonical()
	a.Updated, b.Updated = time.Time{}, time.Time{}
	return reflect.DeepEqual(a, b)
}

func (e Entry) canonical() Entry {
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
	c.Units = slices.Clone(e.Units)
	slices.SortFunc(c.Units, func(a, b unit.Unit) int { return cmp.Compare(a.String(), b.String()) })
	if len(c.Units) == 0 {
		c.Units = nil
	}
	if len(c.Contexts) == 0 {
		c.Contexts = nil
	}
	c.Focus.At = t(e.Focus.At)
	c.Snooze.Until = t(e.Snooze.Until)
	c.Deadline, c.SeenAt, c.ReviewedAt, c.Created, c.Updated = t(e.Deadline), t(e.SeenAt), t(e.ReviewedAt), t(e.Created), t(e.Updated)
	if e.Waiting != nil {
		w := *e.Waiting
		w.On = slices.Clone(w.On)
		if len(w.On) == 0 {
			w.On = nil
		}
		for i := range w.On {
			if len(w.On[i].Aliases) == 0 {
				w.On[i].Aliases = nil
			}
		}
		w.Since, w.LastNudge, w.Resurface = t(w.Since), t(w.LastNudge), t(w.Resurface)
		c.Waiting = &w
	}
	return c
}

func sortRefs(rs []ItemRef) {
	slices.SortFunc(rs, func(a, b ItemRef) int {
		return cmp.Or(cmp.Compare(a.Unit.String(), b.Unit.String()), strings.Compare(a.Item, b.Item))
	})
}

// isMe says whether p is the user.
func isMe(p items.Person, me Me) bool { return p.Is(me) }
