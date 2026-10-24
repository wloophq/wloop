package client

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/wloophq/wloop/internal/api"
)

func TestKeysAsksTheSurface(t *testing.T) {
	seen := make(chan string, 1)
	socket := serve(t, func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Method + " " + r.URL.Path + "?" + r.URL.RawQuery
		fmt.Fprint(w, `{"bindings":[{"key":"g i","action":"nav:inbox","context":"global","owner":"core","help":"Inbox"}]}`)
	})
	res, err := New(socket).Keys(context.Background(), "tui")
	if err != nil || len(res.Bindings) != 1 || res.Bindings[0] != (api.KeyView{Key: "g i", Action: "nav:inbox", Context: "global", Owner: "core", Help: "Inbox"}) {
		t.Fatalf("%+v %v", res, err)
	}
	if got := <-seen; got != "GET "+api.Keys+"?surface=tui" {
		t.Fatalf("sent %q", got)
	}
}
