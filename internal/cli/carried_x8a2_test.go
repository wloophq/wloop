package cli

import "testing"

func TestServeHandsGitHubsAPIClientToNoInProcessModule(t *testing.T) {
	t.Skip("carried from X8a2 review: under serve the host still builds GitHub requests and sends them through the plugin's github.forward action: " +
		"the publisher (ghsource.Publisher.GH, withGitHubClient, X8c1), the brief source (brief.Source{GitHub}, serve_github.go:155 and module.go:47, X8c2), " +
		"the presenter's Explorer, FailedSteps and Budget (serve_github.go:143,174,193, X8c3); forgeCalls falls back to ghsource.Local (gh in the host) when the plugin is not running (X8c3). " +
		"Once they move, github.forward, ghsource.PluginTransport and PluginClient, internal/github's gh auth token and internal/ghcli leave internal/")
}
