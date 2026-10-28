package present

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/duration"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// clearedMute is the set that clears every mute override.
func clearedMute() map[string]string {
	return map[string]string{port.OverrideMutedUntil: "", port.OverrideMuteUntilStateChange: "", port.OverrideMutedKind: "",
		port.OverrideMutedWaitingOn: ""}
}

// mute mutes u for the request's duration, or until its situation, as the
// row shows it unmuted, changes.
func (p *Presenter) mute(ctx context.Context, u unit.Unit, args api.PluginActionArgs) error {
	req := args.Request
	if req.Until == "" && !req.UntilState {
		return api.Refuse(http.StatusBadRequest, "mute needs until or until_state")
	}
	if req.Until != "" && req.UntilState {
		return api.Refuse(http.StatusBadRequest, "mute takes until or until_state, not both")
	}
	set := clearedMute()
	data := "until state change"
	if req.UntilState {
		if args.Unmuted == nil {
			return api.Refuse(http.StatusNotFound, "%s is not in the inventory", u.Label())
		}
		set[port.OverrideMuteUntilStateChange] = port.OverrideTrue
		set[port.OverrideMutedKind], set[port.OverrideMutedWaitingOn] = string(args.Unmuted.Kind), args.Unmuted.Summary.WaitsOn
	} else {
		d, err := duration.Parse(req.Until)
		if err != nil {
			return api.Refuse(http.StatusBadRequest, "mute %v", err)
		}
		set[port.OverrideMutedUntil] = p.h.Now().Add(d).UTC().Format(time.RFC3339Nano)
		data = "for " + req.Until
	}
	if err := p.setOverride(ctx, u, set); err != nil {
		return err
	}
	return p.recordEvent(ctx, u, dinv.KindMuted, data)
}

func (p *Presenter) unmute(ctx context.Context, u unit.Unit) error {
	if err := p.setOverride(ctx, u, clearedMute()); err != nil {
		return err
	}
	return p.recordEvent(ctx, u, dinv.KindUnmuted, "")
}

// overrideAttempts bounds the rereads of an override another writer, such
// as a mute expiring, changed between read and write.
const overrideAttempts = 5

// setOverride writes set over u's host overrides, compare-and-set over
// what it read.
func (p *Presenter) setOverride(ctx context.Context, u unit.Unit, set map[string]string) error {
	if p.d.Host.Gates == nil || p.d.Host.Reader == nil {
		return api.Refuse(http.StatusServiceUnavailable, "the host keeps no overrides")
	}
	p.overrides.Lock()
	defer p.overrides.Unlock()
	for range overrideAttempts {
		st, err := p.d.Host.Reader.RoundState(ctx, wire(u))
		if err != nil {
			return err
		}
		was := make(map[string]string, len(set))
		for n := range set {
			was[n] = st.Overrides[n]
		}
		written, err := p.d.Host.Gates.SetOverride(ctx, wire(u), set, was)
		if err != nil || written {
			return err
		}
	}
	return fmt.Errorf("%s: the override kept changing while it was written", u.Label())
}

// expireLapsedMutes clears every "mute until state change" of k's rows
// whose situation changed, while it is still the mute the row held, and
// records the unmute.
func (p *Presenter) expireLapsedMutes(ctx context.Context, k readmodel.Kept) error {
	for _, o := range prrow.LapsedMutes(k) {
		if _, err := p.expireMute(ctx, o); err != nil {
			return err
		}
	}
	return nil
}

func (p *Presenter) expireMute(ctx context.Context, o dinv.Override) (cleared bool, err error) {
	if p.d.Host.Gates == nil {
		return false, src.ErrUnsupported
	}
	u := o.PR.Unit()
	p.overrides.Lock()
	cleared, err = p.d.Host.Gates.SetOverride(ctx, wire(u), clearedMute(),
		map[string]string{port.OverrideMuteUntilStateChange: port.OverrideTrue, port.OverrideMutedKind: o.MutedKind, port.OverrideMutedWaitingOn: o.MutedWaitingOn})
	p.overrides.Unlock()
	if err != nil || !cleared {
		return false, err
	}
	return true, p.recordEvent(ctx, u, dinv.KindUnmuted, "state changed")
}
