package readmodel

import (
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/gate"
	"github.com/wloophq/wloop/internal/domain/turn"
)

// Unmuter is optional on a Provider: in, a row input of the provider,
// derived as if the user had not muted its unit.
type Unmuter interface {
	Unmuted(in any) any
}

// Unmuted is the read's input as if the user had not muted its unit.
func (r Read) Unmuted() any {
	if u, ok := r.p.(Unmuter); ok {
		return u.Unmuted(r.In)
	}
	return r.In
}

// Notice is what a row notifies, as its provider derives it.
type Notice struct {
	// Gate is the kind of the gate the notice stands for, which keys it on
	// the unit's task; "" for one no gate stands for.
	Gate gate.Kind
	// Held says the notice is of a held round; Question of the open
	// question of the turn record Record.
	Held, Question bool
	Record         int64
	// Token lasts as long as what is notified.
	Token string
	// Attention carries no ID and no Category, which the caller keys.
	Attention api.Attention
}

// Noticer is optional on a Provider: what row, derived from in at at,
// notifies; ok is false when it notifies nothing.
type Noticer interface {
	Notice(base Base, row api.Row, in any, at time.Time) (Notice, bool)
}

// Notice is what the read's row notifies; ok is false when it notifies
// nothing or its provider notifies no row.
func (r Read) Notice() (Notice, bool) {
	if n, ok := r.p.(Noticer); ok {
		return n.Notice(r.Base, r.Row, r.In, r.At)
	}
	return Notice{}, false
}

// Booker is optional on a Provider: the turn book in, a row input of the
// provider, holds; has is false for a unit with none.
type Booker interface {
	Book(in any) (b turn.Book, has bool)
}

// Book is the turn book the read's input holds; kept is false when its
// provider keeps no book, so the caller reads it.
func (r Read) Book() (b turn.Book, has, kept bool) {
	bk, ok := r.p.(Booker)
	if !ok {
		return turn.Book{}, false, false
	}
	b, has = bk.Book(r.In)
	return b, has, true
}

// Signaler is optional on a Provider: the link signals of row, derived
// from in at at, under the host's overlays, by name.
type Signaler interface {
	Signals(base Base, row api.Row, in any, at time.Time) map[string]bool
}

// Signals are the link signals of the read's row, by name; none when its
// provider reports none.
func (r Read) Signals() map[string]bool {
	if s, ok := r.p.(Signaler); ok {
		return s.Signals(r.Base, r.Row, r.In, r.At)
	}
	return nil
}
