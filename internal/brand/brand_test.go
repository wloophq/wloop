package brand

import "testing"

func TestBrandValues(t *testing.T) {
	if Name != "wloop" {
		t.Skip("built with another brand")
	}
	for _, c := range []struct{ name, got, want string }{
		{"Command", Command, "wloop"},
		{"App", App, "wloop"},
		{"MCPServer", MCPServer(), "wloop"},
		{"ToolPrefix", ToolPrefix(), "mcp__wloop__"},
		{"LaunchAgent", LaunchAgent(), "io.github.wloophq.wloop"},
		{"StateDir", StateDir(), "wloop"},
		{"SkillName", SkillName("review"), "wloop-review"},
		{"DocsURL", DocsURL(), "https://wloophq.github.io/wloop/"},
	} {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, c.got, c.want)
		}
	}
}

func TestDerivedNamesFollowName(t *testing.T) {
	oldName, oldOrg := Name, Org
	t.Cleanup(func() { Name, Org = oldName, oldOrg })
	Name, Org = "zzbrand", "zzbrandhq"
	for _, c := range []struct{ name, got, want string }{
		{"MCPServer", MCPServer(), "zzbrand"},
		{"ToolPrefix", ToolPrefix(), "mcp__zzbrand__"},
		{"LaunchAgent", LaunchAgent(), "io.github.zzbrandhq.zzbrand"},
		{"StateDir", StateDir(), "zzbrand"},
		{"SkillName", SkillName("review"), "zzbrand-review"},
		{"DocsURL", DocsURL(), "https://zzbrandhq.github.io/zzbrand/"},
	} {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, c.got, c.want)
		}
	}
}
