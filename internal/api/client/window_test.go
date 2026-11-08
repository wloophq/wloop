package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/wloophq/wloop/internal/api"
)

func TestClientWindowPostsTheRequest(t *testing.T) {
	got := make(chan api.WindowRequest, 1)
	socket := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != api.WindowPath {
			http.NotFound(w, r)
			return
		}
		var req api.WindowRequest
		json.NewDecoder(r.Body).Decode(&req)
		got <- req
		fmt.Fprint(w, `{"delivered":true}`)
	})
	res, err := New(socket).Window(context.Background(), api.WindowRequest{Action: api.WindowOpen, Route: "view=needs-me"})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Delivered {
		t.Fatal("Delivered false, the daemon served true")
	}
	if req := <-got; req != (api.WindowRequest{Action: api.WindowOpen, Route: "view=needs-me"}) {
		t.Fatalf("posted %+v", req)
	}
}
