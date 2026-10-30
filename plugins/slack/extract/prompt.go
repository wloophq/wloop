package extract

import (
	"cmp"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"sync"

	slack "github.com/wloophq/wloop/plugins/slack/domain"
	"github.com/wloophq/wloop/plugins/slack/items"
	"github.com/wloophq/wloop/plugins/slack/jsonschema"
)

// PromptFile and BriefSchemaFile are the extraction's prompt and its
// brief's schema in the plugin's profile layer.
const (
	PromptFile      = "prompts/slack-extraction.md"
	BriefSchemaFile = "prompts/slack-brief.schema.json"
)

//go:embed schema.json
var answerSchemaJSON string

// Prompts are the extraction's prompt and brief schema as the profile
// holds them, and the schema an extraction answers to: the brief's schema
// under the conversation's brief and each segment's.
type Prompts struct {
	template, brief string
	schemaJSON      string
	schema          *jsonschema.Schema

	mu       sync.Mutex
	composed map[string]composedSchema
}

// NewPrompts are the prompts of template and briefSchema.
func NewPrompts(template, briefSchema string) (*Prompts, error) {
	out, s, err := compose(answerSchemaJSON, briefSchema)
	if err != nil {
		return nil, err
	}
	return &Prompts{template: template, brief: briefSchema, schemaJSON: out, schema: s, composed: map[string]composedSchema{}}, nil
}

// LoadPrompts reads the prompts through read, the plugin's profile layer.
func LoadPrompts(ctx context.Context, read func(ctx context.Context, path string) ([]byte, error)) (*Prompts, error) {
	tmpl, err := read(ctx, PromptFile)
	if err != nil {
		return nil, fmt.Errorf("extract: %s: %w", PromptFile, err)
	}
	brief, err := read(ctx, BriefSchemaFile)
	if err != nil {
		return nil, fmt.Errorf("extract: %s: %w", BriefSchemaFile, err)
	}
	return NewPrompts(string(tmpl), string(brief))
}

// Schema is the schema an extraction answers to, without the brief
// intent's extra fields.
func (p *Prompts) Schema() string { return p.schemaJSON }

var errNoPrompts = errors.New("extract: no prompts")

// Prompt fills the extraction prompt for in; each placeholder is replaced once, so text in the
// conversation that looks like a placeholder stays as written.
func Prompt(in Input) (string, error) {
	if in.Prompts == nil {
		return "", errNoPrompts
	}
	if in.Viewer.ID == "" {
		return "", fmt.Errorf("extract: no viewer")
	}
	lang := in.Language
	if lang == "" {
		lang = "English"
	}
	name := in.Viewer.Name
	if name == "" {
		name = in.Viewer.ID
	}
	return strings.NewReplacer(
		"{{VIEWER_ID}}", in.Viewer.ID,
		"{{VIEWER_NAME}}", name,
		"{{NOW}}", in.Now.In(zone(in)).Format("2006-01-02 15:04 MST (Monday)"),
		"{{LANGUAGE}}", lang,
		"{{LEDGER}}", renderLedger(in),
		"{{CONTEXTS}}", renderContexts(in.Contexts),
		"{{VOCABULARY}}", VocabularyOf(in.Vocabulary),
		"{{CONVERSATION}}", Render(in),
	).Replace(in.Prompts.template), nil
}

// VocabularyOf is the vocabulary line of the prompt.
func VocabularyOf(v string) string {
	if strings.TrimSpace(v) == "" {
		return "(none yet)"
	}
	return strings.TrimSpace(v)
}

func renderContexts(cs []string) string {
	if len(cs) == 0 {
		return "(no context is offered: leave `place.context` empty)"
	}
	return "- " + strings.Join(cs, "\n- ")
}

func renderLedger(in Input) string {
	var b strings.Builder
	if in.Brief != "" {
		fmt.Fprintf(&b, "Previous brief: %s\n\n", GoalOf(in.Brief))
	}
	if len(in.Ledger) == 0 {
		b.WriteString("(no items yet)")
		return b.String()
	}
	for _, it := range in.Ledger {
		state := string(it.State)
		if it.Resolution != "" {
			state += ": " + string(it.Resolution)
		}
		flags := ""
		if it.Confirmed {
			flags = ", confirmed"
		}
		fmt.Fprintf(&b, "%s (%s, %s%s) %s\n", it.ID, state, it.Kind, flags, it.Summary)
		fmt.Fprintf(&b, "  requester: %s; assignees: %s; basis: %s; waiting on: %s\n",
			person(it.Requester), people(it.Assignees), it.Basis, people(it.WaitingOn))
		fmt.Fprintf(&b, "  origin: %s; confidence: %s", tsOfRef(it.Origin.Ref), it.Confidence)
		if it.Deadline != "" {
			fmt.Fprintf(&b, "; deadline: %s", it.Deadline)
		}
		if it.Evidence.Anchor.Ref != "" {
			fmt.Fprintf(&b, "; evidence: %s %s", it.Evidence.Kind, tsOfRef(it.Evidence.Anchor.Ref))
		}
		b.WriteString("\n")
		if it.Ambiguity != "" {
			fmt.Fprintf(&b, "  ambiguity: %s\n", it.Ambiguity)
		}
		if it.Context != "" {
			fmt.Fprintf(&b, "  context: %s\n", oneLine(it.Context))
		}
		for _, o := range it.Options {
			fmt.Fprintf(&b, "  option: %s (backed by %s)\n", o.Label, people(o.ArguedBy))
		}
		if len(it.URLs) > 0 {
			fmt.Fprintf(&b, "  links: %s\n", strings.Join(it.URLs, " "))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// person renders p as the conversation renders them: a user as a mention, a user group as
// a subteam mention, a team or role by its name.
func person(p items.Person) string {
	if p.Key == "" {
		return "nobody"
	}
	if id, ok := strings.CutPrefix(p.Key, slack.UserKey("")); ok {
		if p.Name == "" || p.Name == id {
			return "<@" + id + ">"
		}
		return "<@" + id + "|" + p.Name + ">"
	}
	name := strings.TrimPrefix(p.Key, slack.GroupKey(""))
	if !slack.IsGroupID(name) {
		return cmp.Or(p.Name, name)
	}
	if p.Name == "" || p.Name == slack.UnknownGroup {
		return "<!subteam^" + name + ">"
	}
	return "<!subteam^" + name + "|" + p.Name + ">"
}

func people(ps []items.Person) string {
	if len(ps) == 0 {
		return "nobody"
	}
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = person(p)
	}
	return strings.Join(out, ", ")
}

func tsOfRef(ref string) string {
	if i := strings.LastIndexByte(ref, '/'); i >= 0 {
		return "ts=" + ref[i+1:]
	}
	return ref
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }
