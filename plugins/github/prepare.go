package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"time"

	sdk "github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
	"github.com/wloophq/wloop/plugins/github/nudge"
	"github.com/wloophq/wloop/plugins/github/pr"
)

// ActionNudgePrepare is the nudge's prepare: it refuses a nudge when
// nobody but the viewer is to be asked or the same wait was nudged
// already, and fills whom it asks into the call's inputs.
const ActionNudgePrepare = "nudge.prepare"

// prepareArgs are the args the host's daemon sends a prepare: the call's
// inputs and the unit's row without the user's mute.
type prepareArgs struct {
	Unmuted *struct {
		Kind    string `json:"kind"`
		Summary struct {
			WaitsOn string    `json:"waits_on"`
			Since   time.Time `json:"since"`
			Reason  string    `json:"reason"`
		} `json:"summary"`
		Payload json.RawMessage `json:"payload"`
	} `json:"unmuted"`
	Inputs map[string]any `json:"inputs"`
}

type prepareResult struct {
	Inputs map[string]any `json:"inputs"`
}

func (r *running) prepareNudge(ctx context.Context, u sdk.Unit, raw json.RawMessage) (json.RawMessage, error) {
	viewer, err := r.db.Viewer(ctx)
	if err != nil {
		return nil, err
	}
	x, err := r.texts(ctx)
	if err != nil {
		return nil, err
	}
	return PrepareNudge(ctx, r.host, NudgeEnv{Viewer: viewer, Ignore: r.settings().Ignore, Now: r.now(), Texts: x}, u, raw)
}

// NudgeEnv is what the nudge's prepare reads besides the host: the viewer,
// who a nudge never mentions, the time and the profile's texts.
type NudgeEnv struct {
	Viewer string
	Ignore []string
	Now    time.Time
	Texts  nudge.Texts
}

// PrepareNudge is the nudge's prepare of u with the daemon's args, reading
// the unit's events and turns from h.
func PrepareNudge(ctx context.Context, h src.Host, env NudgeEnv, u sdk.Unit, raw json.RawMessage) (json.RawMessage, error) {
	ref, err := refOf(u)
	if err != nil {
		return nil, err
	}
	var args prepareArgs
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &args); err != nil {
			return nil, fmt.Errorf("%w: %s: %v", src.ErrUnfit, ActionNudgePrepare, err)
		}
	}
	if args.Unmuted == nil {
		return nil, fmt.Errorf("%w: %s is not in the inventory", src.ErrNotFound, ref)
	}
	var payload struct {
		Author string `json:"author"`
	}
	if len(args.Unmuted.Payload) > 0 {
		_ = json.Unmarshal(args.Unmuted.Payload, &payload)
	}
	s := nudge.Situation{Kind: args.Unmuted.Kind, WaitsOn: args.Unmuted.Summary.WaitsOn, Since: args.Unmuted.Summary.Since, Reason: args.Unmuted.Summary.Reason}
	evs, book, err := hostOf(ctx, h, u)
	if err != nil {
		return nil, err
	}
	who, why := nudge.Targets(s, payload.Author, env.Viewer, env.Ignore)
	if refusal := nudge.Refusal(ref, who, why, s.Since, evs, nudge.Unstarted(book), env.Now); refusal != "" {
		return nil, fmt.Errorf("%w: %s", src.ErrUnfit, refusal)
	}
	inputs := maps.Clone(args.Inputs)
	if inputs == nil {
		inputs = map[string]any{}
	}
	line, err := env.Texts.Waiting(who, s)
	if err != nil {
		return nil, err
	}
	inputs["situation"] = line
	inputs["waits_on_maintainers"] = slices.Equal(who, []string{nudge.Maintainers})
	inputs["who"] = slices.Clone(who)
	return json.Marshal(prepareResult{Inputs: inputs})
}

// hostOf are the host's events and turns of u.
func hostOf(ctx context.Context, h src.Host, u sdk.Unit) ([]pr.Event, src.Book, error) {
	var evs []pr.Event
	if h.History != nil {
		hs, err := h.History.Events(ctx, src.EventQuery{Units: []sdk.Unit{u}})
		if err != nil && !errors.Is(err, src.ErrUnsupported) {
			return nil, src.Book{}, err
		}
		for _, e := range hs {
			if ref, ok := pr.RefOf(e.Unit); ok {
				evs = append(evs, pr.Event{PR: ref, Kind: e.Kind, Key: e.Key, Actor: e.Actor, At: e.At, Data: string(e.Data)})
			}
		}
	}
	var book src.Book
	if h.Reader != nil {
		books, err := h.Reader.Books(ctx, []sdk.Unit{u})
		if err != nil && !errors.Is(err, src.ErrUnsupported) {
			return nil, src.Book{}, err
		}
		if len(books) > 0 {
			book = books[0]
		}
	}
	return evs, book, nil
}
