package manifest

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wloophq/wloop/pkg/plugin"
)

const pulls = `
id = "pulls"
kinds = ["source"]
protocol = 1

[source]
name = "Pulls"
slot = "pull"
brief_subject = true
rounds = true
payload = "Pull"
row_kinds = ["to_review", "held"]
kinds = ["pull-request"]
default_delivery = "comment"
steps = ["turn.dismiss"]
config_reads = ["sweep", "brief"]
notify = ["rounds", "events"]

[source.codec]
keys = ["{owner}/{repo}#{number}"]
urls = ["https://pulls.example/{owner}/{repo}/pull/{number}"]
label = "{repo}#{number}"
fold_case = true
target = "<url>"

[source.codec.fields]
number = '[1-9][0-9]*'

[[source.facts]]
name = "pull.state"
type = "string"
doc = "The pull request's state."
enum = ["OPEN", "MERGED"]

[[source.facts]]
name = "pull.stalled_for"
type = "duration"
doc = "How long it has waited."
time_varying = true

[[source.effects]]
name = "pull.nudged"
applies = ["turn", "draft"]
doc = "Records the nudge."

[[source.events]]
name = "pull.merged"
doc = "The pull request merged."
resolves = "merged"

[[source.events]]
name = "pull.reviewed"
doc = "I reviewed it."
answers_asked_turn = true

[[source.roles]]
name = "probe"
doc = "A synthetic pull request."
launch = "probe"
prepare = "action:nudge.prepare"

[[source.deliveries]]
name = "comment"
posts = true

[[source.deliveries]]
name = "reaction"

[[source.draft_kinds]]
name = "nudge"

[[source.draft_kinds]]
name = "react"
delivery = "reaction"

[[source.signals]]
name = "stalled"
doc = "Nobody moved it past the threshold."

[[source.situations]]
name = "review"
doc = "Someone asked for my review."
rank = 2

[[source.jobs]]
name = "ingest"
every = "5m"
every_from = "sweep.fast"

[[source.commands]]
name = "status"
usage = "status"

[[source.tools]]
name = "pull_read"
doc = "Reads a pull request."
input = '{"type": "object", "properties": {"url": {"type": "string"}}}'

[[source.tools.answers]]
name = "read"
doc = "The pull request, read."
fields = [{name = "Title"}, {name = "Count", number = true}]

[[source.tools]]
name = "pull_comment"
doc = "Comments at once."
input = '{"type": "object"}'
posts = "now"

[source.ui]
web = "web"

[[source.ui.views]]
id = "my-pulls"
rows = true
surfaces = ["web", "menubar"]
unit_sort = "repo"

[[source.ui.facets]]
id = "repo"
field = "pull.repo"
match = "suffix"
menu = true

[[source.ui.menubar]]
id = "mine"
view = "my-pulls"

[[source.ui.keys]]
key = "g p"
action = "nav:my-pulls"
`

const threads = `
id = "chat"
kinds = ["source"]
protocol = 1

[source]
name = "Chat"
slot = "chat"

[source.codec]
keys = ["{team}/{conv}/{ts}", "{team}/{conv}/seg/{seg}"]
urls = ["https://{team}.chat.example/archives/{conv}/p{ts}"]
targets = '^(C|D)[A-Z0-9]+$'

[source.codec.fields]
ts = '[0-9]+\.[0-9]+'
`

func TestManifestCodecParsesKeysWithoutThePlugin(t *testing.T) {
	m, err := Parse([]byte(pulls))
	if err != nil {
		t.Fatal(err)
	}
	src, err := m.Source()
	if err != nil {
		t.Fatal(err)
	}
	if src.Provider != "pulls" || src.Slot != "pull" || len(src.Facts) != 2 || src.Jobs[0].Every != Duration(5*time.Minute) || src.Jobs[0].EveryFrom != "sweep.fast" ||
		!src.Deliveries[0].Posts || src.Deliveries[1].Posts || src.Events[0].Resolves != "merged" || !src.Events[1].AnswersAskedTurn ||
		src.Situations[0].Rank != 2 || src.Tools[0].Answers[0].Fields[1] != (AnswerField{Name: "Count", Number: true}) || src.Tools[1].Posts != "now" ||
		src.UI.Views[0].UnitSort != "repo" || src.UI.Web != "web" || src.DraftKinds[1].Delivery != "reaction" {
		t.Errorf("source %+v", src)
	}
	c, err := src.Codec.Compile()
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Valid("acme/api#12"); err != nil {
		t.Error(err)
	}
	for _, bad := range []string{"acme/api#0", "acme/api#", "acme#12", "acme/api#12x", "acme/a/b#1"} {
		if c.Valid(bad) == nil {
			t.Errorf("%q is valid", bad)
		}
	}
	if got := c.URL("acme/api#12"); got != "https://pulls.example/acme/api/pull/12" {
		t.Errorf("URL = %q", got)
	}
	if got := c.URL("nope"); got != "" {
		t.Errorf("URL of a bad key = %q", got)
	}
	for _, u := range []string{"https://pulls.example/acme/api/pull/12", "http://pulls.example/acme/api/pull/12/files", " https://pulls.example/acme/api/pull/12#top "} {
		if key, ok := c.KeyOf(u); !ok || key != "acme/api#12" {
			t.Errorf("KeyOf(%q) = %q, %v", u, key, ok)
		}
	}
	if _, ok := c.KeyOf("https://pulls.example/acme/api/issues/12"); ok {
		t.Error("an issue address names a key")
	}
	if got := c.Label("acme/api#12"); got != "api#12" {
		t.Errorf("Label = %q", got)
	}
	if !c.Equal("Acme/API#12", "acme/api#12") {
		t.Error("fold_case keys differ")
	}

	m, err = Parse([]byte(threads))
	if err != nil {
		t.Fatal(err)
	}
	src, err = m.Source()
	if err != nil {
		t.Fatal(err)
	}
	c, err = src.Codec.Compile()
	if err != nil {
		t.Fatal(err)
	}
	if c.Valid("T1/C2/seg/9") != nil || c.Valid("T1/C2/1700000000.000100") != nil || c.Valid("T1/C2/abc") == nil {
		t.Error("two key shapes do not both validate")
	}
	if key, ok := c.KeyOf("https://T1.chat.example/archives/C2/p1700000000.000100"); !ok || key != "T1/C2/1700000000.000100" {
		t.Errorf("KeyOf = %q, %v", key, ok)
	}
	if c.URL("T1/C2/seg/9") != "" {
		t.Error("a key whose template no address has has an address")
	}
	if c.Label("T1/C2/seg/9") != "T1/C2/seg/9" || c.Equal("T1/c2/1.1", "T1/C2/1.1") {
		t.Error("label or case")
	}
	if !c.Takes("C123") || c.Takes("acme/api#1") {
		t.Error("targets")
	}
}

