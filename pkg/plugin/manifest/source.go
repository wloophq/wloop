package manifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/wloophq/wloop/pkg/plugin"
)

// SourceKind is the kind whose table [Source] decodes.
const SourceKind plugin.Kind = "source"

// Source is the [source] table of a source plugin's manifest.
type Source struct {
	// Name is how a person reads the plugin's service.
	Name  string `toml:"name" doc:"How a person reads the plugin's service, such as \"GitHub\". Required."`
	Codec Codec  `toml:"codec" doc:"How the plugin's unit keys and addresses are spelled. Required."`
	SourceVocabulary
	Jobs        []Job     `toml:"jobs" doc:"The jobs the host runs on the plugin through Ingest, by name."`
	ConfigReads []string  `toml:"config_reads" doc:"The configuration keys outside its own table the plugin reads, such as \"sweep\" or \"items.heat\"; Config returns them, checked against the configuration's keys."`
	Notify      []string  `toml:"notify" doc:"The host topics whose changes the host notifies the plugin of." enum:"rounds,gates,marks,tasks,turns,sessions,overrides,viewer,config,events,workspaces,agents"`
	UI          *UI       `toml:"ui" doc:"What clients show of the plugin; every label lives in its profile layer."`
	Commands    []Command `toml:"commands" doc:"The commands the plugin binary serves in CLI mode, which run without the daemon."`
	Tools       []Tool    `toml:"tools" doc:"The MCP tools the plugin serves through CallTool."`
}

// SourceVocabulary is what a source plugin adds to the host's tables before
// the profile loads.
type SourceVocabulary struct {
	// Provider is the plugin's id, the provider of its units; Source sets it
	// from the manifest.
	Provider string `toml:"-"`

	Slot            string      `toml:"slot" doc:"The workflow fact slot of the plugin's units, such as \"pr\"; every fact name starts with it. Required."`
	BriefSubject    bool        `toml:"brief_subject" doc:"The plugin's units may have a brief, whose facts their workflows read."`
	Rounds          bool        `toml:"rounds" doc:"The plugin's units count automatic turns per unit, against turns.per_unit_per_day."`
	Payload         string      `toml:"payload" doc:"The template root of the plugin's turn payload, such as \"PR\", which only intents of the plugin read."`
	Facts           []Fact      `toml:"facts" doc:"The workflow facts the plugin reports."`
	Effects         []Effect    `toml:"effects" doc:"The turn and draft effects the plugin applies through ApplyEffect."`
	Events          []Event     `toml:"events" doc:"The event kinds only the plugin's units record."`
	Roles           []Role      `toml:"roles" doc:"The roles an intent of the plugin may take, each naming host mechanisms."`
	RowKinds        []string    `toml:"row_kinds" doc:"The values of row.kind on the plugin's rows."`
	Deliveries      []Delivery  `toml:"deliveries" doc:"How the plugin posts drafts."`
	DraftKinds      []DraftKind `toml:"draft_kinds" doc:"The kinds of the plugin's drafts; the host derives a draft's delivery from its kind."`
	DefaultDelivery string      `toml:"default_delivery" doc:"The delivery of a draft whose kind names none; one of deliveries."`
	Signals         []Named     `toml:"signals" doc:"The link signals the plugin reports, read as task.link_signal.<name>."`
	Marks           []Named     `toml:"marks" doc:"The marks the plugin's rounds write that a workflow may read with handled(); a name has no slash, space or control character."`
	Kinds           []string    `toml:"kinds" doc:"The task kind values the plugin's units give their tasks."`
	Situations      []Situation `toml:"situations" doc:"The situations the plugin derives, in the order the cockpit ranks them."`
	Steps           []string    `toml:"steps" doc:"The core workflow steps that run on the plugin's units, such as \"turn.dismiss\"."`
}

