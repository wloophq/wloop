package cli

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/daemon"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	wsdomain "github.com/wloophq/wloop/internal/domain/workspace"
	"github.com/wloophq/wloop/internal/github"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/intent/builtin"
	"github.com/wloophq/wloop/internal/turn"
	"github.com/wloophq/wloop/internal/workspace/fake"
)

// userProfile writes the ask-author fixture, and files, into a profile
// directory te's config names.
func userProfile(t *testing.T, te *testEnv, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	all := maps.Clone(files)
	if all == nil {
		all = map[string]string{}
	}
	for _, name := range []string{"intent.toml", "instructions.md.tmpl"} {
		b, err := os.ReadFile(filepath.Join(askAuthorFixture, name))
		if err != nil {
			t.Fatal(err)
		}
		all[filepath.Join("intents", "ask-author", name)] = string(b)
	}
	for name, body := range all {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	te.cfg.Profile = dir
	return dir
}

// askAuthorFixture is absolute: some tests change the working directory.
var askAuthorFixture, _ = filepath.Abs(filepath.Join("..", "intent", "testdata", "good", "ask-author", "intents", "ask-author"))

func TestCommandLinesParse(t *testing.T) {
	t.Parallel()
	reg := turnBundled(t).Registry
	for _, c := range []struct {
		intent dintent.Name
		args   []string
		want   intentArgs
	}{
		{dintent.Review, nil, intentArgs{}},
		{dintent.Review, []string{"look at the migration first"}, intentArgs{guidance: "look at the migration first"}},
		{dintent.Review, []string{"look at", "the migration"}, intentArgs{guidance: "look at the migration"}},
		{dintent.Review, []string{"focus on", " the\nmigration"}, intentArgs{guidance: "focus on  the\nmigration"}},
		{dintent.Review, []string{"--no-wait"}, intentArgs{noWait: true}},
		{dintent.Review, strings.Fields("rename the --profile flag"), intentArgs{guidance: "rename the --profile flag"}},
		{dintent.Review, strings.Fields("-- --profile is now a config"), intentArgs{guidance: "--profile is now a config"}},
		{dintent.Review, strings.Fields("look -- --profile=x"), intentArgs{guidance: "look -- --profile=x"}},
		{dintent.Address, nil, intentArgs{}},
		{dintent.Address, []string{"--take-over", "fix", "the", "nit"}, intentArgs{inputs: map[string]any{"take_over": true}, guidance: "fix the nit"}},
		{dintent.Address, []string{"--no-brief", "skip the nit"}, intentArgs{noBrief: true, guidance: "skip the nit"}},
		{dintent.Address, []string{"--no-wait"}, intentArgs{noWait: true}},
		{dintent.Address, []string{"fix", "the test"}, intentArgs{guidance: "fix the test"}},
		{dintent.Address, []string{"skip", "the nit"}, intentArgs{guidance: "skip the nit"}},
		{dintent.Nudge, nil, intentArgs{}},
		{dintent.Nudge, []string{"be", "brief"}, intentArgs{guidance: "be brief"}},
	} {
		d, _ := reg.Get(c.intent)
		got, err := parseIntentArgs(reg, d, surfaceCLI, intentUsage(d), c.args)
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s %q: got %+v, %v; want %+v", c.intent, c.args, got, err, c.want)
		}
	}
}

func TestAFlagOfAnotherIntentIsRefusedBeforeGuidance(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		args []string
		want string
	}{
		{[]string{"review", r7.URL(), "--self-review"}, "--self-review applies to address"},
		{[]string{"nudge", r7.URL(), "--self-review"}, "--self-review applies to address"},
		{[]string{"review", r7.URL(), "--nope", "x"}, "unknown flag --nope"},
		{[]string{"nudge", r7.URL(), "--no-wait"}, "--no-wait applies to address or review"},
		{[]string{"review", r7.URL(), "--take-over", "look"}, "--take-over applies to address"},
		{[]string{"review", r7.URL(), "--no-wait=x", "look"}, "--no-wait takes no value"},
	} {
		te := newTestEnv(t, &fake.Manager{})
		te.noDaemon(t)
		if code := te.run(c.args...); code != 1 || !strings.Contains(te.err.String(), c.want) || strings.Contains(te.err.String(), brand.Command+" serve") {
			t.Errorf("%v: exit %d: %s", c.args, code, te.err)
		}
	}
}

