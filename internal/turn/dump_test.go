package turn

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/brand/brandtest"
)

const scenarioDump = "testdata/scenarios.golden"

type dumpedScenario struct {
	Name, ID, Prompt, Sources, Text string
	Records                         bool
}

type scenarioDumpFile struct {
	Count     int
	Scenarios []dumpedScenario
}

func TestScenarioGoldenHoldsItsCount(t *testing.T) {
	f, err := readScenarioDump(scenarioDump)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Scenarios) != f.Count {
		t.Fatalf("%s holds %d scenarios, its header says %d", scenarioDump, len(f.Scenarios), f.Count)
	}
}

func TestScenariosRenderTheirGolden(t *testing.T) {
	f, err := readScenarioDump(scenarioDump)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Scenarios) < f.Count {
		t.Fatalf("%s holds %d scenarios, fewer than its header's %d", scenarioDump, len(f.Scenarios), f.Count)
	}
	l, err := Bundled()
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Scenario{}
	for _, s := range Scenarios() {
		byName[s.Name] = s
	}
	for _, want := range f.Scenarios {
		t.Run(want.Name, func(t *testing.T) {
			s, ok := byName[want.Name]
			if !ok {
				t.Fatalf("no scenario %s in Scenarios()", want.Name)
			}
			r, err := Render(s.Context, l, "")
			if err != nil {
				t.Fatal(err)
			}
			want.Prompt, want.Text = brandtest.Expand(want.Prompt), brandtest.Expand(want.Text)
			got := dumpedScenario{Name: s.Name, ID: r.ID, Prompt: Prompt(s.Context, r.ID), Records: r.Records, Sources: strings.Join(r.Sources, "; "), Text: r.Text}
			got.Prompt, got.Text = strings.ReplaceAll(got.Prompt, got.ID, "<id>"), strings.ReplaceAll(got.Text, got.ID, "<id>")
			want.Prompt, want.Text = strings.ReplaceAll(want.Prompt, want.ID, "<id>"), strings.ReplaceAll(want.Text, want.ID, "<id>")
			if brandtest.IsDefault() && got.ID != want.ID || got.Prompt != want.Prompt || got.Records != want.Records || got.Sources != want.Sources {
				t.Errorf("got  id %s records %t sources %q\n  prompt %s\nwant id %s records %t sources %q\n  prompt %s",
					got.ID, got.Records, got.Sources, got.Prompt, want.ID, want.Records, want.Sources, want.Prompt)
			}
			if got.Text != want.Text {
				t.Errorf("text differs:\n%s", firstDifference(want.Text, got.Text))
			}
		})
	}
}

// firstDifference shows the first line where two texts part.
func firstDifference(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < max(len(w), len(g)); i++ {
		var a, b string
		if i < len(w) {
			a = w[i]
		}
		if i < len(g) {
			b = g[i]
		}
		if a != b {
			return fmt.Sprintf("line %d\nwant %q\ngot  %q", i+1, a, b)
		}
	}
	return "same lines"
}

func readScenarioDump(path string) (scenarioDumpFile, error) {
	raw, err := os.Open(path)
	if err != nil {
		return scenarioDumpFile{}, err
	}
	defer raw.Close()
	sc := bufio.NewScanner(raw)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	n := 0
	next := func() (string, bool) {
		if !sc.Scan() {
			return "", false
		}
		n++
		return sc.Text(), true
	}
	fail := func(format string, a ...any) (scenarioDumpFile, error) {
		return scenarioDumpFile{}, fmt.Errorf("%s:%d: "+format, append([]any{path, n}, a...)...)
	}
	var f scenarioDumpFile
	l, _ := next()
	count, ok := strings.CutPrefix(l, "count: ")
	if f.Count, err = strconv.Atoi(count); !ok || err != nil {
		return fail("want count: <n>, got %q", l)
	}
	field := func(key string) (string, error) {
		l, ok := next()
		v, has := strings.CutPrefix(l, key+": ")
		if !ok || !has {
			if l == key+":" {
				return "", nil
			}
			return "", fmt.Errorf("%s:%d: want %s: <value>, got %q", path, n, key, l)
		}
		return v, nil
	}
	for {
		l, ok := next()
		if !ok {
			break
		}
		name, has := strings.CutPrefix(l, "== ")
		if !has || name == "end" {
			return fail("want == <name>, got %q", l)
		}
		s := dumpedScenario{Name: name}
		if s.ID, err = field("id"); err != nil {
			return scenarioDumpFile{}, err
		}
		if s.Prompt, err = field("prompt"); err != nil {
			return scenarioDumpFile{}, err
		}
		records, err := field("records")
		if err != nil {
			return scenarioDumpFile{}, err
		}
		if s.Records, err = strconv.ParseBool(records); err != nil {
			return fail("records: %v", err)
		}
		if s.Sources, err = field("sources"); err != nil {
			return scenarioDumpFile{}, err
		}
		var text strings.Builder
		for {
			l, ok := next()
			if !ok {
				return fail("%s: no == end", name)
			}
			if l == "== end" {
				break
			}
			text.WriteString(l + "\n")
		}
		s.Text = text.String()
		f.Scenarios = append(f.Scenarios, s)
	}
	if err := sc.Err(); err != nil {
		return scenarioDumpFile{}, err
	}
	return f, nil
}