// Codec is how a source plugin's unit keys and web addresses are spelled.
// Templates name fields in braces; a field matches its pattern in Fields,
// or a run of characters other than "/", "?", "#" and white space.
type Codec struct {
	Keys      []string          `toml:"keys" doc:"The templates of a unit key, such as \"{owner}/{repo}#{number}\"; a key is valid when it matches one. Required."`
	URLs      []string          `toml:"urls" doc:"The templates of a unit's web address; the first whose fields a key has is its address, and an address matching one names the key whose template has the same fields."`
	Label     string            `toml:"label" doc:"The template of the unit as a person reads it; empty, or a field the key lacks, reads the key."`
	Fields    map[string]string `toml:"fields" doc:"The pattern of a field, by name, as a Go regular expression."`
	FoldCase  bool              `toml:"fold_case" doc:"Two keys that differ only in case name the same unit."`
	Target    string            `toml:"target" doc:"How a command line names a unit, such as \"<url>\"."`
	TargetsRe string            `toml:"targets" doc:"The pattern a draft target this plugin posts to matches, as a Go regular expression; empty takes none."`
}

// Fact is a workflow fact a source plugin reports.
type Fact struct {
	Name        string   `toml:"name" doc:"The fact's name, <slot>.<name>. Required."`
	Type        string   `toml:"type" doc:"The fact's type. Required." enum:"bool,int,string,list,duration,timestamp"`
	Doc         string   `toml:"doc" doc:"What the fact says, for the reference pages. Required."`
	Slots       []string `toml:"slots" doc:"Where the fact is read besides the workflows." enum:"action,turn,name"`
	Dim         bool     `toml:"dim" doc:"The fact is a profile dimension."`
	Enum        []string `toml:"enum" doc:"The values the fact takes; empty takes any."`
	Untrusted   bool     `toml:"untrusted" doc:"A third party's text sets the fact, so no approval rule reads it."`
	TimeVarying bool     `toml:"time_varying" doc:"The fact changes with time alone: the plugin reports it as a temporal value."`
}

// Effect is a turn or draft effect a source plugin applies.
type Effect struct {
	Name    string   `toml:"name" doc:"The effect's name, <slot>.<name>. Required."`
	Applies []string `toml:"applies" doc:"What may carry it. Required." enum:"turn,draft"`
	Doc     string   `toml:"doc" doc:"What the effect does. Required."`
}

// Event is an event kind only a plugin's units record.
type Event struct {
	Name             string `toml:"name" doc:"The event kind. Required."`
	Doc              string `toml:"doc" doc:"What happened. Required."`
	Resolves         string `toml:"resolves" doc:"The link event the host's resolver reads it as." enum:"review,merged,closed"`
	AnswersAskedTurn bool   `toml:"answers_asked_turn" doc:"The event answers the turn that asked for it, which my_review reconciles with."`
	// Verdicts are the record verdicts of an answering event's states.
	Verdicts map[string]string `toml:"verdicts" doc:"The record verdict of each state an answering event's data starts with (APPROVE, REQUEST_CHANGES or COMMENT); a state it lacks answers nothing."`
}

// Situation is a situation a plugin derives.
type Situation struct {
	Name string `toml:"name" doc:"The situation's kind. Required."`
	Doc  string `toml:"doc" doc:"What the unit waits on. Required."`
	Rank int    `toml:"rank" doc:"Its order among the plugin's situations, lowest first; 0 is its place among the declarations."`
}

// DraftKind is a kind of a plugin's drafts.
type DraftKind struct {
	Name     string `toml:"name" doc:"The kind's name, such as \"nudge\". Required."`
	Purpose  string `toml:"purpose" doc:"What a draft of the kind is for; empty is its name."`
	Delivery string `toml:"delivery" doc:"How it is posted, one of deliveries; empty is default_delivery."`
}

// Named is a name the plugin declares, with what it means.
type Named struct {
	Name string `toml:"name" doc:"The name. Required."`
	Doc  string `toml:"doc" doc:"What it means. Required."`
}

