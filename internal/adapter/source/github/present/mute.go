package present

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/domain/duration"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/unit"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// mute mutes u for the request's duration, or until who its task's items
// wait on changes: a wake on the pull request's task.
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
		owes, err := p.owes(ctx, u)
		if err != nil {
			return err
		}
		w.When = OwesChange(owes)
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

// owes are who u's open items, as the plugin last reported them, wait on,
// each once, as task.link_owes names them: me for the viewer.
func (p *Presenter) owes(ctx context.Context, u unit.Unit) ([]string, error) {
	rep, ok, err := p.reportOf(ctx, u)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, api.Refuse(http.StatusNotFound, "%s is not in the inventory", u.Label())
	}
	var viewer src.Person
	if p.h.Viewer != nil {
		id, err := p.h.Viewer(ctx)
		if err != nil {
			return nil, err
		}
		viewer = id.Viewer
	}
	out := []string{}
	for _, it := range rep.Items {
		if it.State != "" && it.State != "open" {
			continue
		}
		for _, who := range it.WaitingOn {
			k := who.Key
			if k != "" && (k == viewer.Key || slices.Contains(viewer.Aliases, k)) {
				k = "me"
			}
			if k != "" && !slices.Contains(out, k) {
				out = append(out, k)
			}
		}
	}
	return out, nil
}

// OwesChange is the condition that holds once task.link_owes is no longer
// the set owes, in any order.
func OwesChange(owes []string) string {
	parts := []string{"size(task.link_owes) != " + strconv.Itoa(len(owes))}
	for _, k := range owes {
		parts = append(parts, "!("+strconv.Quote(k)+" in task.link_owes)")
	}
	return strings.Join(parts, " || ")
}
