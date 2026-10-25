// Package extract turns a Slack conversation window into items ledger ops
// through one agent call.
package extract

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/link"
	"github.com/wloophq/wloop/internal/domain/slack"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type Viewer struct{ ID, Name string }

type Input struct {
	Unit         unit.Unit
	Viewer       Viewer
	Now          time.Time // rendered with the configured zone's abbreviation
	Language     string    // the language of every text field, config slack.extraction.language
	Conversation slack.Conversation
	Messages     []slack.Message // the window, ascending; never cut inside a thread
	NewFrom      string          // first ts after the unit's cursor; "" means all are new
	Users        map[string]slack.User
	Ledger       []items.Item // the unit's current items, rendered by their own Item.ID
	Brief        string
	Contexts     []string     // placement contexts offered (context map keys)
	Segments     []slack.Span // a DM's current spans, ascending, rendered s1, s2...
	WindowChars  int          // Render cuts the oldest messages before NewFrom beyond it; 0 keeps all
	// Context are read-only messages before Messages, rendered c1, c2...: nothing is extracted from them.
	Context []slack.Message
	// Segment is the one DM segment the call covers, its bounds marked; zero for a whole conversation.
	Segment slack.Span
	Focus   string // the ts of the message the unit is about, marked focus
}

type Result struct {
	Brief, ViewerRole, Verdict, VerdictWhy string
	StartedByViewer, ClosurePosted         bool
	Items                                  []ItemOut
	Segments                               []SegmentOut // DM conversations only
	Links                                  []LinkOut
	// Dropped counts the items and links left out because they cite a message outside the window.
	Dropped int
}

// ItemOut is the schema's item, v2 fields plus ref, mode and place.
type ItemOut struct {
	Ref, Kind, Summary, OriginTS, Requester                                    string   // Ref is an Item.ID of Input.Ledger, or "n<N>" for a new item
	Assignees, WaitingOn                                                       []string // Slack user ids
	AssignmentTS, AssignmentBasis, State, Resolution, EvidenceTS, EvidenceKind string
	Confidence, Ambiguity, Context, Deadline                                   string
	Links                                                                      []struct{ URL, Label string }
	Options                                                                    []struct {
		Option   string
		BackedBy []string
	}
	Mode  string                        // "none", "headless", "session"
	Place struct{ Context, Why string } // Context is one of Input.Contexts or ""
}

// SegmentOut: Ref "s<N>" names an existing segment, "new" mints one.
type SegmentOut struct{ Ref, FromTS, ToTS, Topic string }

type LinkOut struct {
	Item, Target, EvidenceTS string    // Target: a URL or "<provider>:<key>" ("github:acme/widget#527")
	Kind                     link.Kind // "mention" | "review" | "merge"; prompt.md defines each
}

// ParseTarget turns a LinkOut.Target into a unit through the registered codecs;
// false drops the link and counts it.
type ParseTarget func(target string) (unit.Unit, bool)

// Extract asks for in's result; ask carries Tier and MaxCostUSD. An answer that does not fit
// the schema, or names a ref, segment or link item in doesn't hold, fails with turns.ErrUnfit;
// a new item, an item whose evidence or a link citing a message outside the window, the context's
// included, is dropped.
func Extract(ctx context.Context, a turns.Asker, ask turns.Ask, in Input) (Result, turns.Answer, error) {
	prompt, err := Prompt(in)
	if err != nil {
		return Result{}, turns.Answer{}, err
	}
	ask.Prompt, ask.Schema = prompt, Schema()
	ans, err := a.Ask(ctx, ask)
	if err != nil {
		return Result{}, ans, err
	}
	r, err := decode(ans.JSON, in)
	if err != nil {
		return Result{}, ans, fmt.Errorf("%w: %v", turns.ErrUnfit, err)
	}
	return r, ans, nil
}

// UnitState is a unit's last extraction; Topic names a DM segment's topic.
type UnitState struct {
	Brief, Verdict, VerdictWhy, ViewerRole, Topic string
	StartedByViewer, ClosurePosted                bool
	At                                            time.Time
}

type Hint struct{ Mode, Context, Why string }

