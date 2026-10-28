package source

import (
	"encoding/json"
	"time"

	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/wloophq/wloop/pkg/plugin"
	ppb "github.com/wloophq/wloop/pkg/plugin/pb"
	"github.com/wloophq/wloop/pkg/plugin/source/pb"
)

func ts(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

func tm(p *timestamppb.Timestamp) time.Time {
	if p == nil {
		return time.Time{}
	}
	return p.AsTime()
}

func raw(b []byte) json.RawMessage {
	if len(b) == 0 {
		return nil
	}
	return json.RawMessage(b)
}

func mapSlice[T, U any](in []T, f func(T) U) []U {
	if len(in) == 0 {
		return nil
	}
	out := make([]U, len(in))
	for i, v := range in {
		out[i] = f(v)
	}
	return out
}

func unitPB(u plugin.Unit) *ppb.Unit {
	if u == (plugin.Unit{}) {
		return nil
	}
	return &ppb.Unit{Provider: u.Provider, Key: u.Key}
}

func unitGo(u *ppb.Unit) plugin.Unit { return plugin.Unit{Provider: u.GetProvider(), Key: u.GetKey()} }

func unitsPB(us []plugin.Unit) []*ppb.Unit { return mapSlice(us, unitPB) }
func unitsGo(us []*ppb.Unit) []plugin.Unit { return mapSlice(us, unitGo) }

func personPB(p Person) *pb.Person {
	return &pb.Person{Key: p.Key, Name: p.Name, Aliases: p.Aliases}
}

func personGo(p *pb.Person) Person {
	return Person{Key: p.GetKey(), Name: p.GetName(), Aliases: p.GetAliases()}
}

func peoplePB(ps []Person) []*pb.Person { return mapSlice(ps, personPB) }
func peopleGo(ps []*pb.Person) []Person { return mapSlice(ps, personGo) }

func anchorPB(a Anchor) *pb.Anchor {
	if a == (Anchor{}) {
		return nil
	}
	return &pb.Anchor{Unit: unitPB(a.Unit), Ref: a.Ref, Permalink: a.Permalink, At: ts(a.At)}
}

func anchorGo(a *pb.Anchor) Anchor {
	return Anchor{Unit: unitGo(a.GetUnit()), Ref: a.GetRef(), Permalink: a.GetPermalink(), At: tm(a.GetAt())}
}

var timeUnits = map[TimeUnit]pb.TimeUnit{Calendar: pb.TimeUnit_TIME_UNIT_CALENDAR, WorkingDays: pb.TimeUnit_TIME_UNIT_WORKING_DAYS}

func factPB(f Fact) *pb.Fact {
	out := &pb.Fact{Name: f.Name}
	v := f.Value
	switch v.Kind {
	case KindBool:
		out.Value = &pb.Fact_Bool{Bool: v.Bool}
	case KindInt:
		out.Value = &pb.Fact_Int{Int: v.Int}
	case KindString:
		out.Value = &pb.Fact_String_{String_: v.String}
	case KindList:
		out.Value = &pb.Fact_List{List: &pb.StringList{Values: v.List}}
	case KindDuration:
		out.Value = &pb.Fact_Duration{Duration: durationpb.New(v.Duration)}
	case KindTime:
		out.Value = &pb.Fact_Time{Time: timestamppb.New(v.Time)}
	case KindSince:
		out.Value = &pb.Fact_Since{Since: &pb.Since{Since: timestamppb.New(v.Time)}}
	case KindAt:
		out.Value = &pb.Fact_At{At: &pb.At{At: timestamppb.New(v.Time)}}
	case KindAfter:
		out.Value = &pb.Fact_After{After: &pb.After{After: timestamppb.New(v.Time), Threshold: durationpb.New(v.Threshold), Unit: timeUnits[v.Unit]}}
	}
	return out
}

func factGo(f *pb.Fact) Fact {
	out := Fact{Name: f.GetName()}
	switch v := f.GetValue().(type) {
	case *pb.Fact_Bool:
		out.Value = Bool(v.Bool)
	case *pb.Fact_Int:
		out.Value = Int(v.Int)
	case *pb.Fact_String_:
		out.Value = String(v.String_)
	case *pb.Fact_List:
		out.Value = Value{Kind: KindList, List: v.List.GetValues()}
	case *pb.Fact_Duration:
		out.Value = Duration(v.Duration.AsDuration())
	case *pb.Fact_Time:
		out.Value = Time(v.Time.AsTime())
	case *pb.Fact_Since:
		out.Value = Since(v.Since.GetSince().AsTime())
	case *pb.Fact_At:
		out.Value = At(v.At.GetAt().AsTime())
	case *pb.Fact_After:
		unit := Calendar
		if v.After.GetUnit() == pb.TimeUnit_TIME_UNIT_WORKING_DAYS {
			unit = WorkingDays
		}
		out.Value = After(v.After.GetAfter().AsTime(), v.After.GetThreshold().AsDuration(), unit)
	}
	return out
}

func factsPB(fs []Fact) []*pb.Fact { return mapSlice(fs, factPB) }
func factsGo(fs []*pb.Fact) []Fact { return mapSlice(fs, factGo) }

func cardPB(c *Card) *pb.Card {
	if c == nil {
		return nil
	}
	out := &pb.Card{
		Label: c.Label, Title: c.Title, State: c.State, Outcome: c.Outcome, Why: c.Why,
		WaitsOn: peoplePB(c.WaitsOn), Since: ts(c.Since), Link: c.Link, TurnsLink: c.TurnsLink,
		StartedByMe: c.StartedByMe, ClosurePosted: c.ClosurePosted, Cares: c.Cares,
		Signals: mapSlice(c.Signals, func(s CardSignal) *pb.CardSignal {
			return &pb.CardSignal{Key: s.Key, Value: s.Value, Tone: s.Tone, At: ts(s.At)}
		}),
	}
	if h := c.LastHuman; h != nil {
		out.LastHuman = &pb.Human{Who: personPB(h.Who), What: h.What, At: ts(h.At), Link: h.Link}
	}
	return out
}

func cardGo(c *pb.Card) *Card {
	if c == nil {
		return nil
	}
	out := &Card{
		Label: c.GetLabel(), Title: c.GetTitle(), State: c.GetState(), Outcome: c.GetOutcome(), Why: c.GetWhy(),
		WaitsOn: peopleGo(c.GetWaitsOn()), Since: tm(c.GetSince()), Link: c.GetLink(), TurnsLink: c.GetTurnsLink(),
		StartedByMe: c.GetStartedByMe(), ClosurePosted: c.GetClosurePosted(), Cares: c.GetCares(),
		Signals: mapSlice(c.GetSignals(), func(s *pb.CardSignal) CardSignal {
			return CardSignal{Key: s.GetKey(), Value: s.GetValue(), Tone: s.GetTone(), At: tm(s.GetAt())}
		}),
	}
	if h := c.GetLastHuman(); h != nil {
		out.LastHuman = &Human{Who: personGo(h.GetWho()), What: h.GetWhat(), At: tm(h.GetAt()), Link: h.GetLink()}
	}
	return out
}

func optionPB(o Option) *pb.ItemOption {
	return &pb.ItemOption{Label: o.Label, ArguedBy: peoplePB(o.ArguedBy)}
}

func optionGo(o *pb.ItemOption) Option {
	return Option{Label: o.GetLabel(), ArguedBy: peopleGo(o.GetArguedBy())}
}

func evidencePB(e ItemEvidence) *pb.ItemEvidence {
	if e == (ItemEvidence{}) {
		return nil
	}
	return &pb.ItemEvidence{Kind: e.Kind, Anchor: anchorPB(e.Anchor)}
}

func evidenceGo(e *pb.ItemEvidence) ItemEvidence {
	return ItemEvidence{Kind: e.GetKind(), Anchor: anchorGo(e.GetAnchor())}
}

func itemPB(it Item) *pb.Item {
	return &pb.Item{
		Id: it.ID, Unit: unitPB(it.Unit), Kind: it.Kind, Summary: it.Summary, Context: it.Context,
		Options: mapSlice(it.Options, optionPB), Deadline: it.Deadline, Urls: it.URLs, Contexts: it.Contexts,
		Category: it.Category, Origin: anchorPB(it.Origin), Requester: personPB(it.Requester),
		Assignees: peoplePB(it.Assignees), AssignedAt: anchorPB(it.AssignedAt), Basis: it.Basis,
		WaitingOn: peoplePB(it.WaitingOn), State: it.State, Resolution: it.Resolution,
		Evidence: evidencePB(it.Evidence), Trail: mapSlice(it.Trail, evidencePB), Confidence: it.Confidence,
		Ambiguity: it.Ambiguity, Provenance: it.Provenance, Confirmed: it.Confirmed, TaskKey: it.TaskKey,
		UserAt: ts(it.UserAt), CreatedAt: ts(it.CreatedAt), UpdatedAt: ts(it.UpdatedAt),
	}
}

func itemGo(it *pb.Item) Item {
	return Item{
		ID: it.GetId(), Unit: unitGo(it.GetUnit()), Kind: it.GetKind(), Summary: it.GetSummary(), Context: it.GetContext(),
		Options: mapSlice(it.GetOptions(), optionGo), Deadline: it.GetDeadline(), URLs: it.GetUrls(), Contexts: it.GetContexts(),
		Category: it.GetCategory(), Origin: anchorGo(it.GetOrigin()), Requester: personGo(it.GetRequester()),
		Assignees: peopleGo(it.GetAssignees()), AssignedAt: anchorGo(it.GetAssignedAt()), Basis: it.GetBasis(),
		WaitingOn: peopleGo(it.GetWaitingOn()), State: it.GetState(), Resolution: it.GetResolution(),
		Evidence: evidenceGo(it.GetEvidence()), Trail: mapSlice(it.GetTrail(), evidenceGo), Confidence: it.GetConfidence(),
		Ambiguity: it.GetAmbiguity(), Provenance: it.GetProvenance(), Confirmed: it.GetConfirmed(), TaskKey: it.GetTaskKey(),
		UserAt: tm(it.GetUserAt()), CreatedAt: tm(it.GetCreatedAt()), UpdatedAt: tm(it.GetUpdatedAt()),
	}
}

func itemsPB(its []Item) []*pb.Item { return mapSlice(its, itemPB) }
func itemsGo(its []*pb.Item) []Item { return mapSlice(its, itemGo) }

func snapshotPB(s Snapshot) *pb.Snapshot {
	out := &pb.Snapshot{
		Unit: unitPB(s.Unit), Rev: s.Rev, At: ts(s.At), Gone: s.Gone, DataJson: s.Data, SituationJson: s.Situation,
		Card: cardPB(s.Card), Items: itemsPB(s.Items), Facts: factsPB(s.Facts), Owes: peoplePB(s.Owes),
		Signals: mapSlice(s.Signals, func(g Signal) *pb.LinkSignal {
			return &pb.LinkSignal{Name: g.Name, On: g.On, Since: ts(g.Since)}
		}),
		Offers:   mapSlice(s.Offers, func(o Offer) *pb.Offer { return &pb.Offer{Action: o.Action, ArgsJson: o.Args} }),
		Contexts: s.Contexts, Due: ts(s.Due),
	}
	if h := s.Extraction; h != nil {
		out.ExtractionHealth = &pb.ExtractionHealth{Pending: int64(h.Pending), BudgetHit: h.BudgetHit, Oldest: ts(h.Oldest)}
	}
	return out
}

func snapshotGo(s *pb.Snapshot) Snapshot {
	out := Snapshot{
		Unit: unitGo(s.GetUnit()), Rev: s.GetRev(), At: tm(s.GetAt()), Gone: s.GetGone(),
		Data: raw(s.GetDataJson()), Situation: raw(s.GetSituationJson()),
		Card: cardGo(s.GetCard()), Items: itemsGo(s.GetItems()), Facts: factsGo(s.GetFacts()), Owes: peopleGo(s.GetOwes()),
		Signals: mapSlice(s.GetSignals(), func(g *pb.LinkSignal) Signal {
			return Signal{Name: g.GetName(), On: g.GetOn(), Since: tm(g.GetSince())}
		}),
		Offers:   mapSlice(s.GetOffers(), func(o *pb.Offer) Offer { return Offer{Action: o.GetAction(), Args: raw(o.GetArgsJson())} }),
		Contexts: s.GetContexts(), Due: tm(s.GetDue()),
	}
	if h := s.GetExtractionHealth(); h != nil {
		out.Extraction = &ExtractionHealth{Pending: int(h.GetPending()), BudgetHit: h.GetBudgetHit(), Oldest: tm(h.GetOldest())}
	}
	return out
}

func draftEvidencePB(e Evidence) *pb.DraftEvidence {
	if e == (Evidence{}) {
		return nil
	}
	return &pb.DraftEvidence{Ref: e.Ref, Permalink: e.Permalink, At: ts(e.At)}
}

func draftEvidenceGo(e *pb.DraftEvidence) Evidence {
	return Evidence{Ref: e.GetRef(), Permalink: e.GetPermalink(), At: tm(e.GetAt())}
}

func draftPB(d Draft) *pb.Draft {
	o := d.Origin
	return &pb.Draft{
		Id: d.ID, Unit: unitPB(d.Unit), Items: d.Items, Task: d.Task, Kind: d.Kind, Purpose: d.Purpose,
		Delivery: d.Delivery, Effects: d.Effects, Target: d.Target, Text: d.Text, Reaction: d.Reaction,
		Replaced: d.Replaced, State: d.State, Evidence: draftEvidencePB(d.Evidence),
		Origin: &pb.DraftOrigin{Kind: o.Kind, TurnSeq: o.TurnSeq, Intent: o.Intent, Workflow: o.Workflow, Rule: o.Rule, Key: o.Key, Unit: unitPB(o.Unit)},
	}
}

func draftGo(d *pb.Draft) Draft {
	o := d.GetOrigin()
	return Draft{
		ID: d.GetId(), Unit: unitGo(d.GetUnit()), Items: d.GetItems(), Task: d.GetTask(), Kind: d.GetKind(), Purpose: d.GetPurpose(),
		Delivery: d.GetDelivery(), Effects: d.GetEffects(), Target: d.GetTarget(), Text: d.GetText(), Reaction: d.GetReaction(),
		Replaced: d.GetReplaced(), State: d.GetState(), Evidence: draftEvidenceGo(d.GetEvidence()),
		Origin: DraftOrigin{Kind: o.GetKind(), TurnSeq: o.GetTurnSeq(), Intent: o.GetIntent(), Workflow: o.GetWorkflow(), Rule: o.GetRule(), Key: o.GetKey(), Unit: unitGo(o.GetUnit())},
	}
}

func briefInputPB(in BriefInput) *pb.BriefInputResponse {
	return &pb.BriefInputResponse{Text: in.Text, Bytes: in.Bytes, Cut: in.Cut, PartsJson: in.Parts}
}

func briefInputGo(in *pb.BriefInputResponse) BriefInput {
	return BriefInput{Text: in.GetText(), Bytes: in.GetBytes(), Cut: in.GetCut(), Parts: raw(in.GetPartsJson())}
}

func briefRevisionPB(r BriefRevision) *pb.BriefRevisionResponse {
	return &pb.BriefRevisionResponse{Revision: r.Revision, PartsJson: r.Parts}
}

func briefRevisionGo(r *pb.BriefRevisionResponse) BriefRevision {
	return BriefRevision{Revision: r.GetRevision(), Parts: raw(r.GetPartsJson())}
}

func roundStatePB(st RoundState) *pb.RoundState {
	return &pb.RoundState{
		Marks: st.Marks, SentAt: ts(st.SentAt), AgentHome: st.AgentHome, FirstRoundSkipped: st.FirstRoundSkipped,
		Today: int64(st.Today), DayTotal: int64(st.DayTotal), Paused: st.Paused,
		Pending: mapSlice(st.Pending, func(p PendingRound) *pb.PendingRound {
			return &pb.PendingRound{Intent: p.Intent, Reason: p.Reason, Since: ts(p.Since), Deadline: ts(p.Deadline)}
		}),
	}
}

func roundStateGo(st *pb.RoundState) RoundState {
	out := RoundState{
		SentAt: tm(st.GetSentAt()), AgentHome: st.GetAgentHome(), FirstRoundSkipped: st.GetFirstRoundSkipped(),
		Today: int(st.GetToday()), DayTotal: int(st.GetDayTotal()), Paused: st.GetPaused(),
		Pending: mapSlice(st.GetPending(), func(p *pb.PendingRound) PendingRound {
			return PendingRound{Intent: p.GetIntent(), Reason: p.GetReason(), Since: tm(p.GetSince()), Deadline: tm(p.GetDeadline())}
		}),
	}
	if len(st.GetMarks()) > 0 {
		out.Marks = st.GetMarks()
	}
	return out
}

func placementPB(p Placement) *pb.Placement {
	return &pb.Placement{Repo: p.Repo, Checkout: p.Checkout, Role: p.Role, Shared: p.Shared}
}

func placementGo(p *pb.Placement) Placement {
	return Placement{Repo: p.GetRepo(), Checkout: p.GetCheckout(), Role: p.GetRole(), Shared: p.GetShared()}
}

func optString(p *string) *string {
	if p == nil {
		return nil
	}
	s := *p
	return &s
}

func itemOpPB(o ItemOp) *pb.ItemOp {
	out := &pb.ItemOp{
		Op: o.Op, Unit: unitPB(o.Unit), Item: o.Item, Anchor: anchorPB(o.Anchor), Assignees: peoplePB(o.Assignees),
		Basis: o.Basis, Resolution: o.Resolution, LinkKind: o.LinkKind, Reason: o.Reason,
	}
	if o.Evidence != nil {
		out.Evidence = &pb.ItemEvidence{Kind: o.Evidence.Kind, Anchor: anchorPB(o.Evidence.Anchor)}
	}
	if o.To != nil {
		out.To = &ppb.Unit{Provider: o.To.Provider, Key: o.To.Key}
	}
	if f := o.Fields; f != nil {
		out.Fields = &pb.ItemFields{
			Kind: optString(f.Kind), Summary: optString(f.Summary), Context: optString(f.Context), Deadline: optString(f.Deadline),
			Ambiguity: optString(f.Ambiguity), Category: optString(f.Category), Confidence: optString(f.Confidence),
		}
		if f.Options != nil {
			out.Fields.Options = &pb.ItemOptionList{Options: mapSlice(*f.Options, optionPB)}
		}
		if f.URLs != nil {
			out.Fields.Urls = &pb.StringList{Values: *f.URLs}
		}
		if f.Contexts != nil {
			out.Fields.Contexts = &pb.StringList{Values: *f.Contexts}
		}
		if f.Requester != nil {
			out.Fields.Requester = personPB(*f.Requester)
		}
		if f.WaitingOn != nil {
			out.Fields.WaitingOn = &pb.PersonList{People: peoplePB(*f.WaitingOn)}
		}
	}
	return out
}

func itemOpGo(o *pb.ItemOp) ItemOp {
	out := ItemOp{
		Op: o.GetOp(), Unit: unitGo(o.GetUnit()), Item: o.GetItem(), Anchor: anchorGo(o.GetAnchor()), Assignees: peopleGo(o.GetAssignees()),
		Basis: o.GetBasis(), Resolution: o.GetResolution(), LinkKind: o.GetLinkKind(), Reason: o.GetReason(),
	}
	if e := o.GetEvidence(); e != nil {
		ev := evidenceGo(e)
		out.Evidence = &ev
	}
	if o.GetTo() != nil {
		to := unitGo(o.GetTo())
		out.To = &to
	}
	if f := o.GetFields(); f != nil {
		out.Fields = &ItemFields{
			Kind: optString(f.Kind), Summary: optString(f.Summary), Context: optString(f.Context), Deadline: optString(f.Deadline),
			Ambiguity: optString(f.Ambiguity), Category: optString(f.Category), Confidence: optString(f.Confidence),
		}
		if f.GetOptions() != nil {
			opts := mapSlice(f.GetOptions().GetOptions(), optionGo)
			out.Fields.Options = &opts
		}
		if f.GetUrls() != nil {
			urls := f.GetUrls().GetValues()
			out.Fields.URLs = &urls
		}
		if f.GetContexts() != nil {
			cs := f.GetContexts().GetValues()
			out.Fields.Contexts = &cs
		}
		if f.GetRequester() != nil {
			p := personGo(f.GetRequester())
			out.Fields.Requester = &p
		}
		if f.GetWaitingOn() != nil {
			ps := peopleGo(f.GetWaitingOn().GetPeople())
			out.Fields.WaitingOn = &ps
		}
	}
	return out
}

func eventPB(e Event) *pb.Event {
	return &pb.Event{Unit: unitPB(e.Unit), Kind: e.Kind, Actor: e.Actor, At: ts(e.At), Anchor: anchorPB(e.Anchor), DataJson: e.Data}
}

func eventGo(e *pb.Event) Event {
	return Event{Unit: unitGo(e.GetUnit()), Kind: e.GetKind(), Actor: e.GetActor(), At: tm(e.GetAt()), Anchor: anchorGo(e.GetAnchor()), Data: raw(e.GetDataJson())}
}

func taskPB(t Task) *pb.Task {
	return &pb.Task{Id: t.ID, Title: t.Title, State: t.State, Category: t.Category, Parent: t.Parent, PropsJson: t.Props, Units: unitsPB(t.Units)}
}

func taskGo(t *pb.Task) Task {
	return Task{ID: t.GetId(), Title: t.GetTitle(), State: t.GetState(), Category: t.GetCategory(), Parent: t.GetParent(), Props: raw(t.GetPropsJson()), Units: unitsGo(t.GetUnits())}
}

func bookPB(b Book) *pb.Book {
	return &pb.Book{
		Unit: unitPB(b.Unit),
		Turns: mapSlice(b.Turns, func(t Turn) *pb.Turn {
			return &pb.Turn{Seq: t.Seq, Id: t.ID, Intent: t.Intent, Role: t.Role, Workflow: t.Workflow, State: t.State, Head: t.Head,
				Session: t.Session, SentAt: ts(t.SentAt), StartedAt: ts(t.StartedAt), EndedAt: ts(t.EndedAt)}
		}),
		Records: mapSlice(b.Records, func(r TurnRecord) *pb.TurnRecord {
			return &pb.TurnRecord{Id: r.ID, TurnSeq: r.TurnSeq, Head: r.Head, At: ts(r.At), RecordJson: r.Record}
		}),
	}
}

func bookGo(b *pb.Book) Book {
	return Book{
		Unit: unitGo(b.GetUnit()),
		Turns: mapSlice(b.GetTurns(), func(t *pb.Turn) Turn {
			return Turn{Seq: t.GetSeq(), ID: t.GetId(), Intent: t.GetIntent(), Role: t.GetRole(), Workflow: t.GetWorkflow(), State: t.GetState(),
				Head: t.GetHead(), Session: t.GetSession(), SentAt: tm(t.GetSentAt()), StartedAt: tm(t.GetStartedAt()), EndedAt: tm(t.GetEndedAt())}
		}),
		Records: mapSlice(b.GetRecords(), func(r *pb.TurnRecord) TurnRecord {
			return TurnRecord{ID: r.GetId(), TurnSeq: r.GetTurnSeq(), Head: r.GetHead(), At: tm(r.GetAt()), Record: raw(r.GetRecordJson())}
		}),
	}
}

func workspacePB(w Workspace) *pb.Workspace {
	return &pb.Workspace{Manager: w.Manager, Id: w.ID, Repo: w.Repo, Path: w.Path, Branch: w.Branch, PushBranch: w.PushBranch,
		Status: w.Status, IsMain: w.IsMain, Units: unitsPB(w.Units)}
}

func workspaceGo(w *pb.Workspace) Workspace {
	return Workspace{Manager: w.GetManager(), ID: w.GetId(), Repo: w.GetRepo(), Path: w.GetPath(), Branch: w.GetBranch(),
		PushBranch: w.GetPushBranch(), Status: w.GetStatus(), IsMain: w.GetIsMain(), Units: unitsGo(w.GetUnits())}
}

func gateRefGo(g *pb.GateRef) GateRef { return GateRef{Gate: g.GetGate(), Draft: g.GetDraft()} }
func gateRefPB(g GateRef) *pb.GateRef { return &pb.GateRef{Gate: g.Gate, Draft: g.Draft} }

func callPB(c Call) *pb.Call {
	p := pb.Priority_PRIORITY_USER
	if c.Priority == PriorityBackground {
		p = pb.Priority_PRIORITY_BACKGROUND
	}
	return &pb.Call{Priority: p, MeterScope: c.MeterScope}
}

func callGo(c *pb.Call) Call {
	out := Call{MeterScope: c.GetMeterScope()}
	if c.GetPriority() == pb.Priority_PRIORITY_BACKGROUND {
		out.Priority = PriorityBackground
	}
	return out
}
