package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/link"
	"github.com/wloophq/wloop/internal/domain/record"
)

const toolName = "turn_end"

type tool struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	InputSchema *schema `json:"inputSchema"`
}

type schema struct {
	Type                 string             `json:"type"`
	Description          string             `json:"description,omitempty"`
	Enum                 []string           `json:"enum,omitempty"`
	MaxLength            int                `json:"maxLength,omitempty"`
	Minimum              *int               `json:"minimum,omitempty"`
	Properties           map[string]*schema `json:"properties,omitempty"`
	Required             []string           `json:"required,omitempty"`
	AdditionalProperties *bool              `json:"additionalProperties,omitempty"`
	Items                *schema            `json:"items,omitempty"`
	MinItems             int                `json:"minItems,omitempty"`
	MaxItems             int                `json:"maxItems,omitempty"`
}

func turnEndTool() tool {
	return tool{Name: toolName, InputSchema: inputSchema()}
}

func object(props map[string]*schema, required ...string) *schema {
	closed := false
	return &schema{Type: "object", Properties: props, Required: required, AdditionalProperties: &closed}
}

func str(max int) *schema { return &schema{Type: "string", MaxLength: max} }

func enum(values ...string) *schema { return &schema{Type: "string", Enum: values} }

func count() *schema {
	zero := 0
	return &schema{Type: "integer", Minimum: &zero}
}

func boolean() *schema { return &schema{Type: "boolean"} }

// inputSchema is turn_end's arguments; record follows domain/record's
// Record field for field.
func inputSchema() *schema {
	option := object(map[string]*schema{
		"id":          str(20),
		"label":       str(record.LabelMax),
		"consequence": str(record.ConsequenceMax),
		"publishes":   boolean(),
	}, "id", "label", "consequence")
	one := 1
	item := object(map[string]*schema{
		"path": str(record.PathMax),
		"line": {Type: "integer", Minimum: &one},
		"body": str(record.ItemBodyMax),
	}, "path", "line", "body")
	rec := object(map[string]*schema{
		"state":   enum(string(record.Done), string(record.Settled), string(record.Asked), string(record.Failed)),
		"summary": str(record.SummaryMax),
		"outcome": object(map[string]*schema{
			"verdict":      enum("APPROVE", "COMMENT", "REQUEST_CHANGES"),
			"url":          str(record.URLMax),
			"blocking":     count(),
			"non_blocking": count(),
		}),
		"question": object(map[string]*schema{
			"kind":      enum("choice", "confirm", "free"),
			"text":      str(record.TextMax),
			"options":   {Type: "array", Items: option, MinItems: 2, MaxItems: 5},
			"recommend": str(record.RecommendMax),
			"publishes": boolean(),
		}, "kind", "text"),
		"artifact": object(map[string]*schema{
			"kind":  enum(record.ArtifactKinds...),
			"body":  str(record.BodyMax),
			"items": {Type: "array", Items: item, MaxItems: record.ItemsMax},
		}, "kind"),
		"resume":     str(record.ResumeMax),
		"next":       str(record.NextMax),
		"user":       str(record.NextMax),
		"suspicious": boolean(),
		"ledger":     {Type: "array", Items: ledgerOp(), MaxItems: record.LedgerMax},
	}, "state", "summary")
	return object(map[string]*schema{
		"turn":   {Type: "string"},
		"record": rec,
	}, "turn", "record")
}

func enumOf[T ~string](values ...T) *schema {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = string(v)
	}
	return enum(out...)
}

// ledgerOp is the JSON of items.Op.
func ledgerOp() *schema {
	u := func() *schema {
		return object(map[string]*schema{"provider": str(32), "key": str(300)}, "provider", "key")
	}
	anchor := func() *schema {
		return object(map[string]*schema{
			"unit":      u(),
			"ref":       str(300),
			"permalink": str(record.URLMax),
			"at":        {Type: "string"},
		}, "ref")
	}
	person := object(map[string]*schema{
		"key":     str(200),
		"name":    str(200),
		"aliases": {Type: "array", Items: str(200)},
	}, "key")
	people := &schema{Type: "array", Items: person}
	texts := &schema{Type: "array", Items: str(record.URLMax)}
	fields := object(map[string]*schema{
		"kind":       enumOf(items.Request, items.Question, items.Decision, items.Todo),
		"summary":    str(items.MaxSummary),
		"context":    str(items.MaxContext),
		"deadline":   str(100),
		"ambiguity":  str(items.MaxSummary),
		"options":    {Type: "array", Items: object(map[string]*schema{"label": str(record.LabelMax), "argued_by": people}, "label")},
		"urls":       texts,
		"contexts":   texts,
		"initial":    enumOf(items.InitialInbox, items.InitialNext),
		"requester":  person,
		"waiting_on": people,
		"confidence": enumOf(items.High, items.Low),
	})
	return object(map[string]*schema{
		"op":         enumOf(items.OpCreate, items.OpUpdate, items.OpAssign, items.OpResolve, items.OpReopen, items.OpLink, items.OpEvidence),
		"unit":       u(),
		"item":       str(100),
		"anchor":     anchor(),
		"fields":     fields,
		"assignees":  {Type: "array", Items: person},
		"basis":      enumOf(items.ExplicitMention, items.DMAddressee, items.ReplyContext, items.SelfAssigned, items.Reassigned, items.GroupAsk, items.NoBasis),
		"resolution": enumOf(items.Done, items.Answered, items.Fixed, items.Delegated, items.Declined, items.Dismissed, items.Superseded, items.Moot),
		"evidence": object(map[string]*schema{
			"kind":   enumOf(items.EvidenceMessage, items.EvidenceReaction, items.EvidenceExternalClaim, items.EvidenceUnitEvent, items.EvidenceSentDraft),
			"anchor": anchor(),
		}, "kind", "anchor"),
		"to":        u(),
		"link_kind": enumOf(link.Mention, link.Review, link.Merge),
		"reason":    str(items.MaxSummary),
	}, "op", "anchor")
}

