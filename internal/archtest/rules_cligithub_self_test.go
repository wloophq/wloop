package archtest

import "testing"

func TestCLIGitHubClientFlagsAPlantedImport(t *testing.T) {
	t.Parallel()
	imports := []string{"context", module + "internal/github", module + "internal/github/githubtest", module + "internal/ghcli", module + "internal/domain/pr"}
	debts := map[string]string{"internal/cli/old_test.go": "debt: planted", "internal/cli/serve.go": "debt: planted"}
	used := map[string]bool{}
	flagged(t, "a command", ruleCLIGitHubClient("internal/cli/doctor.go", imports, debts, used), 3, "only through its plugin")
	flagged(t, "a test", ruleCLIGitHubClient("internal/cli/new_test.go", imports, debts, used), 3, "only through its plugin")
	flagged(t, "a test in debt", ruleCLIGitHubClient("internal/cli/old_test.go", imports, debts, used), 0, "")
	flagged(t, "a non-test file in debt", ruleCLIGitHubClient("internal/cli/serve.go", imports, debts, used), 3, "only through its plugin")
	flagged(t, "the plugin", ruleCLIGitHubClient("internal/adapter/source/github/local.go", imports, debts, used), 0, "")
	if !used["internal/cli/old_test.go"] || used["internal/cli/serve.go"] {
		t.Errorf("used: %v", used)
	}
}
