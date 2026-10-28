package builtin

import (
	"slices"
	"strings"
)

// Surface is a file kind of the profile language, or the configuration.
type Surface string

const (
	SurfaceIntent   Surface = "intent"   // intents/<name>/intent.toml
	SurfaceWorkflow Surface = "workflow" // workflows/<name>/workflow.toml
	SurfaceStep     Surface = "step"     // a workflow step's arguments
	SurfaceActions  Surface = "actions"  // actions.toml
	SurfaceDefaults Surface = "defaults" // defaults.toml
	SurfaceLabels   Surface = "labels"   // labels.toml
	SurfaceTools    Surface = "tools"    // tools.toml
	SurfaceTasks    Surface = "tasks"    // tasks.toml
	SurfaceViews    Surface = "views"    // views.toml
	SurfaceGates    Surface = "gates"    // gates.toml
	SurfaceKeys     Surface = "keys"     // keys.toml
	SurfaceNotices  Surface = "notices"  // notices.toml
	SurfaceBlock    Surface = "block"    // a judgment block's front matter
	SurfaceConfig   Surface = "config"   // config.toml
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
	KindMap    // a table whose keys an author names
	KindAny    // more than one form, which its decoder reads
	KindNumber // an integer or a decimal
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
	// Required says that the table holding the key must set it.
	Required bool
}

// Value is one value of an enum key.
type Value struct {
	Name string
	Doc  string
	// Releases says that the value lets a send or a run go with no person
	// approving it, which no did-you-mean suggests.
	Releases bool
}

// Grammar is every reserved key of the language.
var Grammar = slices.Concat(intentGrammar, workflowGrammar, stepGrammar, actionsGrammar, configGrammar, profileGrammar, toolsGrammar, tasksGrammar, viewsGrammar, gatesGrammar, keysGrammar, noticesGrammar)

// Problem is a key Check refuses, at its dotted path in the document.
type Problem struct {
	Path string
	Msg  string
	// Near is the value or key the message suggests, "" when none.
	Near string
	// Unknown says that no row names the key.
	Unknown bool
}

// Check walks doc, a document of surface decoded without a schema (a TOML
// table or a JSON object as map[string]any), against the grammar: unknown
// keys and values, with did-you-mean, kinds and enums. A
// surface the grammar has no row of refuses every key.
func Check(surface Surface, doc map[string]any) []Problem {
	return check(Grammar, surface, doc)
}

// CheckStep is Check on the arguments of a workflow step, use, without use itself.
func CheckStep(use string, args map[string]any) []Problem {
	var rows []Key
	for _, k := range stepGrammar {
		if k.Step == use {
			rows = append(rows, k)
		}
	}
	w := walker{rows: rows}
	w.table(nil, "", args)
	return w.problems
}

// StepArgs are the arguments step use takes, in the grammar's order.
func StepArgs(use string) []string {
	var out []string
	for _, k := range stepGrammar {
		if k.Step == use {
			out = append(out, k.Path)
		}
	}
	return out
}

// IsPredKey says whether a key named last holds a predicate somewhere in
// the language, so a value written without quotes gets the quoting hint.
func IsPredKey(last string) bool {
	for _, k := range Grammar {
		if k.Kind == KindPred && k.Path[strings.LastIndex(k.Path, ".")+1:] == last {
			return true
		}
	}
	return false
}

// ValuesOf are the values of the enum key at path of surface, the table's
// set that a decoder checks a value against; nil when the key is no enum.
func ValuesOf(surface Surface, path string) []Value {
	for _, k := range Grammar {
		if k.In == surface && k.Path == path && k.Values != nil {
			return k.Values()
		}
	}
	return nil
}

// StepValuesOf are the values of step use's enum argument arg.
func StepValuesOf(use, arg string) []Value {
	for _, k := range stepGrammar {
		if k.Step == use && k.Path == arg && k.Values != nil {
			return k.Values()
		}
	}
	return nil
}

// ValueNames are the names of values.
func ValueNames(values []Value) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = v.Name
	}
	return out
}