type Reader interface {
	States(ctx context.Context, units []unit.Unit) (map[unit.Unit]UnitState, error)
	Hints(ctx context.Context, u unit.Unit) (map[string]Hint, error) // by ledger item id
}

type wireResult struct {
	Brief           string       `json:"brief"`
	ViewerRole      string       `json:"viewer_role"`
	StartedByViewer bool         `json:"started_by_viewer"`
	Items           []wireItem   `json:"items"`
	Verdict         string       `json:"verdict"`
	VerdictWhy      string       `json:"verdict_why"`
	ClosurePosted   bool         `json:"closure_posted"`
	Segments        []wireSeg    `json:"segments"`
	Links           []wireLinkTo `json:"links"`
}

type wireItem struct {
	Ref             string   `json:"ref"`
	Kind            string   `json:"kind"`
	Summary         string   `json:"summary"`
	OriginTS        string   `json:"origin_ts"`
	Requester       string   `json:"requester"`
	Assignees       []string `json:"assignees"`
	AssignmentTS    string   `json:"assignment_ts"`
	AssignmentBasis string   `json:"assignment_basis"`
	WaitingOn       []string `json:"waiting_on"`
	State           string   `json:"state"`
	Resolution      string   `json:"resolution"`
	EvidenceTS      string   `json:"evidence_ts"`
	EvidenceKind    string   `json:"evidence_kind"`
	Confidence      string   `json:"confidence"`
	Ambiguity       string   `json:"ambiguity"`
	Links           []struct {
		URL   string `json:"url"`
		Label string `json:"label"`
	} `json:"links"`
	Context string `json:"context"`
	Options []struct {
		Option   string   `json:"option"`
		BackedBy []string `json:"backed_by"`
	} `json:"options"`
	Deadline string `json:"deadline"`
	Mode     string `json:"mode"`
	Place    struct {
		Context string `json:"context"`
		Why     string `json:"why"`
	} `json:"place"`
}

type wireSeg struct {
	Ref    string `json:"ref"`
	FromTS string `json:"from_ts"`
	ToTS   string `json:"to_ts"`
	Topic  string `json:"topic"`
}

type wireLinkTo struct {
	Item       string `json:"item"`
	Target     string `json:"target"`
	Kind       string `json:"kind"`
	EvidenceTS string `json:"evidence_ts"`
}

func decode(raw []byte, in Input) (Result, error) {
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return Result{}, err
	}
	if err := conform(schemaDoc, doc, "$"); err != nil {
		return Result{}, err
	}
	var w wireResult
	if err := json.Unmarshal(raw, &w); err != nil {
		return Result{}, err
	}
	r := Result{
		Brief: w.Brief, ViewerRole: w.ViewerRole, Verdict: w.Verdict, VerdictWhy: w.VerdictWhy,
		StartedByViewer: w.StartedByViewer, ClosurePosted: w.ClosurePosted,
	}
	known := map[string]bool{}
	for _, it := range in.Ledger {
		known[it.ID] = true
	}
	refs := map[string]bool{}
	inWindow := map[string]bool{}
	for _, m := range in.Messages {
		inWindow[m.TS] = true
	}
	dropped := map[string]bool{}
	offered := map[string]bool{}
	for _, c := range in.Contexts {
		offered[c] = true
	}
	for _, wi := range w.Items {
		if !known[wi.Ref] && !isNewRef(wi.Ref) {
			return Result{}, fmt.Errorf("item ref %q is neither in the ledger nor new", wi.Ref)
		}
		if isNewRef(wi.Ref) && !inWindow[wi.OriginTS] || wi.EvidenceTS != "" && !inWindow[wi.EvidenceTS] {
			dropped[wi.Ref] = true
			r.Dropped++
			continue
		}
		if refs[wi.Ref] {
			return Result{}, fmt.Errorf("item ref %q appears twice", wi.Ref)
		}
		refs[wi.Ref] = true
		it := ItemOut{
			Ref: wi.Ref, Kind: wi.Kind, Summary: wi.Summary, OriginTS: wi.OriginTS, Requester: wi.Requester,
			Assignees: wi.Assignees, WaitingOn: wi.WaitingOn, AssignmentTS: wi.AssignmentTS,
			AssignmentBasis: wi.AssignmentBasis, State: wi.State, Resolution: wi.Resolution,
			EvidenceTS: wi.EvidenceTS, EvidenceKind: wi.EvidenceKind, Confidence: wi.Confidence,
			Ambiguity: wi.Ambiguity, Context: wi.Context, Deadline: wi.Deadline, Mode: wi.Mode,
		}
		for _, l := range wi.Links {
			it.Links = append(it.Links, struct{ URL, Label string }{l.URL, l.Label})
		}
		for _, o := range wi.Options {
			it.Options = append(it.Options, struct {
				Option   string
				BackedBy []string
			}{o.Option, o.BackedBy})
		}
		it.Place.Why = wi.Place.Why
		if offered[wi.Place.Context] {
			it.Place.Context = wi.Place.Context
		}
		r.Items = append(r.Items, it)
	}
	for _, s := range w.Segments {
		if s.Ref != "new" {
			n, _ := strconv.Atoi(strings.TrimPrefix(s.Ref, "s"))
			if n < 1 || n > len(in.Segments) {
				return Result{}, fmt.Errorf("segment %q is not one of the %d rendered", s.Ref, len(in.Segments))
			}
		}
		r.Segments = append(r.Segments, SegmentOut(s))
	}
	for _, l := range w.Links {
		if dropped[l.Item] || (l.EvidenceTS != "" && !inWindow[l.EvidenceTS]) {
			r.Dropped++
			continue
		}
		if l.Item != "" && !refs[l.Item] && !known[l.Item] {
			return Result{}, fmt.Errorf("link names item %q, which neither the answer nor the ledger holds", l.Item)
		}
		r.Links = append(r.Links, LinkOut{Item: l.Item, Target: l.Target, EvidenceTS: l.EvidenceTS, Kind: link.Kind(l.Kind)})
	}
	return r, nil
}

