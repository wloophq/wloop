package github

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

// deniedRules are the manifest's write_commands as the session policy
// renders them for Claude Code: a prefix whose last word does not end in *
// gains " *", and * matches any text, case-sensitively.
func deniedRules(t *testing.T) []*regexp.Regexp {
	t.Helper()
	raw, err := os.ReadFile("plugin.toml")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Source struct {
			Session struct {
				WriteCommands []string `toml:"write_commands"`
			} `toml:"session"`
		} `toml:"source"`
	}
	if err := toml.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	var out []*regexp.Regexp
	for _, prefix := range m.Source.Session.WriteCommands {
		rule := prefix
		if !strings.HasSuffix(prefix, "*") {
			rule += " *"
		}
		parts := strings.Split(rule, "*")
		for i, p := range parts {
			parts[i] = regexp.QuoteMeta(p)
		}
		out = append(out, regexp.MustCompile("^"+strings.Join(parts, ".*")+"$"))
	}
	return out
}

func TestWriteCommandsDenyEveryWriteForm(t *testing.T) {
	t.Parallel()
	rules := deniedRules(t)
	denied := func(cmd string) bool {
		return slices.ContainsFunc(rules, func(r *regexp.Regexp) bool { return r.MatchString(cmd) })
	}
	var writes []string
	for _, method := range []string{"POST", "post", "Post", "PATCH", "patch", "PUT", "put", "DELETE", "delete", "Delete"} {
		writes = append(writes,
			"gh api -X "+method+" repos/acme/api/pulls/7/reviews --input r.json",
			"gh api -X"+method+" repos/acme/api/pulls/7/reviews",
			"gh api -X="+method+" repos/acme/api/pulls/7/reviews",
			"gh api --method "+method+" repos/acme/api/pulls/7/reviews",
			"gh api --method="+method+" repos/acme/api/pulls/7/reviews",
			"gh api repos/acme/api/pulls/7/reviews -X "+method,
			"gh api repos/acme/api/pulls/7/reviews -X"+method,
			"gh api repos/acme/api/pulls/7/reviews --method="+method,
		)
	}
	for _, field := range []string{"-f body=hi", "-fbody=hi", "-F body=hi", "-Fbody=hi", "--field body=hi", "--field=body=hi",
		"--raw-field body=hi", "--raw-field=body=hi", "--input r.json", "--input=r.json"} {
		writes = append(writes, "gh api repos/acme/api/issues/7/comments "+field, "gh api "+field+" repos/acme/api/issues/7/comments",
			"gh api graphql "+field)
	}
	writes = append(writes,
		"gh pr review 7 --approve", "gh pr comment 7 -b hi", "gh pr merge 7", "gh pr close 7", "gh pr reopen 7", "gh pr edit 7 --title x",
		"gh pr ready 7", "gh pr create --fill", "gh pr update-branch 7", "gh pr lock 7", "gh pr unlock 7",
		"gh issue create -t x", "gh issue comment 7 -b hi", "gh issue close 7", "gh issue reopen 7", "gh issue edit 7 --add-label x",
		"gh issue delete 7 --yes", "gh issue transfer 7 acme/web", "gh issue lock 7", "gh issue unlock 7",
		"gh label create bug", "gh release create v1", "gh workflow run ci.yml", "gh run rerun 9", "gh repo edit --visibility public",
		"gh secret set TOKEN", "gh variable set X", "gh gist create f.txt", "gh project item-add 1 --url x", "gh cache delete --all",
	)
	writes = append(writes, "gh alias set co 'pr comment'", "gh alias import aliases.yml", "gh extension install acme/gh-post",
		"gh extension exec post", "gh pr revert 7", "gh discussion create --title x --body y", "gh discussion comment 3 --body y",
		"gh discussion edit 3 --title x", "gh agent-task create 'fix it'", "gh skill publish", "gh skill install acme/skills",
		"gh skill update", "gh copilot suggest 'push it'")
	for _, cmd := range slices.Clone(writes) {
		if w := strings.Fields(cmd); w[1] != "api" && len(w) > 3 {
			writes = append(writes, strings.Join(w[:3], " "))
		}
	}
	for _, cmd := range slices.Clone(writes) {
		rest := strings.TrimPrefix(cmd, "gh ")
		writes = append(writes, "gh -R acme/api "+rest, "gh --hostname ghe.acme.test "+rest, "gh --repo=acme/api "+rest)
	}
	for _, cmd := range writes {
		if !denied(cmd) {
			t.Errorf("not denied: %s", cmd)
		}
	}
	for _, cmd := range []string{
		"gh api repos/acme/api/pulls/7/comments",
		"gh api repos/acme/api/pulls/7/comments --paginate",
		"gh api repos/acme/api/pulls/7 --jq .head.sha",
		"gh api repos/acme/api/pulls/7/reviews --paginate --jq '.[] | .state'",
		"gh pr view 7 --json reviews,comments",
		"gh pr diff 7",
		"gh pr checks 7",
		"gh pr view 7 --comments",
		"gh issue view 7",
		"gh run view 9 --log-failed",
		"gh -R acme/api pr view 7 --json reviews",
		"gh --hostname ghe.acme.test api repos/acme/api/pulls/7/comments",
		"gh alias list",
		"gh extension list",
		"gh discussion view 3",
		"gh discussion list",
		"gh agent-task list",
		"gh skill list",
	} {
		if denied(cmd) {
			t.Errorf("a read denied: %s", cmd)
		}
	}
}