func TestSourceTableRefuses(t *testing.T) {
	head := "id = \"acme\"\nkinds = [\"source\"]\nprotocol = 1\n[source]\nname = \"Acme\"\nslot = \"acme\"\ncodec = {keys = [\"{id}\"]}\n"
	for _, c := range []struct{ name, extra, want string }{
		{"a fact of another slot", "[[source.facts]]\nname = \"pr.x\"\ntype = \"bool\"\ndoc = \"d\"\n", "acme.<name>"},
		{"a fact type", "[[source.facts]]\nname = \"acme.x\"\ntype = \"float\"\ndoc = \"d\"\n", "float"},
		{"an unknown key", "[[source.facts]]\nname = \"acme.x\"\ntype = \"bool\"\ndoc = \"d\"\nlabel = \"x\"\n", "label"},
		{"a repeated fact", "[[source.facts]]\nname = \"acme.x\"\ntype = \"bool\"\ndoc = \"d\"\n[[source.facts]]\nname = \"acme.x\"\ntype = \"bool\"\ndoc = \"d\"\n", "twice"},
		{"an effect applying to nothing", "[[source.effects]]\nname = \"acme.x\"\ndoc = \"d\"\n", "applies"},
		{"a tool schema", "[[source.tools]]\nname = \"acme_read\"\ninput = \"[\"\n", "JSON"},
		{"a job without an interval", "[[source.jobs]]\nname = \"ingest\"\n", "every"},
		{"an interval key not read", "[[source.jobs]]\nname = \"ingest\"\nevery = \"1m\"\nevery_from = \"sweep.fast\"\n", "config_reads"},
		{"a topic", "notify = [\"weather\"]\n", "weather"},
		{"a default delivery not declared", "default_delivery = \"comment\"\n", "default_delivery"},
		{"a draft kind without a delivery", "[[source.draft_kinds]]\nname = \"nudge\"\n", "no delivery"},
		{"a tool posting later", "[[source.tools]]\nname = \"acme_post\"\ndoc = \"d\"\ninput = \"{}\"\nposts = \"later\"\n", "later"},
		{"an event resolving nothing", "[[source.events]]\nname = \"acme.done\"\ndoc = \"d\"\nresolves = \"done\"\n", "resolves"},
		{"a menu of no view", "[[source.ui.menubar]]\nid = \"m\"\nview = \"nope\"\n", "nope"},
		{"a web directory outside", "[source.ui]\nweb = \"../x\"\n", "web"},
		{"buckets on a text facet", "[[source.ui.facets]]\nid = \"opened\"\nsort = \"text\"\nbuckets = [{id = \"day\", within = \"24h\"}]\n", "sort = time"},
		{"a bucket with no age", "[[source.ui.facets]]\nid = \"opened\"\nsort = \"time\"\nbuckets = [{id = \"day\"}]\n", "positive within"},
		{"mixed ranks", "[[source.situations]]\nname = \"review\"\ndoc = \"d\"\nrank = 2\n[[source.situations]]\nname = \"merge\"\ndoc = \"d\"\n", "all or none"},
	} {
		t.Run(c.name, func(t *testing.T) {
			m, err := Parse([]byte(head + c.extra))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := m.Source(); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("Source = %v, want an error naming %q", err, c.want)
			}
		})
	}
	for _, c := range []struct {
		name  string
		codec Codec
		want  string
	}{
		{"no key", Codec{}, "keys"},
		{"a url whose fields no key has", Codec{Keys: []string{"{a}"}, URLs: []string{"https://x.example/{b}"}}, "no key template"},
		{"a url without https", Codec{Keys: []string{"{a}"}, URLs: []string{"x.example/{a}"}}, "https"},
		{"a stray brace", Codec{Keys: []string{"{a}}"}}, "brace"},
		{"a bad field pattern", Codec{Keys: []string{"{a}"}, Fields: map[string]string{"a": "("}}, "field"},
		{"a field no key uses", Codec{Keys: []string{"{a}"}, Fields: map[string]string{"b": "x"}}, "no key template"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if _, err := c.codec.Compile(); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("Compile = %v, want an error naming %q", err, c.want)
			}
		})
	}
}

func TestTheNotifyEnumIsThePluginTopics(t *testing.T) {
	f, ok := reflect.TypeFor[Source]().FieldByName("Notify")
	if !ok {
		t.Fatal("Source has no Notify")
	}
	var topics []string
	for _, tp := range plugin.Topics {
		topics = append(topics, string(tp))
	}
	if got := strings.Split(f.Tag.Get("enum"), ","); !slices.Equal(got, topics) {
		t.Errorf("notify's enum %v, plugin.Topics %v", got, topics)
	}
}
