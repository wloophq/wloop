package source_test

import (
	"testing"

	"github.com/wloophq/wloop/internal/plugins/bundled"
	psource "github.com/wloophq/wloop/internal/plugins/source"
	"github.com/wloophq/wloop/plugins/slack"
)

// TestTheSlackManifestTakesTheTargetsItsPluginPostsTo checks the bundled
// Slack manifest's targets against the plugin's own publisher.
func TestTheSlackManifestTakesTheTargetsItsPluginPostsTo(t *testing.T) {
	srcs, err := bundled.Sources()
	if err != nil {
		t.Fatal(err)
	}
	var p psource.Publisher
	for _, s := range srcs {
		if s.Provider == "slack" {
			p = psource.Publisher{ID: s.Provider, Source: s}
		}
	}
	if p.Source == nil {
		t.Fatal("no bundled slack manifest")
	}
	for _, target := range []string{
		"C0000000001/1700000000.000100", " D0000000001/1700000000.000100 ", "G0AB/1700000000.000100",
		"https://acme.slack.com/archives/C0000000001/p1700000000000100",
		"https://Acme.Slack.com/archives/C0000000001/p1700000000000100?thread_ts=1700000000.000050&cid=C0000000001",
		"http://slack.com/archives/D0000000001/p1700000000000100/",
		"#general", "C0000000001", "U0000000001", "@U0000000001", "@dana", "dana@example.com", "@{slack:U0000000001}", "slack:U0000000001",
		"X0000000001/1700000000.000100", "C0000000001/1700000000.1", "acme/api#12", "task:t-1", "slack:T000/C000/1.2", "dana smith",
		"https://github.com/acme/api/pull/12", "https://acme.slack.com/archives/C0000000001", "https://evil.example/archives/C1/p1700000000000100",
	} {
		if got, want := p.Takes(target), (&slack.Publisher{}).Takes(target); got != want {
			t.Errorf("Takes(%q) = %t, the plugin's publisher takes it: %t", target, got, want)
		}
	}
}
