// Package home composes the cockpit's home and decision queues from the profile's views, the decisions each
// task waits on and the words of the profile and its plugins.
package home

import (
	"context"

	coretasks "github.com/wloophq/wloop/internal/core/tasks"
	"github.com/wloophq/wloop/internal/domain/decision"
	"github.com/wloophq/wloop/internal/domain/task"
	"github.com/wloophq/wloop/internal/domain/unit"
)

// Lists are the tasks of several views in one pass (coretasks.Service).
type Lists interface {
	Sets(ctx context.Context, views []task.View, keep coretasks.Keep) (map[task.View][]task.Task, error)
}

// Decisions are the decisions a task waits on, as the profile maps its open gates and its units' states.
type Decisions interface {
	Of(ctx context.Context, e task.Task) ([]decision.Found, error)
}

// Words are the profile's and the plugins' words in the served locale.
type Words interface {
	Profile() Profile
	Kind(k decision.Kind) KindWords
	Plugin(p unit.ProviderID) PluginWords
	// Form is the CLDR plural category of n in the served locale.
	Form(n int) string
}

// Forms are a text's plural forms by CLDR category; "other" is required.
type Forms map[string]string

// Or is f, else the first of others that holds a text.
func (f Forms) Or(others ...Forms) Forms {
	for _, x := range append([]Forms{f}, others...) {
		if len(x) > 0 {
			return x
		}
	}
	return nil
}

// For is the text of category form, else the "other" text.
func (f Forms) For(form string) string {
	if s, ok := f[form]; ok {
		return s
	}
	return f["other"]
}

// Profile is home.toml: the briefing's words and counters, the sections and the capture hint.
type Profile struct {
	Template, Join, LastJoin, End, Count, Empty string
	PerSentence                                 int
	Counters                                    []Counter
	Sections                                    []Section
	CaptureHint                                 string
}

// Counter counts a decision kind's decisions (Kind), one per item of its queue, or a view's tasks (View, with its own Noun and Clause).
type Counter struct {
	ID           string
	Kind         decision.Kind
	View         task.View
	Noun, Clause Forms
}

// Section is the first Limit rows of View, titled Label, each showing Time.
type Section struct {
	ID          string
	View        task.View
	Label, Time string
	Limit       int
}

// KindWords are a decision kind's view and words; Primary is the gate intent a queue offers first.
type KindWords struct {
	View                  task.View
	Noun, Clause          Forms
	Queue, Empty, Primary string
}

// PluginWords are a plugin's noun, its words of each decision kind and what its queue offers first on each.
type PluginWords struct {
	Noun    Forms
	Kinds   map[decision.Kind]PluginKind
	Primary map[decision.Kind]string
	// Situations are how the plugin's situations read, by situation.
	Situations map[string]string
}

// PluginKind are a plugin's words of a decision kind, each empty when the kind's own stand.
type PluginKind struct {
	Clause       Forms
	Queue, Empty string
}
