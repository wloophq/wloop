package manifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
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
	Jobs     []Job     `toml:"jobs" doc:"The jobs the host runs on the plugin through Ingest, by name."`
	Commands []Command `toml:"commands" doc:"The commands the plugin binary serves in CLI mode, which run without the daemon."`
	Tools    []Tool    `toml:"tools" doc:"The MCP tools the plugin serves through CallTool."`
}

// SourceVocabulary is what a source plugin adds to the host's tables before
// the profile loads.
type SourceVocabulary struct {
	// Provider is the plugin's id, the provider of its units; Source sets it
	// from the manifest.
	Provider string `toml:"-"`

	Slot         string     `toml:"slot" doc:"The workflow fact slot of the plugin's units, such as \"pr\"; every fact name starts with it. Required."`
	BriefSubject bool       `toml:"brief_subject" doc:"The plugin's units may have a brief, whose facts their workflows read."`
	Rounds       bool       `toml:"rounds" doc:"The plugin's units count automatic turns per unit, against turns.per_unit_per_day."`
	Payload      string     `toml:"payload" doc:"The template root of the plugin's turn payload, such as \"PR\", which only intents of the plugin read."`
	Facts        []Fact     `toml:"facts" doc:"The workflow facts the plugin reports."`
	Effects      []Effect   `toml:"effects" doc:"The turn and draft effects the plugin applies through ApplyEffect."`
	Events       []Named    `toml:"events" doc:"The event kinds only the plugin's units record."`
	Roles        []Role     `toml:"roles" doc:"The roles an intent of the plugin may take, each naming host mechanisms."`
	RowKinds     []string   `toml:"row_kinds" doc:"The values of row.kind on the plugin's rows."`
	Deliveries   []Delivery `toml:"deliveries" doc:"How the plugin posts drafts."`
	Signals      []Named    `toml:"signals" doc:"The link signals the plugin reports, read as task.link_signal.<name>."`
	Kinds        []string   `toml:"kinds" doc:"The task kind values the plugin's units give their tasks."`
	Situations   []Named    `toml:"situations" doc:"The situations the plugin derives items from."`
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
	Name    string   `toml:"name" doc:"The job's name, which Ingest receives. Required."`
	Every   Duration `toml:"every" doc:"How often the job runs, as a Go duration. Required."`
	Timeout Duration `toml:"timeout" doc:"A run's limit, as a Go duration; empty is the host's default."`
}

// Command is a command of the plugin binary in CLI mode.
type Command struct {
	Name  string `toml:"name" doc:"The command's name, after the plugin's id on the command line. Required."`
	Usage string `toml:"usage" doc:"The command's arguments, as its help shows them."`
}

// Tool is an MCP tool the plugin serves.
type Tool struct {
	Name  string `toml:"name" doc:"The tool's name; its description lives in the profile's tools.toml. Required."`
	Input string `toml:"input" doc:"The JSON schema of the tool's arguments, an object. Required."`
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
	named("event", s.Events)
	named("signal", s.Signals)
	named("situation", s.Situations)
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
	}
	return errors.Join(errs...)
}
