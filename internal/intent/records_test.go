package intent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/domain/task"
)

// withRecord is minimal with its [record] table replaced by record.
func withRecord(record string) string {
	head, _, _ := strings.Cut(minimal, "[record]")
	return head + "[record]\n" + record
}

func loadRecordIntent(t *testing.T, record string) (*Loaded, Problems) {
	t.Helper()
	root := t.TempDir()
	writeIntent(t, root, "ask-author", withRecord(record), "Ask.\n")
	return Load(userLayer(root), bundledFS(), testBase(), nil)
}

func TestRecordKindsComeFromTheProfile(t *testing.T) {
	l, problems := Load(userLayer(t.TempDir()), bundledFS(), testBase(), nil)
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	b, ok := l.Records.Kind("brief")
	if !ok || b.Schema == nil || b.Projects["risk"] != "risk" || b.Projects["size"] != "effort" || !b.Writes(task.WriterAgent) || !b.Writes(task.WriterPlugin) {
		t.Fatalf("brief %+v %v", b, ok)
	}
	for _, name := range []string{"report", "decision"} {
		if k, ok := l.Records.Kind(name); !ok || k.Schema == nil {
			t.Errorf("%s: %+v %v", name, k, ok)
		}
	}
	if _, ok := l.Records.Kind("evidence"); ok {
		t.Fatal("a kind the profile does not declare")
	}

	root := t.TempDir()
	writeFile(t, filepath.Join(root, RecordsFile), "[records.evidence]\nschema = \"records/evidence.schema.json\"\nwriters = [\"agent\"]\n\n[records.evidence.projects]\nlevel = \"risk\"\n")
	writeFile(t, filepath.Join(root, "records", "evidence.schema.json"), `{"type":"object","properties":{"level":{"type":"string"}}}`)
	l, problems = Load(userLayer(root), bundledFS(), testBase(), nil)
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	if k, ok := l.Records.Kind("evidence"); !ok || k.Projects["level"] != "risk" || k.Schema.ValidateJSON([]byte(`{"level":1}`)) == nil {
		t.Fatalf("the user's kind %+v %v", k, ok)
	}

	for user, want := range map[string]string{
		"[records.evidence]\nwriters = [\"agent\"]\n":                                        "records.evidence.schema: is required",
		"[records.evidence]\nschema = \"records/none.json\"\n":                               "records.evidence.schema",
		"[records.brief]\nwriters = [\"robot\"]\n":                                           "records.brief.writers[0]",
		"[records.brief.projects]\nsize = \"weight\"\n":                                      "records.brief.projects.size",
		"[records.evidence]\nschema = \"records/bad.json\"\n":                                "records.evidence.schema",
		"[records.evidence]\nschema = \"records/evidence.schema.json\"\nwhy = \"because\"\n": "records.evidence.why",
	} {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, RecordsFile), user)
		writeFile(t, filepath.Join(dir, "records", "bad.json"), `{"type":"object","oneOf":[]}`)
		writeFile(t, filepath.Join(dir, "records", "evidence.schema.json"), `{"type":"object"}`)
		if _, problems := Load(userLayer(dir), bundledFS(), testBase(), nil); !strings.Contains(problems.Error(), want) {
			t.Errorf("%q: problems %q, want %q", user, problems.Error(), want)
		}
	}
}

func TestARecordMissingNamesADeclaredGate(t *testing.T) {
	_, problems := loadRecordIntent(t, "requires = \"report\"\nmissing = \"nowhere\"\n")
	if !strings.Contains(problems.Error(), `record.missing: "nowhere" names no gate of gates.toml`) {
		t.Fatalf("problems %q", problems.Error())
	}
	_, problems = loadRecordIntent(t, "requires = \"essay\"\nmissing = \"missing-record\"\n")
	if !strings.Contains(problems.Error(), `record.requires: "essay" names no record kind of records.toml`) {
		t.Fatalf("problems %q", problems.Error())
	}
	if _, problems := loadRecordIntent(t, "states = [\"done\"]\nrequires = \"report\"\nmissing = \"missing-record\"\n"); len(problems) > 0 {
		t.Fatal(problems.Error())
	}
}

func TestARecordTableWithOnlyRequiresLoads(t *testing.T) {
	l, problems := loadRecordIntent(t, "requires = \"report\"\nmissing = \"missing-record\"\n")
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	d, _ := l.Registry.Get("ask-author")
	if d.Record.Requires != "report" || d.Record.Missing != "missing-record" || d.Record.States != nil {
		t.Fatalf("record %+v", d.Record)
	}
	if _, problems := loadRecordIntent(t, "requires = \"report\"\nmissing = \"missing-record\"\noutcome = [\"url\"]\n"); !strings.Contains(problems.Error(), "record.states: is required") {
		t.Fatalf("an outcome key without states: %q", problems.Error())
	}
}

func TestRequiresWithoutMissingIsAProblem(t *testing.T) {
	_, problems := loadRecordIntent(t, "states = [\"done\"]\nrequires = \"report\"\n")
	if !strings.Contains(problems.Error(), "record.missing: is required with record.requires") {
		t.Fatalf("problems %q", problems.Error())
	}
}

func TestEveryBundledIntentThatRequiresARecordNamesItsGate(t *testing.T) {
	files, err := filepath.Glob("../../profiles/default/intents/*/intent.toml")
	if err != nil {
		t.Fatal(err)
	}
	ofPlugins, err := filepath.Glob("../../plugins/*/profile/intents/*/intent.toml")
	if err != nil || len(ofPlugins) == 0 {
		t.Fatalf("%v %v", ofPlugins, err)
	}
	for _, f := range append(files, ofPlugins...) {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "\nrequires") && !strings.Contains(string(b), "\nmissing") {
			t.Errorf("%s requires a record and names no gate", f)
		}
	}
}

func TestAnIntentWithoutARecordTableLoadsAndOwesNothing(t *testing.T) {
	head, _, _ := strings.Cut(minimal, "[record]")
	root := t.TempDir()
	writeIntent(t, root, "ask-author", head, "Ask.\n")
	l, problems := Load(userLayer(root), bundledFS(), testBase(), nil)
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	d, ok := l.Registry.Get("ask-author")
	if !ok || d.Record.Requires != "" || d.Record.Missing != "" || d.Record.States != nil || d.Records.Src != "true" || !d.Record.None {
		t.Fatalf("an intent with no [record] %+v %v", d.Record, ok)
	}
	if rules, err := l.Rules("ask-author"); err != nil || rules.Requires != "" {
		t.Fatalf("its rules %+v %v", rules, err)
	}
}
