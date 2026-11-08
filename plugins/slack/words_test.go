package slack

import (
	"os"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

// TestEveryCardSignalCodeIsWordedInEachLocale holds that the plugin's layer words, in English and in pt-BR, every
// signal key the card sends and every value code it sends.
func TestEveryCardSignalCodeIsWordedInEachLocale(t *testing.T) {
	codes := map[string][]string{SignalItems: {ItemsNone, ItemsOpen}, SignalReplies: {RepliesCount}, SignalLastActivity: nil}
	for _, path := range []string{"profile/ui.toml", "profile/locales/pt-BR/ui.toml"} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var w struct {
			Signals map[string]struct {
				Label  string            `toml:"label"`
				Values map[string]string `toml:"values"`
			} `toml:"signals"`
		}
		if err := toml.Unmarshal(raw, &w); err != nil {
			t.Fatal(err)
		}
		for key, vs := range codes {
			if strings.TrimSpace(w.Signals[key].Label) == "" {
				t.Errorf("%s: signal %s has no label", path, key)
			}
			for _, v := range vs {
				if strings.TrimSpace(w.Signals[key].Values[v]) == "" {
					t.Errorf("%s: %s=%s has no words", path, key, v)
				}
			}
		}
	}
}
