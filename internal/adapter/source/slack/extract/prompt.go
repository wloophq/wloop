package extract

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/profiles"
)

// PromptFile is the bundled profile's extraction prompt, relative to the
// profile's root.
const PromptFile = "prompts/slack-extraction.md"

var promptTemplate = func() string {
	b, err := fs.ReadFile(profiles.Default, path.Join("default", PromptFile))
	if err != nil {
		panic("extract: the bundled profile has no " + PromptFile + ": " + err.Error())
	}
	return string(b)
}()

//go:embed schema.json
var schemaJSON string

var schemaDoc = func() map[string]any {
	var doc map[string]any
	if err := json.Unmarshal([]byte(schemaJSON), &doc); err != nil {
		panic("extract: schema.json: " + err.Error())
	}
	return doc
}()

func Schema() string { return schemaJSON }

// Prompt fills the extraction prompt for in; each placeholder is replaced once, so text in the
// conversation that looks like a placeholder stays as written.
func Prompt(in Input) (string, error) {
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
		"{{CONVERSATION}}", Render(in),
	).Replace(promptTemplate), nil
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
		fmt.Fprintf(&b, "Previous brief: %s\n\n", in.Brief)
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

func person(p items.Person) string {
	if p.Key == "" {
		return "nobody"
	}
	if id, ok := strings.CutPrefix(p.Key, "slack:"); ok {
		if p.Name == "" || p.Name == id {
			return id
		}
		return p.Name + " (" + id + ")"
	}
	return p.Key
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
