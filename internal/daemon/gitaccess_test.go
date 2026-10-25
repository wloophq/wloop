package daemon

import (
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/api"
)

func TestGitAccessKeepsTheLastOutcomeAndTheLastSuccessPerHost(t *testing.T) {
	t.Parallel()
	s := newServer(t)
	for _, body := range []string{
		`{"host":"github.com","at":"2026-10-20T10:00:00Z","op":"fetch of acme/api#7"}`,
		`{"host":"github.com","at":"2026-10-20T11:00:00Z","op":"fetch of acme/api#8","error":"Permission denied (publickey)"}`,
		`{"host":"github.com","at":"2026-10-20T09:00:00Z","op":"fetch of acme/api#6"}`,
		`{"host":"gitlab.com","at":"2026-10-20T10:30:00Z","op":"ls-remote (doctor --git)"}`,
	} {
		if rec := post(t, s, api.AdminGitAccess, body); rec.Code != 204 {
			t.Fatalf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}
	if rec := post(t, s, api.AdminGitAccess, `{"op":"x"}`); rec.Code != 400 {
		t.Fatalf("a record without host: %d", rec.Code)
	}
	var h api.Health
	if code := get(t, s, "/api/health", &h); code != 200 {
		t.Fatalf("%d", code)
	}
	at := func(s string) time.Time { v, _ := time.Parse(time.RFC3339, s); return v }
	if len(h.GitAccess) != 2 {
		t.Fatalf("%+v", h.GitAccess)
	}
	gh, gl := h.GitAccess[0], h.GitAccess[1]
	if gh.Host != "github.com" || gh.Last.Op != "fetch of acme/api#8" || gh.Last.Error == "" || !gh.Last.At.Equal(at("2026-10-20T11:00:00Z")) ||
		gh.LastOK == nil || gh.LastOK.Op != "fetch of acme/api#7" {
		t.Fatalf("github.com: %+v %+v", gh, gh.LastOK)
	}
	if gl.Host != "gitlab.com" || gl.Last.Op != "ls-remote (doctor --git)" || gl.LastOK == nil {
		t.Fatalf("gitlab.com: %+v", gl)
	}
}
