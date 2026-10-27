package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNotificationsDigestAt(t *testing.T) {
	if got := Defaults().Notifications.DigestAt; got != "09:00" {
		t.Fatalf("default %q", got)
	}
	for raw, want := range map[string]string{``: "09:00", `notifications = {digest_at = ""}`: "", `notifications = {digest_at = "18:30"}`: "18:30"} {
		path := filepath.Join(t.TempDir(), "config.toml")
		if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		c, err := LoadFile(path, tools())
		if err != nil || c.Notifications.DigestAt != want {
			t.Errorf("%s: %q %v", raw, c.Notifications.DigestAt, err)
		}
	}
	path := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(path, []byte(`notifications = {digest_at = "9am"}`), 0o600)
	if _, err := LoadFile(path, tools()); err == nil || !strings.Contains(err.Error(), "notifications.digest_at") {
		t.Fatalf("a bad time: %v", err)
	}
}
