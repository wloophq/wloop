package daemon

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/catalog"
	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/domain/unit"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/readmodel"
)

// NotOffered is the conflict of an action the row does not offer.
func NotOffered(action string, u unit.Unit) string {
	return fmt.Sprintf("no %s on %s", action, u.Label())
}

// offerOf is the state of id on the row, false when the row does not offer it.
func offerOf(r api.Row, id string) (api.ActionState, bool) {
	i := slices.IndexFunc(r.Actions, func(st api.ActionState) bool { return st.ID == id })
	if i < 0 {
		return api.ActionState{}, false
	}
	return r.Actions[i], true
}

type unavailable struct{ msg string }

func (e unavailable) Error() string { return e.msg }

// currentRow is the row of the unit as served now, or, for a unit the read
// model does not list, the row its plugin derives.
func (s *Server) currentRow(ctx context.Context, u unit.Unit) (api.Row, error) {
	return s.rowAsked(ctx, u, catalog.Asked{})
}

// rowAsked is currentRow for a request that asks for an action with its
// inputs.
func (s *Server) rowAsked(ctx context.Context, u unit.Unit, asked catalog.Asked) (api.Row, error) {
	m := s.readModel()
	rd, ok, err := m.Read(ctx, u)
	if err != nil {
		return api.Row{}, err
	}
	if !ok {
		row, found, err := s.pluginRow(ctx, api.UnitRowQuery{Unit: u, Action: asked.Name, Inputs: asked.Inputs})
		if err != nil {
			return api.Row{}, err
		}
		if !found {
			return api.Row{}, notFound{u.Label() + " is not in the inventory"}
		}
		if row.Unit == u || row.Unit.IsZero() {
			return row, nil
		}
		if rd, ok, err = m.Read(ctx, row.Unit); err != nil || !ok {
			return row, err
		}
	}
	if asked.Name == "" && asked.Inputs == nil {
		return rd.Row, nil
	}
	b := rd.Base
	b.Asked = asked
	return rd.Derive(b, rd.In)
}

// readRow is the read model's entry of the unit under any case: a unit it
// does not list under u's key is read under the key its plugin names it by.
func (s *Server) readRow(ctx context.Context, u unit.Unit) (readmodel.Read, bool, error) {
	m := s.readModel()
	rd, ok, err := m.Read(ctx, u)
	if ok || err != nil {
		return rd, ok, err
	}
	if _, has := s.presenter(u.Provider); !has {
		return readmodel.Read{}, false, nil
	}
	row, found, err := s.pluginRow(ctx, api.UnitRowQuery{Unit: u, Kept: true})
	if err != nil || !found || row.Unit == u || row.Unit.IsZero() {
		return readmodel.Read{}, false, err
	}
	return m.Read(ctx, row.Unit)
}

func forceableKey(u unit.Unit) string { return strings.ToLower(u.Label()) }

// forceableOf is the unit's last remove refusal that force gets past, ""
// for none.
func (s *Server) forceableOf(u unit.Unit) string {
	v, _ := s.forceable.Load(forceableKey(u))
	reason, _ := v.(string)
	return reason
}

// waitingIntakes are the open intakes by pull request, the oldest first,
// after the stale session ones expire; none when the daemon keeps none.
func (s *Server) waitingIntakes(ctx context.Context) (map[unit.Unit][]intake.Intake, error) {
	if s.intakes == nil {
		return nil, nil
	}
	open, err := s.openIntakes(ctx)
	if err != nil {
		return nil, err
	}
	out := map[unit.Unit][]intake.Intake{}
	for _, i := range open {
		out[i.Unit] = append(out[i.Unit], i)
	}
	return out, nil
}

func (s *Server) handleActions(w http.ResponseWriter, _ *http.Request) {
	l := s.loaded()
	off := s.disabledPlugins(l)
	writeJSON(w, api.ActionsResponse{Version: catalog.VersionFor(l, off), Specs: catalog.SpecsFor(l, off), Plan: catalog.PlanSpecs(l)})
}

// disabledPlugins are the disabled plugins some action of l belongs to.
func (s *Server) disabledPlugins(l *intentload.Loaded) []unit.ProviderID {
	if s.deps.PluginEnabled == nil {
		return nil
	}
	var off []unit.ProviderID
	for _, sp := range catalog.Specs(l) {
		if p := unit.ProviderID(sp.Plugin); p != "" && !slices.Contains(off, p) && !s.pluginEnabled(p) {
			off = append(off, p)
		}
	}
	return off
}

// LinkState is what the read model holds of unit u for the facts of the tasks that link it: its link signals,
// and when it last moved, the start of its situation under the host's overlays; ok is false for a unit no
// provider serves a row of.
func (s *Server) LinkState(ctx context.Context, u unit.Unit) (signals map[string]bool, moved time.Time, ok bool, err error) {
	rd, ok, err := s.readModel().Read(ctx, u)
	if err != nil || !ok {
		return nil, time.Time{}, false, err
	}
	return rd.Signals(), rd.Row.Summary.Since, true, nil
}