// Role binds an intent to host mechanisms, each by its host name.
type Role struct {
	Name      string `toml:"name" doc:"The role's name. Required."`
	Doc       string `toml:"doc" doc:"What the role's turns do. Required."`
	RoundKind string `toml:"round_kind" doc:"The sweep round its turns are, such as \"review\"."`
	Reconcile string `toml:"reconcile" doc:"The host reconcile its turns run, such as \"my_review\"."`
	Prepare   string `toml:"prepare" doc:"The host prepare its turns run, such as \"action:nudge.prepare\"."`
	Launch    string `toml:"launch" doc:"The host launch its turns run, such as \"probe\"."`
	Workspace string `toml:"workspace" doc:"The workspace builtin its turns run in; empty is any."`
	Context   string `toml:"context" doc:"What its turns are about."`
	Policy    string `toml:"policy" doc:"The agent policy of its sessions."`
	DrivesRow bool   `toml:"drives_row" doc:"Its turns drive the row of the workspace they run in."`
}

// Delivery is how a source plugin posts a draft.
type Delivery struct {
	Name  string `toml:"name" doc:"The delivery's name, such as \"comment\". Required."`
	Posts bool   `toml:"posts" doc:"A draft with this delivery reaches people, so it waits for approval."`
}

// Job is a job the host schedules and runs through Ingest.
type Job struct {
	Name      string   `toml:"name" doc:"The job's name, which Ingest receives. Required."`
	Every     Duration `toml:"every" doc:"How often the job runs, as a Go duration, unless every_from is set. Required."`
	EveryFrom string   `toml:"every_from" doc:"A duration key of the configuration the interval comes from, such as \"sweep.fast\", one of config_reads or under them; every is its default."`
	Timeout   Duration `toml:"timeout" doc:"A run's limit, as a Go duration; empty is the host's default."`
}

// Command is a command of the plugin binary in CLI mode.
type Command struct {
	Name  string `toml:"name" doc:"The command's name, after the plugin's id on the command line. Required."`
	Usage string `toml:"usage" doc:"The command's arguments, as its help shows them."`
}

// Tool is an MCP tool the plugin serves.
type Tool struct {
	Name    string   `toml:"name" doc:"The tool's name; its description lives in the profile's tools.toml. Required."`
	Doc     string   `toml:"doc" doc:"What the tool does, for the reference pages. Required."`
	Input   string   `toml:"input" doc:"The JSON schema of the tool's arguments, an object. Required."`
	Posts   string   `toml:"posts" doc:"The tool posts at once, recorded as a sent draft; empty posts nothing." enum:"now"`
	Answers []Answer `toml:"answers" doc:"The answers the tool returns, which tools.toml words."`
}

// Answer is one answer of a tool, worded by tools.toml.
type Answer struct {
	Name   string        `toml:"name" doc:"The answer's name. Required."`
	Doc    string        `toml:"doc" doc:"When the tool answers it. Required."`
	Fields []AnswerField `toml:"fields" doc:"The fields its wording reads."`
}

// AnswerField is a field an answer's wording reads.
type AnswerField struct {
	Name   string `toml:"name" doc:"The field's name, as the template reads it. Required."`
	Number bool   `toml:"number" doc:"The field is a number; otherwise text."`
}

// UI is what clients show of a source plugin. Labels and notification
// bodies live in the plugin's profile layer.
type UI struct {
	Views   []UIView  `toml:"views" doc:"The plugin's views."`
	Facets  []UIFacet `toml:"facets" doc:"The facets of the plugin's rows, whose values a snapshot reports."`
	Menubar []UIMenu  `toml:"menubar" doc:"The menu bar's sections of the plugin."`
	Keys    []UIKey   `toml:"keys" doc:"The plugin's key bindings."`
	Web     string    `toml:"web" doc:"A directory of the plugin the host serves under /plugins/<id>/, with no call to the plugin."`
}

// UIView is a view of a plugin.
type UIView struct {
	ID       string   `toml:"id" doc:"The view's id, unique across plugins. Required."`
	Rows     bool     `toml:"rows" doc:"A list of rows filtered by a snapshot's views; otherwise a custom view served by View."`
	Order    int      `toml:"order" doc:"Its order among the views."`
	Surfaces []string `toml:"surfaces" doc:"Where it shows." enum:"web,tui,menubar"`
	UnitSort string   `toml:"unit_sort" doc:"The facet the unit column sorts by."`
}

