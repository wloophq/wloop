package cli

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/config"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/termfmt"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

func helpOut(t *testing.T, te *testEnv, args ...string) string {
	t.Helper()
	te.out.Reset()
	te.err.Reset()
	if code := te.run(args...); code != 0 {
		t.Fatalf("%q: exit %d: %s", args, code, te.err)
	}
	return te.out.String()
}

func TestUsageNamesTheWorkspaceManager(t *testing.T) {
	t.Parallel()
	for name, want := range map[string]string{"orca": "Open the workspace of a pull request in Orca", "zed": "Open the workspace of a pull request in zed"} {
		te := newTestEnv(t, &fake.Manager{})
		te.cfg.WorkspaceManagers = []string{name}
		if got := helpOut(t, te, "help", "open"); !strings.Contains(got, want) {
			t.Errorf("%s: help open lacks %q:\n%s", name, want, got)
		}
	}
	te := newTestEnv(t, &fake.Manager{ManagerName: "orca"})
	te.LoadConfig = func() (config.Config, error) { return config.Config{}, errors.New("broken") }
	if got := helpOut(t, te, "open", "--help"); !strings.Contains(got, "Open the workspace of a pull request in Orca") {
		t.Errorf("a broken config: help open is %q", got)
	}
}

func TestEveryCommandHasItsHelp(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	topics := allTopics(te.helpGroups())
	for name := range commands() {
		if strings.HasPrefix(name, "-") || name == "help" || devCommand([]string{name}) {
			continue
		}
		if _, ok := resolveHelp(topics, []string{name}); !ok {
			t.Errorf("%s has no help", name)
		}
	}
	for _, tp := range topics {
		if len(tp.Usage) == 0 || tp.About == "" || tp.Summary == "" || len(tp.Examples) == 0 {
			t.Errorf("%s lacks a usage, description, summary or example: %+v", tp.label(), tp)
		}
		_, intent := te.loadedIntents().Registry.Get(dintent.Name(tp.Names[0]))
		if _, ok := commands()[strings.Fields(tp.Names[0])[0]]; !ok && !intent {
			t.Errorf("%s names no command", tp.label())
		}
	}
}

func TestTheHelpLinksPagesThatExist(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	userProfile(t, te, nil)
	for _, tp := range allTopics(te.helpGroups()) {
		if tp.Docs == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join("..", "..", "docs", "src", "content", "docs", tp.Docs+".md")); err != nil {
			t.Errorf("%s links %s: %v", tp.label(), tp.Docs, err)
		}
	}
}

func TestUsageListsEveryCommandByTask(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	got := helpOut(t, te, "--help")
	for _, want := range []string{"\nGET STARTED\n  init ", "\nPULL REQUESTS\n  act ", "\nWHAT WAITS ON YOU\n", "\nAUTOMATION\n", "\nPROFILE\n", "\nSLACK\n", "\nSETUP\n", "\nTROUBLESHOOTING\n",
		"  pause/resume ", "  track/untrack ", "  slack follow/unfollow ", "  app install ", "  turn show ", "  probe ", "Run '" + te.command() + " <command> --help'"} {
		if !strings.Contains(got, want) {
			t.Errorf("usage lacks %q", want)
		}
	}
	if strings.Contains(got, " [--") {
		t.Errorf("the main help lists flags:\n%s", got)
	}
	if strings.Contains(got, groupYourIntents) {
		t.Errorf("a profile without intents of its own has a %s group", groupYourIntents)
	}
	if got != helpOut(t, te, "help") || got != helpOut(t, te) {
		t.Error("help, --help and no command print different helps")
	}
}

func TestUsageSeparatesWhatAgentsAndScriptsRun(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	got := helpOut(t, te, "help")
	agents := strings.Index(got, "\nFOR AGENTS AND SCRIPTS\n")
	if agents < 0 {
		t.Fatal("usage has no section for agents and scripts")
	}
	for _, cmd := range []string{"\n  instructions ", "\n  turn end/answer ", "\n  hook ", "\n  serve ", "\n  app "} {
		if i := strings.LastIndex(got, cmd); i < agents {
			t.Errorf("%q is not under the agents' section", cmd)
		}
	}
	if i := strings.Index(got, "\n  turn show "); i < 0 || i > agents {
		t.Error("turn show is not a user command")
	}
}

func TestUsageLeavesTheRemovedAndTheDeveloperCommandsOut(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	all := helpOut(t, te, "help")
	for _, tp := range allTopics(te.helpGroups()) {
		all += helpOut(t, te, append([]string{"help"}, strings.Fields(tp.Names[0])...)...)
	}
	for _, gone := range []string{"watch", "install-timer", "hold <url>", "release <url>", "run <url>", "skip <url>", "--except", "--remove", "doctor --repo",
		"doctor --turns", "eval-instructions", "slack eval", "--why", "--no-jobs", "--mcp-port"} {
		if strings.Contains(all, gone) {
			t.Errorf("the help names %q", gone)
		}
	}
}