func isNewRef(ref string) bool {
	n, err := strconv.Atoi(strings.TrimPrefix(ref, "n"))
	return strings.HasPrefix(ref, "n") && err == nil && n > 0
}

// conform checks v against the subset of JSON Schema schema.json uses.
func conform(schema map[string]any, v any, path string) error {
	switch schema["type"] {
	case "object":
		obj, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("%s: not an object", path)
		}
		props, _ := schema["properties"].(map[string]any)
		for _, req := range schema["required"].([]any) {
			if _, ok := obj[req.(string)]; !ok {
				return fmt.Errorf("%s: %s is missing", path, req)
			}
		}
		for k, val := range obj {
			sub, ok := props[k].(map[string]any)
			if !ok {
				return fmt.Errorf("%s: unknown field %s", path, k)
			}
			if err := conform(sub, val, path+"."+k); err != nil {
				return err
			}
		}
	case "array":
		arr, ok := v.([]any)
		if !ok {
			return fmt.Errorf("%s: not an array", path)
		}
		if limit, ok := schema["maxItems"].(float64); ok && len(arr) > int(limit) {
			return fmt.Errorf("%s: %d entries, over %d", path, len(arr), int(limit))
		}
		sub := schema["items"].(map[string]any)
		for i, val := range arr {
			if err := conform(sub, val, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	case "string":
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("%s: not a string", path)
		}
		n := utf8.RuneCountInString(s)
		if limit, ok := schema["maxLength"].(float64); ok && n > int(limit) {
			return fmt.Errorf("%s: %d characters, over %d", path, n, int(limit))
		}
		if limit, ok := schema["minLength"].(float64); ok && n < int(limit) {
			return fmt.Errorf("%s: %d characters, under %d", path, n, int(limit))
		}
		if enum, ok := schema["enum"].([]any); ok && !slices.Contains(enum, any(s)) {
			return fmt.Errorf("%s: %q is not one of %v", path, s, enum)
		}
		if p, ok := schema["pattern"].(string); ok && !regexp.MustCompile(p).MatchString(s) {
			return fmt.Errorf("%s: %q does not match %s", path, s, p)
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("%s: not a boolean", path)
		}
	default:
		return fmt.Errorf("%s: schema type %v unsupported", path, schema["type"])
	}
	return nil
}
