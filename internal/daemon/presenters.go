package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/readmodel"
	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// Presenter is a plugin's source logic as the daemon reaches it: the
// actions a person runs on its units and its views. A running plugin's
// client is one; so is a compiled-in module's.
type Presenter interface {
	Action(ctx context.Context, req src.ActionRequest) (json.RawMessage, error)
	View(ctx context.Context, req src.ViewRequest) (json.RawMessage, error)
}

// Keeper is a compiled-in plugin's bookkeeping on each new snapshot of the
// rows: the writes its rows imply, such as a mute whose situation changed.
type Keeper interface {
	Keep(ctx context.Context, k readmodel.Kept) error
}

// PresenterHost is what the daemon serves a compiled-in plugin's presenter
// beside the broker: the rows as the read model derives them.
type PresenterHost struct {
	Model *readmodel.Model
	// Settings are the rows' settings now; BriefOn says the brief queue runs.
	Settings func() readmodel.RowSettings
	BriefOn  bool
	// Intakes are the open intakes by unit, the oldest first.
	Intakes func(ctx context.Context) (map[unit.Unit][]intake.Intake, error)
	// Books are the units' turn books; nil when the daemon keeps no turns.
	Books func(ctx context.Context, units ...unit.Unit) (map[unit.Unit]turn.Book, error)
	// Viewer is who the viewer is in the forge, cached.
	Viewer func(ctx context.Context) (src.Identity, error)
	// Units are the daemon's codecs.
	Units *unit.Registry
	Now   func() time.Time
	Logf  func(format string, args ...any)
}

// presenter is the presenter of provider p's plugin, enabled or not: a
// disabled one's views still serve its units read only.
func (s *Server) presenter(p unit.ProviderID) (Presenter, bool) {
	s.presentersOnce.Do(s.buildPresenters)
	if pr, ok := s.presenters[p]; ok {
		return pr, true
	}
	if s.deps.PluginPresenter != nil {
		return s.deps.PluginPresenter(p)
	}
	return nil, false
}

func (s *Server) buildPresenters() {
	s.presenters = map[unit.ProviderID]Presenter{}
	h := PresenterHost{Model: s.readModel(), Settings: s.rowSettings, BriefOn: s.deps.Brief != nil, Intakes: s.waitingIntakes,
		Viewer: s.viewerInfo, Units: s.deps.Units, Now: s.deps.Now, Logf: s.logf}
	if s.deps.Turns != nil {
		h.Books = s.deps.Turns.Store.Books
	}
	for p, build := range s.deps.Presenters {
		s.presenters[p] = build(h)
	}
}

// keepPlugins runs every compiled-in plugin's bookkeeping on k.
func (s *Server) keepPlugins(ctx context.Context, k readmodel.Kept) error {
	s.presentersOnce.Do(s.buildPresenters)
	var errs []error
	for p, pr := range s.presenters {
		if keeper, ok := pr.(Keeper); ok && s.pluginEnabled(p) {
			errs = append(errs, keeper.Keep(ctx, k))
		}
	}
	return errors.Join(errs...)
}

func wireUnit(u unit.Unit) plugin.Unit { return plugin.Unit{Provider: string(u.Provider), Key: u.Key} }

// pluginView asks provider p's plugin for view with query.
func (s *Server) pluginView(ctx context.Context, p unit.ProviderID, view string, query any) (json.RawMessage, error) {
	pr, ok := s.presenter(p)
	if !ok {
		return nil, notFound{fmt.Sprintf("no plugin %s serves views", p)}
	}
	q, err := json.Marshal(query)
	if err != nil {
		return nil, err
	}
	out, err := pr.View(ctx, src.ViewRequest{View: view, Query: q})
	return out, pluginErr(err, fmt.Sprintf("%s has no view %s", p, view))
}

// pluginViewOf decodes view of provider p into out; found is false when
// the plugin has no such view or no such unit.
func (s *Server) pluginViewOf(ctx context.Context, p unit.ProviderID, view string, query, out any) (found bool, err error) {
	raw, err := s.pluginView(ctx, p, view, query)
	var missing notFound
	if errors.As(err, &missing) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal(raw, out)
}

// pluginRow is the row of u its plugin derives, for a unit the read model
// does not list; found is false when the plugin has none.
func (s *Server) pluginRow(ctx context.Context, q api.UnitRowQuery) (api.Row, bool, error) {
	var row api.Row
	found, err := s.pluginViewOf(ctx, q.Unit.Provider, api.ViewUnitRow, q, &row)
	return row, found, err
}

