package present

import (
	"context"
	"net/http"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/duration"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/unit"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// untilStateChange is the When of a mute until the state changes: who the
// task's links wait on, and whether a review workspace holds one or its work
// is approved, facts alone, which the host holds to each task's values of
// them at mute time. The stalled signal is left out: time alone turns it on.
const untilStateChange = "task.link_owes, task.link_signal.in_loop, task.link_signal.ready"

// mute mutes u for the request's duration, or until who its task's items
// wait on changes: a wake on the pull request's tasks.
func (p *Presenter) mute(ctx context.Context, u unit.Unit, args api.PluginActionArgs) error {
	req := args.Request
	if req.Until == "" && !req.UntilState {
		return api.Refuse(http.StatusBadRequest, "mute needs until or until_state")
	}
	if req.Until != "" && req.UntilState {
		return api.Refuse(http.StatusBadRequest, "mute takes until or until_state, not both")
	}
	if p.d.Host.Wakes == nil {
		return api.Refuse(http.StatusServiceUnavailable, "the host keeps no wakes")
	}
	w := src.Wake{Unit: wire(u)}
	data := "until state change"
	if req.UntilState {
		if args.Unmuted == nil {
			return api.Refuse(http.StatusNotFound, "%s is not in the inventory", u.Label())
		}
		w.When = untilStateChange
	} else {
		d, err := duration.Parse(req.Until)
		if err != nil {
			return api.Refuse(http.StatusBadRequest, "mute %v", err)
		}
		w.Until = p.h.Now().Add(d).UTC()
		data = "for " + req.Until
	}
	if err := p.d.Host.Wakes.SetWake(ctx, w); err != nil {
		return err
	}
	return p.recordEvent(ctx, u, dinv.KindMuted, data)
}

func (p *Presenter) unmute(ctx context.Context, u unit.Unit) error {
	if p.d.Host.Wakes == nil {
		return api.Refuse(http.StatusServiceUnavailable, "the host keeps no wakes")
	}
	if err := p.d.Host.Wakes.ClearWake(ctx, wire(u), ""); err != nil {
		return err
	}
	return p.recordEvent(ctx, u, dinv.KindUnmuted, "")
}
