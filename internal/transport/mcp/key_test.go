package mcp

import (
	"net/http"
	"testing"

	"github.com/wloophq/wloop/internal/api"
)

func TestTheEndpointRefusesARequestWithoutTheInstallKey(t *testing.T) {
	key := "k1"
	s := &Server{Turns: &fakeTurns{}, Port: 47800, Key: func() string { return key }}
	withKey := func(v string) func(*http.Request) {
		return func(r *http.Request) { r.Header.Set(api.MCPKeyHeader, v) }
	}
	for name, edit := range map[string]func(*http.Request){
		"none":  func(*http.Request) {},
		"stale": withKey("k0"),
		"empty": withKey(""),
	} {
		if rec := call(t, s, "", "", initialize, edit); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	rec := call(t, s, "", "", initialize, withKey("k1"))
	if rec.Code != http.StatusOK || rec.Header().Get("Mcp-Session-Id") == "" {
		t.Fatalf("keyed: %d %s", rec.Code, rec.Body)
	}
	key = ""
	if rec := call(t, s, "", "", initialize, withKey("")); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no key in state: %d", rec.Code)
	}
}