func TestHelpListsTheRegistrysIntents(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	userProfile(t, te, nil)
	if code := te.run("help"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	out := te.out.String()
	for _, want := range []string{
		"\nPULL REQUESTS\n  act ",
		"\n  review ",
		"\n  address ",
		"\n  nudge ",
		"\n" + groupYourIntents + "\n  ask-author ",
		"Ask the author one question\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("help lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "  probe <url>") || strings.Count(out, "\n  probe ") != 1 {
		t.Errorf("help lists the probe intent as a command:\n%s", out)
	}
	te.out.Reset()
	if code := te.run("ask-author", "--help"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	for _, want := range []string{te.command() + " ask-author <url> [--topic <topic>] [guidance...]\n", "  --topic <topic>  What to ask about\n"} {
		if !strings.Contains(te.out.String(), want) {
			t.Errorf("ask-author --help lacks %q:\n%s", want, te.out)
		}
	}
	te.out.Reset()
	if code := te.run("address", "--help"); code != 0 || !strings.Contains(te.out.String(), te.command()+" address <url> [--self-review] [--take-over] [--no-wait] [--no-brief] [guidance...]\n") {
		t.Errorf("address --help: exit %d:\n%s", code, te.out)
	}
}

func TestReservedNamesCoverEveryCommand(t *testing.T) {
	t.Parallel()
	bundled := turnBundled(t).Registry
	for name := range commands() {
		if strings.HasPrefix(name, "-") {
			continue
		}
		if _, ok := bundled.Get(dintent.Name(name)); ok {
			continue
		}
		if !slices.Contains(builtin.ReservedNames, name) {
			t.Errorf("builtin.ReservedNames lacks the command %s", name)
		}
	}
}

func TestInstructionsTakesTheIntentsFlags(t *testing.T) {
	t.Parallel()
	_, te := handScenario(t, wsdomain.Workspace{ID: "ws-1", DisplayName: "REVIEW feat/x", LinkedPR: 7})
	userProfile(t, te, nil)
	inSession(te, "term_1")
	if code := te.run("instructions", "review", r7.URL(), "--topic", "x"); code == 0 || !strings.Contains(te.err.String(), "--topic applies to ask-author") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	te.err.Reset()
	if code := te.run("instructions", "ask-author", r7.URL(), "--topic", "the API", "--", "be", "kind"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	if out := te.out.String(); !strings.Contains(out, "About: the API") {
		t.Fatalf("out:\n%s", out)
	}
	b := bookOf(t, te, r7)
	if len(b.Turns) != 1 || b.Turns[0].Intent != "ask-author" || b.Turns[0].Inputs["topic"] != "the API" || b.Turns[0].Guidance != "be kind" {
		t.Fatalf("turns %+v", b.Turns)
	}
}

func TestInstructionsRefusesFlagsWithTheSpecTexts(t *testing.T) {
	t.Parallel()
	_, te := handScenario(t, wsdomain.Workspace{ID: "ws-1", DisplayName: "REVIEW feat/x", LinkedPR: 7})
	userProfile(t, te, nil)
	inSession(te, "term_1")
	for _, tc := range []struct{ intent, flag, want string }{
		{"review", "--nope", "unknown flag --nope"},
		{"nudge", "--no-wait", "--no-wait applies to"},
	} {
		te.err.Reset()
		code := te.run("instructions", tc.intent, r7.URL(), tc.flag)
		got := te.err.String()
		if code == 0 || !strings.Contains(got, tc.want) || strings.Contains(got, "flag provided but not defined") {
			t.Errorf("%s %s: exit %d: %s", tc.intent, tc.flag, code, got)
		}
	}
}

func TestARoundTakesNoInputs(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	if code := te.run("instructions", "address", "--turn", "abc123abc123", "--take-over", r7.URL()); code == 0 || !strings.Contains(te.err.String(), "--turn prints a stored turn; it takes no --take-over and no guidance") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
}

func TestARoundReadsAnyRegistryIntent(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	agentDaemon(t, te)
	userProfile(t, te, nil)
	for _, intent := range []dintent.Name{dintent.Probe, "ask-author"} {
		te.out.Reset()
		sent, err := te.sendTurn(context.Background(), te.cfg, turn.Request{Intent: turn.Intent(intent), Unit: r7.Unit(), Payload: &turn.PR{}})
		if err != nil {
			t.Fatal(err)
		}
		if code := te.run("instructions", string(intent), "--turn", sent.ID, r7.URL()); code != 0 {
			t.Fatalf("%s: exit %d: %s", intent, code, te.err)
		}
		if !strings.HasPrefix(te.out.String(), "Instructions "+sent.ID) {
			t.Fatalf("%s: %s", intent, te.out)
		}
	}
	if code := te.run("instructions", "probe", r7.URL()); code == 0 || !strings.Contains(te.err.String(), `unknown intent "probe"`) {
		t.Fatalf("a probe by hand: exit %d: %s", code, te.err)
	}
}

func TestRoundPolicyAllowsEveryIntentsStoredTurn(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.Executable = "/Apps/" + brand.Name
	userProfile(t, te, nil)
	raw, err := te.agentCommand(context.Background(), te.cfg, "/wt", roundOptions{trusted: true})
	if err != nil {
		t.Fatal(err)
	}
	raw = strings.ReplaceAll(raw, `'\''`, "'")
	for _, intent := range []string{"address", "ask-author", "nudge", "review"} {
		if want := "Bash('/Apps/" + brand.Name + "' instructions " + intent + " --turn *)"; !strings.Contains(raw, want) {
			t.Errorf("lacks %s", want)
		}
	}
	if strings.Contains(raw, "instructions probe") {
		t.Errorf("a round reads probe turns: %s", raw)
	}
}

func TestAReviewSessionLoadsProjectSettingsOnlyWhereTheConfigSays(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.cfg.Intents = map[string]config.IntentConfig{"review": {ProjectSettings: &config.ProjectSettings{When: `pr.visibility == "private"`}}}
	pr := func(visibility string) *github.PullRequest {
		p := &github.PullRequest{}
		p.Ref, p.Visibility = r7, visibility
		return p
	}
	for _, c := range []struct {
		intent dintent.Name
		pr     *github.PullRequest
		off    bool
	}{
		{dintent.Review, pr("private"), false},
		{dintent.Review, pr("public"), true},
		{dintent.Review, nil, true},
		{dintent.Address, pr("public"), false},
	} {
		raw, err := te.agentCommand(context.Background(), te.cfg, "/wt", roundOptions{trusted: true, intent: c.intent, pr: c.pr})
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(raw, "--setting-sources user"); got != c.off {
			t.Errorf("%s %+v: user settings only %v, want %v: %s", c.intent, c.pr, got, c.off, raw)
		}
	}
}

func TestEvalInstructionsFiltersScenarios(t *testing.T) {
	t.Parallel()
	names := func(args ...string) []string {
		t.Helper()
		got, err := evalScenarios(args)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		var out []string
		for _, s := range got {
			out = append(out, s.Name)
		}
		return out
	}
	if all := names(); len(all) != len(turn.Scenarios()) {
		t.Fatalf("no glob ran %d of %d", len(all), len(turn.Scenarios()))
	}
	for _, n := range names("--scenario", "nudge/*") {
		if !strings.HasPrefix(n, "nudge/") {
			t.Errorf("nudge/* ran %s", n)
		}
	}
	both := names("--scenario", "nudge/*", "--scenario", "review/first/*")
	if !slices.ContainsFunc(both, func(n string) bool { return strings.HasPrefix(n, "review/first/") }) || !slices.ContainsFunc(both, func(n string) bool { return strings.HasPrefix(n, "nudge/") }) {
		t.Errorf("two globs ran %v", both)
	}
	if _, err := evalScenarios([]string{"--scenario", "nothing/*"}); err == nil {
		t.Error("a glob matching nothing ran")
	}
	if _, err := evalScenarios([]string{"--scenario", "[x"}); err == nil {
		t.Error("a bad glob ran")
	}
}

func TestABrokenUserIntentsCommandPrintsItsProblems(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	userProfile(t, te, map[string]string{
		"intents/ask-later/intent.toml":          "label = \"Ask later\"\nnope = 1\n",
		"intents/ask-later/instructions.md.tmpl": "{{.Headline}}\n",
	})
	if code := te.run("ask-later", r7.URL()); code != 1 || !strings.Contains(te.err.String(), "the intent ask-later has problems:") || !strings.Contains(te.err.String(), "intent.toml") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	te.err.Reset()
	if code := te.run("help"); code != 0 || !strings.Contains(te.out.String(), "  ask-later ") || !strings.Contains(te.out.String(), "  ask-author ") {
		t.Fatalf("help: exit %d: %s", code, te.out)
	}
}

func TestTheNotOwnedErrorCarriesTheRequestsFlags(t *testing.T) {
	t.Parallel()
	m := &fake.Manager{RepoList: []wsdomain.Repo{{ID: "r1", Path: "/src/api", RemoteKey: "github.com/acme/api"}}}
	te := newTestEnv(t, m)
	te.gh.basics = addressBasics(t, obj{"__typename": "User", "login": "jo"})
	if code := te.run("address", "https://github.com/acme/api/pull/21", "--self-review", "fix", "it"); code != 1 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	want := "Not yours: take it over first; run '" + brand.Command + " address --take-over --self-review https://github.com/acme/api/pull/21'"
	if !strings.Contains(te.err.String(), want) {
		t.Fatalf("err: %s", te.err)
	}
	if len(m.Writes) != 0 || len(m.Prompts) != 0 {
		t.Fatalf("writes %v prompts %+v", m.Writes, m.Prompts)
	}
}

func TestEveryBuiltinNameHasAnImplementation(t *testing.T) {
	t.Parallel()
	ours, theirs := cliBuiltins(), daemon.Builtins()
	for slot, names := range builtin.Names {
		for _, name := range names {
			if !slices.Contains(ours[string(slot)], name) {
				t.Errorf("%s %q has no implementation", slot, name)
			}
		}
		for _, name := range ours[string(slot)] {
			if !slices.Contains(names, name) {
				t.Errorf("%s %q is implemented but not in builtin.Names", slot, name)
			}
		}
	}
	effects := append(slices.Clone(ours["effects"]), theirs.Effects...)
	for _, name := range builtin.TurnEffectNames() {
		if !slices.Contains(effects, name) {
			t.Errorf("the turn effect %q has no implementation", name)
		}
	}
	for _, name := range effects {
		if !slices.Contains(builtin.TurnEffectNames(), name) {
			t.Errorf("the turn effect %q is implemented but not in builtin.Effects", name)
		}
	}
	for _, r := range builtin.Roles {
		for what, name := range map[string]string{"policy": r.Policy, "context": r.Context, "launch": r.Launch, "reconcile": r.Reconcile} {
			if !slices.Contains(ours[what], name) {
				t.Errorf("the role %s binds the %s %q, which has no implementation", r.Name, what, name)
			}
		}
		if r.Prepare != "" && !strings.HasPrefix(r.Prepare, builtin.PrepareAction) {
			t.Errorf("the role %s binds the prepare %q, which names no plugin action", r.Name, r.Prepare)
		}
	}
}

func turnBundled(t *testing.T) *intentload.Loaded {
	t.Helper()
	l, err := turn.Bundled()
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestProfileSchemaPrintsTheManifestSchema(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.noDaemon(t)
	if code := te.run("profile", "schema"); code != 0 || te.out.String() != string(intentload.Schema) {
		t.Fatalf("exit %d: %s%s", code, te.out, te.err)
	}
}

func TestDoctorLoadsTheIntents(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	if got := te.intentChecks(); len(got) != 1 || got[0].mark != markOK || got[0].info != "act address nudge review start" {
		t.Fatalf("checks %+v", got)
	}
	userProfile(t, te, map[string]string{
		"intents/ask-later/intent.toml":          "label = \"Ask later\"\nnope = 1\n",
		"intents/ask-later/instructions.md.tmpl": "{{.Headline}}\n",
	})
	got := te.intentChecks()
	if len(got) != 1 || got[0].mark != markMissing || got[0].name != "intent ask-later" || !strings.Contains(got[0].info, "nope") {
		t.Fatalf("checks %+v", got)
	}
}

func TestDoctorNamesARevertedOverride(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	dir := userProfile(t, te, map[string]string{"intents/nudge/instructions.md.tmpl": "{{\n"})
	got := te.intentChecks()
	if len(got) != 1 || got[0].mark != markMissing || got[0].name != "intent nudge" || !strings.Contains(got[0].info, filepath.Join(dir, "intents", "nudge", "instructions.md.tmpl")) {
		t.Fatalf("checks %+v", got)
	}
}

func TestASessionRunsUnderItsManifestsPolicy(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	te.Executable = "/Apps/" + brand.Name
	opts := intentOptions(dintent.Def{Name: "ask-author", Policy: "probe"}, roundOptions{trusted: true})
	raw, err := te.agentCommand(context.Background(), te.cfg, "/wt", opts)
	if err != nil {
		t.Fatal(err)
	}
	raw = strings.ReplaceAll(raw, `'\''`, "'")
	if !strings.Contains(raw, "Bash('/Apps/"+brand.Name+"' instructions ask-author --turn *)") || strings.Contains(raw, "instructions review --turn") || !strings.Contains(raw, "Bash(gh *)") {
		t.Fatalf("not the probe policy: %s", raw)
	}
}

func TestAddressByHandSelfReviewRendersTheOwnReview(t *testing.T) {
	t.Parallel()
	_, te := handScenario(t, wsdomain.Workspace{ID: "ws-5", DisplayName: "feat-x"})
	te.gh.basics = addressBasics(t, obj{"__typename": "User", "login": "me"})
	if code := te.run("instructions", "address", "https://github.com/acme/api/pull/21", "--self-review", "--", "plan", "text"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	out := te.out.String()
	for _, want := range []string{"#### Your own review of the change", "plan text", "## Review judgment"} {
		if !strings.Contains(out, want) {
			t.Fatalf("no %q in:\n%s", want, out)
		}
	}
}

func TestSelfReviewAppliesOnlyToAddress(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"instructions", "review", r7.URL(), "--self-review"},
		{"review", r7.URL(), "--self-review"},
		{"nudge", r7.URL(), "--self-review"},
	} {
		te := newTestEnv(t, &fake.Manager{})
		te.noDaemon(t)
		if code := te.run(args...); code == 0 || !strings.Contains(te.err.String(), "--self-review applies to address") {
			t.Errorf("%v: exit %d: %s", args, code, te.err)
		}
	}
}

func TestARoundTakesNoSelfReview(t *testing.T) {
	t.Parallel()
	te := newTestEnv(t, &fake.Manager{})
	if code := te.run("instructions", "address", "--turn", "abc123abc123", "--self-review"); code == 0 || !strings.Contains(te.err.String(), "it takes no --self-review") {
		t.Fatalf("exit %d: %s", code, te.err)
	}
}

func TestAddressSelfReviewThroughTheDaemon(t *testing.T) {
	t.Parallel()
	te, calls := daemonForActions(t)
	if code := te.run("address", "https://github.com/acme/api/pull/20", "--self-review", "fix", "the", "plan"); code != 0 {
		t.Fatalf("exit %d: %s", code, te.err)
	}
	var actions []string
	for _, c := range calls() {
		if strings.HasPrefix(c, "POST /api/units/") {
			actions = append(actions, c)
		}
	}
	if len(actions) != 1 || !strings.HasPrefix(actions[0], "POST /api/units/github/acme/api#20/address") || !strings.Contains(actions[0], `"self_review":true`) || !strings.Contains(actions[0], `"guidance":"fix the plan"`) {
		t.Fatalf("actions %v", actions)
	}
}