// UIFacet is a facet of a plugin's rows.
type UIFacet struct {
	ID    string   `toml:"id" doc:"The facet's id. Required."`
	Field string   `toml:"field" doc:"What it reads: row.*, item.* or <provider>.*."`
	Group bool     `toml:"group" doc:"Rows may be grouped by it."`
	Sort  string   `toml:"sort" doc:"How its values sort." enum:"rank,text,time"`
	Rank  []string `toml:"rank" doc:"Its values in order, with sort = rank."`
	// Buckets group a time facet's values by age; each bucket's label lives
	// in the profile layer.
	Buckets []UIBucket `toml:"buckets" doc:"The age buckets of a time facet, youngest first; only with sort = time."`
	Match   string     `toml:"match" doc:"How a filter matches a value; empty is exact." enum:"exact,suffix,prefix,team"`
	Menu    bool       `toml:"menu" doc:"It shows in the filter menu."`
}

// UIBucket is an age bucket of a time facet: the values younger than
// Within, the first bucket that covers one winning. A negative Within reads
// a value in the future; the last bucket may leave it unset to catch the
// rest.
type UIBucket struct {
	ID     string   `toml:"id" doc:"The bucket's id. Required."`
	Within Duration `toml:"within" doc:"The age its values are younger than, as a Go duration; negative for a value that far in the future. Required but on the last bucket, which then catches the rest."`
}

// UIMenu is a menu bar section showing a view.
type UIMenu struct {
	ID   string `toml:"id" doc:"The section's id. Required."`
	View string `toml:"view" doc:"The view it shows. Required."`
}

// UIKey binds a key to an action.
type UIKey struct {
	Key     string `toml:"key" doc:"The key. Required."`
	Action  string `toml:"action" doc:"The action it runs. Required."`
	Surface string `toml:"surface" doc:"Where it binds; empty is every surface." enum:"web,tui,menubar"`
}

// Duration is a Go duration spelled as text, such as "5m".
type Duration time.Duration

func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

func (d Duration) MarshalText() ([]byte, error) { return []byte(time.Duration(d).String()), nil }

