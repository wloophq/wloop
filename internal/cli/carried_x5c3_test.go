package cli

import "testing"

func TestServeHandsGitHubsAPIClientToNoInProcessModule(t *testing.T) {
	t.Skip("carried from X5c3 review: serve.go, plugincli.go and serve_github.go still hand GitHub's API client to the in-process module, presenter, collector and brief queue (source.Deps.GitHub, present.Deps.Explorer, brief.Source) until X8a2 runs GitHub as a binary")
}
