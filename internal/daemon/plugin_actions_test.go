package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/wloophq/wloop/internal/api"
)

func TestAPluginActionRelaysItsArgumentsAndTheAnswer(t *testing.T) {
	t.Parallel()
	var got []api.PluginActionRequest
	var ids []string
	s := newServerWith(t, func(d *Deps) {
		d.PluginAction = func(_ context.Context, id string, req api.PluginActionRequest) (json.RawMessage, error) {
			ids, got = append(ids, id), append(got, req)
			if req.Action == "eval" {
				return nil, errors.New("the plugin failed")
			}
			return json.RawMessage(`{"conversation":"C0000000001"}`), nil
		}
	})
	rec := post(t, s, api.PluginActionPath("notes", "send"), `{"args":{"target":"#general","dry_run":true}}`)
	if rec.Code != 200 || rec.Body.String() != `{"conversation":"C0000000001"}` {
		t.Fatalf("send: %d %s", rec.Code, rec.Body)
	}
	if len(got) != 1 || ids[0] != "notes" || got[0].Action != "send" || string(got[0].Args) != `{"target":"#general","dry_run":true}` {
		t.Fatalf("relayed %v %+v", ids, got)
	}
	if rec := post(t, s, api.PluginActionPath("notes", "eval"), `{"args":{}}`); rec.Code != 502 {
		t.Fatalf("a plugin's error: %d %s", rec.Code, rec.Body)
	}
	if rec := post(t, newServerWith(t, func(*Deps) {}), api.PluginActionPath("notes", "send"), `{}`); rec.Code != 404 {
		t.Fatalf("no plugin actions: %d", rec.Code)
	}
}
