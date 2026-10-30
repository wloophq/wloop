package source

import (
	"context"
	"time"

	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/core/outbox"
	"github.com/wloophq/wloop/internal/domain/draft"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/task"
	dturn "github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// Hosted are the host's services a plugin spends beyond its unit data: the
// items ledger, the agent, the profile layer, the drafts it proposes, the
// sessions that propose them, the tasks of its units and the briefs it
// writes. A nil field answers its calls with ErrUnsupported.
type Hosted struct {
	Items ItemLedger
	// Ask asks the host's agent; an answer over its cost cap fails with
	// turns.ErrOverBudget.
	Ask func(ctx context.Context, req src.AskRequest) (src.AskResult, error)
	// Profile reads path in the plugin's profile layer.
	Profile func(ctx context.Context, path string) ([]byte, error)
	// Sessions are the host's sessions and their turns.
	Sessions SessionBooks
	// Intents is the loaded registry, whose drafts.allowed_effects bound a
	// session's drafts.
	Intents func() *dintent.Registry
	Drafts  DraftGate
	Tasks   TaskLookup
	// Resolve is the unit an address lands in, through every plugin's codec.
	Resolve func(ctx context.Context, address string) (unit.Unit, bool, error)
	Now     func() time.Time
}

// ItemLedger is the host's items ledger.
type ItemLedger interface {
	Apply(ctx context.Context, prov items.Provenance, ops []items.Op) (coreitems.Result, error)
	Query(ctx context.Context, q coreitems.Query) ([]items.Item, error)
}

// SessionBooks are the host's sessions and the turns their books hold.
type SessionBooks interface {
	Session(ctx context.Context, id string) (dturn.Session, bool, error)
	// BookOf is the book of the session's turns; false for none.
	BookOf(ctx context.Context, session string) (dturn.Book, bool, error)
}

// DraftGate is the host's outbox as a plugin proposes and records drafts.
type DraftGate interface {
	Propose(ctx context.Context, p outbox.Proposal) (draft.Draft, error)
	RecordSent(ctx context.Context, p outbox.Proposal, ev draft.Evidence) (draft.Draft, error)
	PublisherOf(d draft.Draft) unit.ProviderID
}

// TaskLookup reads the tasks linked to a unit.
type TaskLookup interface {
	TasksOf(ctx context.Context, u unit.Unit) ([]task.Task, error)
}

func (h *Hosted) now() time.Time {
	if h == nil || h.Now == nil {
		return time.Now()
	}
	return h.Now()
}

// allowedEffects are the effects intent lets a draft of its sessions carry.
func (h *Hosted) allowedEffects(intent string) []string {
	if h.Intents == nil {
		return nil
	}
	r := h.Intents()
	if r == nil {
		return nil
	}
	def, ok := r.Get(dintent.Name(intent))
	if !ok {
		return nil
	}
	return def.AllowedEffects
}