var (
	slotRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	nameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]*(\.[a-z][a-z0-9_-]*)*$`)
	// bucketRe also takes an age such as 1d.
	bucketRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
)

// FactTypes are the types a fact declares.
var FactTypes = []string{"bool", "int", "string", "list", "duration", "timestamp"}

// Source decodes and validates m's [source] table, and compiles its codec.
func (m *Manifest) Source() (*Source, error) {
	var s Source
	if err := m.Decode(SourceKind, &s); err != nil {
		return nil, err
	}
	s.Provider = m.ID
	if err := s.validate(); err != nil {
		return nil, fmt.Errorf("%s of %s, [source]: %w", File, m.ID, err)
	}
	return &s, nil
}

func (s *Source) validate() error {
	var errs []error
	bad := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }
	if s.Name == "" {
		bad("name is required")
	}
	if _, err := s.Codec.Compile(); err != nil {
		errs = append(errs, fmt.Errorf("codec: %w", err))
	}
	if !slotRe.MatchString(s.Slot) {
		bad("slot %q is not a lowercase name", s.Slot)
	}
	own := func(what, name string) {
		if !nameRe.MatchString(name) || len(name) <= len(s.Slot)+1 || name[:len(s.Slot)+1] != s.Slot+"." {
			bad("%s %q is not named %s.<name>", what, name, s.Slot)
		}
	}
	var seen []string
	unique := func(what, name string) {
		key := what + " " + name
		if slices.Contains(seen, key) {
			bad("%s %q is declared twice", what, name)
		}
		seen = append(seen, key)
	}
	for _, f := range s.Facts {
		own("fact", f.Name)
		unique("fact", f.Name)
		if !slices.Contains(FactTypes, f.Type) {
			bad("fact %q: type %q is not one of %v", f.Name, f.Type, FactTypes)
		}
		if f.Doc == "" {
			bad("fact %q: doc is required", f.Name)
		}
		for _, sl := range f.Slots {
			if !slices.Contains([]string{"action", "turn", "name"}, sl) {
				bad("fact %q: slot %q is not action, turn or name", f.Name, sl)
			}
		}
	}
	for _, e := range s.Effects {
		own("effect", e.Name)
		unique("effect", e.Name)
		if len(e.Applies) == 0 {
			bad("effect %q: applies is required", e.Name)
		}
		for _, a := range e.Applies {
			if a != "turn" && a != "draft" {
				bad("effect %q: applies %q is not turn or draft", e.Name, a)
			}
		}
		if e.Doc == "" {
			bad("effect %q: doc is required", e.Name)
		}
	}
	for _, m := range s.Marks {
		unique("mark", m.Name)
		if m.Doc == "" {
			bad("mark %q: doc is required", m.Name)
		}
	}
	named := func(what string, ns []Named) {
		for _, n := range ns {
			if !nameRe.MatchString(n.Name) {
				bad("%s %q is not a lowercase name", what, n.Name)
			}
			unique(what, n.Name)
			if n.Doc == "" {
				bad("%s %q: doc is required", what, n.Name)
			}
		}
	}
	for _, e := range s.Events {
		named("event", []Named{{e.Name, e.Doc}})
		if e.Resolves != "" && !slices.Contains([]string{"review", "merged", "closed"}, e.Resolves) {
			bad("event %q: resolves %q is not review, merged or closed", e.Name, e.Resolves)
		}
	}
	named("signal", s.Signals)
	ranked := 0
	for _, sit := range s.Situations {
		if sit.Rank != 0 {
			ranked++
		}
	}
	if ranked > 0 && ranked < len(s.Situations) {
		bad("situations set rank on %d of %d; set it on all or none", ranked, len(s.Situations))
	}
	for _, sit := range s.Situations {
		named("situation", []Named{{sit.Name, sit.Doc}})
	}
	var deliveries []string
	for _, d := range s.Deliveries {
		deliveries = append(deliveries, d.Name)
	}
	if s.DefaultDelivery != "" && !slices.Contains(deliveries, s.DefaultDelivery) {
		bad("default_delivery %q is not one of deliveries", s.DefaultDelivery)
	}
	for _, k := range s.DraftKinds {
		if !nameRe.MatchString(k.Name) {
			bad("draft kind %q is not a lowercase name", k.Name)
		}
		unique("draft kind", k.Name)
		switch {
		case k.Delivery != "" && !slices.Contains(deliveries, k.Delivery):
			bad("draft kind %q: delivery %q is not one of deliveries", k.Name, k.Delivery)
		case k.Delivery == "" && s.DefaultDelivery == "":
			bad("draft kind %q names no delivery and there is no default_delivery", k.Name)
		}
	}
	for _, st := range s.Steps {
		if !nameRe.MatchString(st) {
			bad("step %q is not a step name", st)
		}
	}
	for _, c := range s.ConfigReads {
		if !nameRe.MatchString(c) {
			bad("config_reads %q is not a configuration key", c)
		}
	}
	for _, n := range s.Notify {
		if !slices.Contains(plugin.Topics, plugin.Topic(n)) {
			bad("notify %q is not a host topic", n)
		}
	}
	s.validateUI(bad, unique)
	for _, r := range s.Roles {
		if !nameRe.MatchString(r.Name) {
			bad("role %q is not a lowercase name", r.Name)
		}
		unique("role", r.Name)
		if r.Doc == "" {
			bad("role %q: doc is required", r.Name)
		}
	}
	for _, d := range s.Deliveries {
		if !nameRe.MatchString(d.Name) {
			bad("delivery %q is not a lowercase name", d.Name)
		}
		unique("delivery", d.Name)
	}
	for _, j := range s.Jobs {
		if !nameRe.MatchString(j.Name) {
			bad("job %q is not a lowercase name", j.Name)
		}
		unique("job", j.Name)
		if j.Every <= 0 {
			bad("job %q: every must be positive", j.Name)
		}
		if j.EveryFrom != "" && !slices.ContainsFunc(s.ConfigReads, func(c string) bool { return j.EveryFrom == c || strings.HasPrefix(j.EveryFrom, c+".") }) {
			bad("job %q: every_from %q is not under config_reads", j.Name, j.EveryFrom)
		}
	}
	for _, c := range s.Commands {
		if !nameRe.MatchString(c.Name) {
			bad("command %q is not a lowercase name", c.Name)
		}
		unique("command", c.Name)
	}
	for _, t := range s.Tools {
		if !regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`).MatchString(t.Name) {
			bad("tool %q is not a lowercase name of letters, digits and _", t.Name)
		}
		unique("tool", t.Name)
		var schema map[string]any
		if err := json.Unmarshal([]byte(t.Input), &schema); err != nil {
			bad("tool %q: input is not a JSON object: %v", t.Name, err)
		}
		if t.Doc == "" {
			bad("tool %q: doc is required", t.Name)
		}
		if t.Posts != "" && t.Posts != "now" {
			bad("tool %q: posts %q is not now", t.Name, t.Posts)
		}
		for _, a := range t.Answers {
			if !regexp.MustCompile(`^[a-z][a-z0-9_]*$`).MatchString(a.Name) {
				bad("tool %q: answer %q is not a lowercase name", t.Name, a.Name)
			}
			unique("answer of "+t.Name, a.Name)
			if a.Doc == "" {
				bad("tool %q: answer %q: doc is required", t.Name, a.Name)
			}
			for _, f := range a.Fields {
				if !regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`).MatchString(f.Name) {
					bad("tool %q: answer %q: field %q is not a capitalized name", t.Name, a.Name, f.Name)
				}
			}
		}
	}
	return errors.Join(errs...)
}

func (s *Source) validateUI(bad func(string, ...any), unique func(string, string)) {
	ui := s.UI
	if ui == nil {
		return
	}
	var views []string
	for _, v := range ui.Views {
		if !nameRe.MatchString(v.ID) {
			bad("ui view %q is not a lowercase name", v.ID)
		}
		unique("ui view", v.ID)
		views = append(views, v.ID)
	}
	var facets []string
	for _, f := range ui.Facets {
		if !nameRe.MatchString(f.ID) {
			bad("ui facet %q is not a lowercase name", f.ID)
		}
		unique("ui facet", f.ID)
		facets = append(facets, f.ID)
		if len(f.Buckets) > 0 && f.Sort != "time" {
			bad("ui facet %q: buckets need sort = time", f.ID)
		}
		for i, b := range f.Buckets {
			unique("bucket of "+f.ID, b.ID)
			if !bucketRe.MatchString(b.ID) {
				bad("ui facet %q: bucket %q needs a lowercase id", f.ID, b.ID)
			}
			if b.Within == 0 && i < len(f.Buckets)-1 {
				bad("ui facet %q: bucket %q needs a within; only the last bucket catches the rest", f.ID, b.ID)
			}
		}
	}
	for _, v := range ui.Views {
		if v.UnitSort != "" && !slices.Contains(facets, v.UnitSort) {
			bad("ui view %q: unit_sort %q is not a facet", v.ID, v.UnitSort)
		}
	}
	for _, m := range ui.Menubar {
		unique("ui menubar", m.ID)
		if !slices.Contains(views, m.View) {
			bad("ui menubar %q: view %q is not one of the plugin's views", m.ID, m.View)
		}
	}
	for _, k := range ui.Keys {
		if k.Key == "" || k.Action == "" {
			bad("ui key %q: key and action are required", k.Key)
		}
	}
	if ui.Web != "" && (filepath.IsAbs(ui.Web) || strings.HasPrefix(filepath.Clean(ui.Web), "..")) {
		bad("ui web %q is not a directory inside the plugin", ui.Web)
	}
}