// ledgerText is what the items ledger made of the record's ops, "" when
// none were applied.
func (s *Server) ledgerText(ctx context.Context, r turns.Recorded) string {
	l := r.Ledger
	if l == nil {
		return ""
	}
	applied, unchanged := 0, 0
	for _, a := range l.Applied {
		if a.Changed {
			applied++
		} else {
			unchanged++
		}
	}
	var parts []string
	if applied > 0 || len(l.Applied)+len(l.Proposed)+len(l.Refused) == 0 {
		parts = append(parts, s.say(ctx, toolName, "ledger_applied", fields{"Count": applied}))
	}
	if unchanged > 0 {
		parts = append(parts, s.say(ctx, toolName, "ledger_unchanged", fields{"Count": unchanged}))
	}
	if len(l.Proposed) > 0 {
		parts = append(parts, s.say(ctx, toolName, "ledger_proposed", fields{"Count": len(l.Proposed)}))
	}
	if len(l.Refused) > 0 {
		why := make([]string, len(l.Refused))
		for i, f := range l.Refused {
			why[i] = s.say(ctx, toolName, "ledger_refusal", fields{"Index": f.Index, "Reason": f.Reason})
		}
		parts = append(parts, s.say(ctx, toolName, "ledger_refused", fields{"Count": len(l.Refused), "Why": strings.Join(why, "; ")}))
	}
	return strings.Join(parts, ", ")
}

func text(msg string, isError bool) map[string]any {
	return map[string]any{
		"content": []map[string]string{{"type": "text", "text": msg}},
		"isError": isError,
	}
}

func (s *Server) turnEnd(ctx context.Context, handle string, arguments json.RawMessage) any {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(arguments, &raw); err != nil || raw == nil {
		return s.badArguments(ctx, toolName, err)
	}
	var problems record.Problems
	var extra []string
	for k := range raw {
		if k != "turn" && k != "record" {
			extra = append(extra, k)
		}
	}
	if len(extra) > 0 {
		slices.Sort(extra)
		problems = append(problems, s.say(ctx, toolName, "problem_extra_arguments", fields{"Keys": strings.Join(extra, ", ")}))
	}
	var id string
	if v, ok := raw["turn"]; ok && json.Unmarshal(v, &id) != nil {
		problems = append(problems, s.say(ctx, toolName, "problem_turn_type", nil))
	} else if id = strings.TrimSpace(id); id == "" {
		problems = append(problems, s.say(ctx, toolName, "problem_turn_required", nil))
	}
	rawRecord := raw["record"]
	var rec record.Record
	if len(rawRecord) == 0 || string(rawRecord) == "null" {
		problems = append(problems, s.say(ctx, toolName, "problem_record_required", nil))
	} else if r, err := record.Parse(rawRecord); err != nil {
		problems = append(problems, s.say(ctx, toolName, "problem_record", fields{"Error": err.Error()}))
	} else {
		rec = r
		var bad record.Problems
		if errors.As(r.ValidateShape(), &bad) {
			problems = append(problems, bad...)
		}
	}
	if len(problems) > 0 {
		return s.fixThese(ctx, toolName, problems)
	}
	got, err := s.Turns.RecordTurnEnd(ctx, turns.RecordRequest{Session: handle, Turn: id, Via: "mcp", Record: rec})
	var bad record.Problems
	if errors.As(err, &bad) {
		return s.fixThese(ctx, toolName, bad)
	}
	var refusal turns.Refusal
	if errors.As(err, &refusal) {
		return s.answer(ctx, toolName, "refused_record", fields{"Reason": refusal.Reason}, true)
	}
	if err != nil {
		return s.answer(ctx, toolName, "store_failed", fields{"Error": err.Error()}, true)
	}
	return s.answer(ctx, toolName, "recorded", fields{"State": string(got.State), "Unit": got.Unit.Label(), "Note": got.Note, "Dropped": got.DroppedNote(),
		"Ledger": s.ledgerText(ctx, got)}, false)
}
