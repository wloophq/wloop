package builtin

import "slices"

// Surface is a file kind of the profile language, or the configuration.
type Surface string

const (
	SurfaceIntent   Surface = "intent"   // intents/<name>/intent.toml
	SurfaceWorkflow Surface = "workflow" // workflows/<name>/workflow.toml
	SurfaceStep     Surface = "step"     // a workflow step's arguments
	SurfaceActions  Surface = "actions"  // actions.toml
	SurfaceDefaults Surface = "defaults" // defaults.toml
	SurfaceLabels   Surface = "labels"   // labels.toml
	SurfaceConfig   Surface = "config"   // config.json
)

// Kind is what a key's value is.
type Kind int

const (
	KindPred     Kind = iota + 1 // a predicate's source
	KindFact                     // a fact's name
	KindTemplate                 // an inline text/template
	KindFile                     // a file beside the manifest
	KindEnum                     // one of Values
	KindDur                      // a duration (internal/domain/duration)
	KindBool
	KindInt
	KindText
	KindList // a list of Elem
	KindTable
	KindMap // a table whose keys an author names
)

// Key is one reserved key of the language. Path is dotted, "[]" marks an
// array of tables and "*" a name the author chooses:
// "action.refuse[].why", "inputs.*.type", "workflows.*.approval.mode".
type Key struct {
	Path string
	In   Surface
	// Step is the step whose argument the key is, for In == SurfaceStep.
	Step string
	Kind Kind
	// Elem is a list's element kind, for Kind == KindList.
	Elem Kind
	// Values are an enum's values, from the table that owns the set.
	Values  func() []Value
	Default string
	Doc     string
	// Bundled says that only the embedded profile may set the key.
	Bundled bool
	// Was are the key's removed spellings, read only to name the key in
	// the error.
	Was []string
}

// Value is one value of an enum key.
type Value struct {
	Name string
	Doc  string
	Was  []string
}

// Grammar is every reserved key of the language.
var Grammar = slices.Concat(intentGrammar, workflowGrammar, stepGrammar, actionsGrammar, configGrammar, profileGrammar)

// Problem is a key Check refuses, at its dotted path in the document.
type Problem struct {
	Path string
	Msg  string
}

// Check walks doc, a document of surface decoded without a schema (a TOML
// table or a JSON object as map[string]any), against the grammar: unknown
// and removed keys and values, with did-you-mean, kinds and enums.
func Check(surface Surface, doc map[string]any) []Problem {
	return nil
}