// pluginAction runs action name of u's plugin with the request and the
// unit's row as it stands unmuted, and writes what it reports to out.
func (s *Server) pluginAction(ctx context.Context, name string, u unit.Unit, req api.ActionRequest, out io.Writer) error {
	res, err := s.askPlugin(ctx, name, u, api.PluginActionArgs{Request: req})
	if err != nil {
		return err
	}
	for _, l := range res.Output {
		fmt.Fprintln(out, l)
	}
	return nil
}

// askPlugin runs action name of u's plugin with args, the unit's unmuted
// row added.
func (s *Server) askPlugin(ctx context.Context, name string, u unit.Unit, args api.PluginActionArgs) (api.PluginActionResult, error) {
	pr, ok := s.presenter(u.Provider)
	if !ok || !s.pluginEnabled(u.Provider) {
		return api.PluginActionResult{}, notFound{NotOffered(name, u)}
	}
	if row, err := s.unmutedRow(ctx, u); err == nil {
		args.Unmuted = &row
	} else if !errors.As(err, new(notFound)) {
		return api.PluginActionResult{}, err
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return api.PluginActionResult{}, err
	}
	got, err := pr.Action(ctx, src.ActionRequest{Unit: wireUnit(u), Action: name, Input: args.Request.Until, Args: raw})
	if err != nil {
		return api.PluginActionResult{}, pluginErr(err, NotOffered(name, u))
	}
	var res api.PluginActionResult
	if len(got) > 0 {
		if err := json.Unmarshal(got, &res); err != nil {
			return res, fmt.Errorf("%s of %s answered %s: %w", name, u.Label(), got, err)
		}
	}
	return res, nil
}

// pluginErr is a plugin's error as the daemon answers it; unsupported is
// the not found it answers for a call the plugin does not serve.
func pluginErr(err error, unsupported string) error {
	var refused *api.PluginError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &refused):
		switch refused.Status {
		case http.StatusBadRequest:
			return badRequest{refused.Msg}
		case http.StatusNotFound:
			return notFound{refused.Msg}
		case http.StatusConflict:
			return conflict{msg: refused.Msg}
		case http.StatusTooManyRequests:
			return tooManyRequests{refused.Msg}
		case http.StatusBadGateway:
			return badGateway{refused.Msg}
		case http.StatusServiceUnavailable:
			return unavailable{refused.Msg}
		}
		return err
	case errors.Is(err, src.ErrUnsupported):
		return notFound{unsupported}
	case errors.Is(err, src.ErrUnfit):
		return badRequest{trimSentinel(err, src.ErrUnfit)}
	case errors.Is(err, src.ErrNotFound):
		return notFound{trimSentinel(err, src.ErrNotFound)}
	case errors.Is(err, src.ErrUnavailable):
		return unavailable{err.Error()}
	}
	return err
}

// handlePluginView serves view of plugin id: a GET's query parameters, or
// a POST's body, are the view's query.
func (s *Server) handlePluginView(w http.ResponseWriter, r *http.Request) {
	p := unit.ProviderID(r.PathValue("id"))
	if s.refusePlugin(w, unit.Unit{Provider: p}) {
		return
	}
	var query json.RawMessage
	if r.Method == http.MethodPost {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid body: "+err.Error())
			return
		}
		if len(body) > 0 {
			if !json.Valid(body) {
				writeError(w, http.StatusBadRequest, "invalid body: not JSON")
				return
			}
			query = body
		}
	} else {
		params := map[string]string{}
		for k, v := range r.URL.Query() {
			if k != "api" && len(v) > 0 {
				params[k] = v[0]
			}
		}
		query, _ = json.Marshal(params)
	}
	pr, ok := s.presenter(p)
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("no plugin %s serves views", p))
		return
	}
	view := r.PathValue("view")
	out, err := pr.View(r.Context(), src.ViewRequest{View: view, Query: query})
	if err != nil {
		writeActionError(w, pluginErr(err, fmt.Sprintf("%s has no view %s", p, view)))
		return
	}
	if len(out) == 0 {
		out = json.RawMessage("null")
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(append(out, '\n'))
}

// trimSentinel is err's message without the sentinel's prefixes, which the
// plugin's side and the protocol's each add.
func trimSentinel(err, sentinel error) string {
	msg := err.Error()
	for prefix := sentinel.Error() + ": "; strings.HasPrefix(msg, prefix); {
		msg = strings.TrimPrefix(msg, prefix)
	}
	return msg
}