func TestACommandsHelpComesFromEitherSpelling(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"plan", "--help"}, "  " + te.command() + " plan apply <id|url>..."},
		{[]string{"plan", "apply", "-h"}, "  " + te.command() + " plan dismiss <id|url>..."},
		{[]string{"profile", "show", "acme/api", "--help"}, "--intent <intent>"},
		{[]string{"slack", "follow", "--help"}, te.command() + " slack unfollow <permalink>"},
		{[]string{"resume", "--help"}, te.command() + " pause [<url>|<owner/repo>]..."},
		{[]string{"review", "https://github.com/acme/api/pull/7", "--help"}, "--no-brief"},
		{[]string{"profile", "--help"}, "\nCOMMANDS\n  show "},
	} {
		got := helpOut(t, te, c.args...)
		if !strings.Contains(got, c.want) {
			t.Errorf("%q lacks %q:\n%s", c.args, c.want, got)
		}
		if words := helpWords(c.args); got != helpOut(t, te, append([]string{"help"}, words...)...) {
			t.Errorf("%q differs from help %q", c.args, words)
		}
	}
}

func TestGuidanceAndTextAreNotAskingForHelp(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	for _, args := range [][]string{
		{"review", "https://github.com/acme/api/pull/7", "explain", "-h"},
		{"review", "https://github.com/acme/api/pull/7", "--", "--help"},
		{"profile", "add", "review", "--", "--help"},
	} {
		if te.asksHelp(args) {
			t.Errorf("%q asks for help", args)
		}
	}
}

func TestAnUnknownCommandSuggestsTheClosest(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	for name, want := range map[string]string{"statuss": "did you mean 'status'?", "outbx": "did you mean 'outbox'?", "prof": "did you mean 'profile'?", "zzzzzz": ""} {
		te.out.Reset()
		te.err.Reset()
		if code := te.run(name); code != 2 {
			t.Errorf("%s: exit %d", name, code)
		}
		got := te.err.String()
		if !strings.HasPrefix(got, "unknown command '"+name+"'") || !strings.Contains(got, want) || !strings.HasSuffix(got, "Run '"+te.command()+" --help' for the commands.\n") {
			t.Errorf("%s: %q", name, got)
		}
		if want == "" && strings.Contains(got, "did you mean") {
			t.Errorf("%s: suggests %q", name, got)
		}
		if strings.Count(got, "\n") > 2 {
			t.Errorf("%s: more than a short error:\n%s", name, got)
		}
	}
	te.err.Reset()
	if code := te.run("help", "statuss"); code != 2 || !strings.Contains(te.err.String(), "did you mean 'status'?") {
		t.Errorf("help statuss: exit %d: %s", code, te.err)
	}
}

func TestHelpWrapsToTheTerminal(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	topics := allTopics(te.helpGroups())
	for _, width := range []int{40, 60, 80, 200} {
		tm := termfmt.Term{Width: width}
		limit := min(max(width, helpMinWidth), helpMaxWidth)
		for _, tp := range topics {
			for _, l := range strings.Split(renderTopicHelp(tm, tp), "\n") {
				unbreakable := strings.HasPrefix(l, "  "+te.command()+" ") || strings.HasPrefix(l, "  https://")
				if termfmt.Cells(l) > limit && !unbreakable {
					t.Errorf("%s at %d: %q is %d wide", tp.label(), width, l, termfmt.Cells(l))
				}
			}
		}
	}
	for _, l := range strings.Split(renderMainHelp(termfmt.Term{Width: 80}, te.helpGroups()), "\n") {
		if termfmt.Cells(l) > 80 {
			t.Errorf("the main help's %q is wider than 80", l)
		}
	}
}

func TestHelpStylesOnlyWhenAsked(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	if got := helpOut(t, te, "help"); strings.Contains(got, "\x1b") {
		t.Errorf("piped help has escapes: %q", got)
	}
	got := helpOut(t, te, "help", "--color", "always")
	for _, want := range []string{"\x1b[1mGET STARTED\x1b[22m", "\x1b[36minit\x1b[39m", "\x1b[2m<command>\x1b[22m"} {
		if !strings.Contains(got, want) {
			t.Errorf("colored help lacks %q", want)
		}
	}
	te.Getenv = func(k string) string {
		if k == "GH_FORCE_TTY" || k == "NO_COLOR" {
			return "1"
		}
		return ""
	}
	if got := helpOut(t, te, "status", "--help"); strings.Contains(got, "\x1b") {
		t.Errorf("NO_COLOR help has escapes: %q", got)
	}
}

func TestHelpGolden(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	golden(t, "help_main.golden", helpOut(t, te, "help"))
	golden(t, "help_main_color.golden", bracketed(helpOut(t, te, "help", "--color=always")))
	golden(t, "help_review.golden", helpOut(t, te, "review", "--help"))
	golden(t, "help_review_color.golden", bracketed(helpOut(t, te, "help", "review", "--color", "always")))
	golden(t, "help_outbox.golden", helpOut(t, te, "outbox", "approve", "--help"))
}

func TestHelpDevListsTheDeveloperTools(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	if code := te.run("help", "dev"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	for _, want := range []string{"eval-instructions [--scenario <glob>]", "slack eval export|run|score|optimize", "profile try <url> --why <file.json>", "serve [--no-jobs] [--mcp-port <n>]"} {
		if !strings.Contains(te.out.String(), want) {
			t.Errorf("help dev lacks %q", want)
		}
	}
	if code := te.run("help", "nope"); code == 0 {
		t.Fatal("help took an unknown topic")
	}
}

func TestTheBundledIntentsLeadTheGroupTheirManifestsName(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	var prs []string
	for _, g := range te.helpGroups() {
		if g.Title == "PULL REQUESTS" {
			for _, tp := range g.Topics {
				prs = append(prs, tp.label())
			}
		}
	}
	if want := []string{"act", "address", "nudge", "review", "start", "take-over", "remove"}; !slices.Equal(prs, want) {
		t.Errorf("PULL REQUESTS lists %q, want %q", prs, want)
	}
}
