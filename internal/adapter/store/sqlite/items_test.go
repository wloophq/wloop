package sqlite

import (
	"context"
	"reflect"
	"slices"
	"testing"
	"time"

	coreitems "github.com/wloophq/wloop/internal/core/items"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/items"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func init() {
	reads = append(reads, "DB.Items", "itemStore.Load", "itemStore.Query", "itemStore.Proposals", "itemStore.Log")
	writes = append(writes,
		writeCase{
			method: "itemStore.Commit",
			call: func(ctx context.Context, e *noteEnv) error {
				return e.db.Items().Commit(ctx, coreitems.Commit{Items: []items.Item{sampleItem(slackThread, "i1", noteAt)}})
			},
			topics: port.TopicItems, units: []unit.Unit{slackThread},
		},
		writeCase{
			method: "itemStore.SetProposal",
			seed: func(t *testing.T, e *noteEnv) {
				op := items.Op{Op: items.OpReopen, Unit: ghPull, Item: "i1", Anchor: unit.Anchor{Unit: ghPull, Ref: "m:1"}}
				p := coreitems.Proposal{Unit: ghPull, Item: "i1", Op: op, Provenance: items.Turn, State: coreitems.ProposalOpen, At: noteAt}
				if err := e.db.Items().Commit(context.Background(), coreitems.Commit{Proposals: []coreitems.Proposal{p}}); err != nil {
					t.Fatal(err)
				}
			},
			call: func(ctx context.Context, e *noteEnv) error {
				_, err := e.db.Items().SetProposal(ctx, 1, coreitems.ProposalRejected)
				return err
			},
			topics: port.TopicItems, units: []unit.Unit{ghPull},
		},
	)
	hotPlans = append(hotPlans,
		hotPlan{name: "items of a unit", query: itemsOfUnitSQL, args: 2, want: []string{"SEARCH items USING PRIMARY KEY (provider=? AND unit_key=?)"},
			tempSort: "USE TEMP B-TREE FOR ORDER BY"},
		hotPlan{name: "open items", query: itemsQuerySQL + " WHERE state = ?" + itemsOrder, args: 1, want: []string{"SEARCH items USING INDEX items_state (state=?)"},
			tempSort: "USE TEMP B-TREE FOR LAST 2 TERMS OF ORDER BY"},
		hotPlan{name: "item ops of a unit", query: itemOpsSQL, args: 3, want: []string{"SEARCH item_ops USING INDEX item_ops_unit (provider=? AND unit_key=? AND at>?)"},
			tempSort: "USE TEMP B-TREE FOR ORDER BY"},
		hotPlan{name: "open proposals", query: proposalsOfState, args: 1, want: []string{"item_proposals_state (state=?)"}},
		hotPlan{name: "an open proposal by its dedupe key", query: `SELECT id FROM item_proposals WHERE dedupe = ? AND state = 'open'`, args: 1,
			want: []string{"item_proposals_open (dedupe=?)"}},
	)
}

var (
	slackThread = unit.Unit{Provider: "slack", Key: "C000AAA/1.2"}
	ghPull      = unit.Unit{Provider: unit.GitHub, Key: "o/r#1"}
)

func sampleItem(u unit.Unit, id string, at time.Time) items.Item {
	return items.Item{ID: id, Unit: u, Kind: items.Request, Summary: "review " + id, Context: "ctx",
		Options:   []items.Option{{Label: "a", ArguedBy: []items.Person{{Key: "slack:U000BBB"}}}},
		Origin:    unit.Anchor{Unit: u, Ref: "m:1", At: at},
		Requester: items.Person{Key: "slack:U000BBB", Name: "b"}, Basis: items.NoBasis, State: items.Open,
		Trail:      []items.Evidence{{Kind: items.EvidenceSentDraft, Anchor: unit.Anchor{Unit: u, Ref: "draft:1:reply"}}},
		Confidence: items.High, Provenance: items.Extracted, Confirmed: true, UserAt: at, CreatedAt: at, UpdatedAt: at}
}

func TestItemsStoreRoundTrips(t *testing.T) {
	ctx := context.Background()
	db, path := openTemp(t)
	at := time.Date(2026, 10, 22, 12, 0, 0, 500, time.UTC)
	it := sampleItem(slackThread, "i1", at)
	op := items.Op{Op: items.OpUpdate, Unit: slackThread, Item: "i1", Anchor: unit.Anchor{Unit: slackThread, Ref: "m:2"}, Fields: &items.Fields{Summary: ptrTo("x")}}
	gen := db.log.(interface{ Gen() port.Generation })
	before := gen.Gen()
	err := db.Items().Commit(ctx, coreitems.Commit{
		Items:     []items.Item{it},
		Log:       []coreitems.LogEntry{{Unit: slackThread, Item: "i1", Op: op, Provenance: items.Extracted, Outcome: "proposed", Reason: "confirmed", At: at}},
		Proposals: []coreitems.Proposal{{Unit: slackThread, Item: "i1", Op: op, Provenance: items.Extracted, State: coreitems.ProposalOpen, At: at}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if gen.Gen() == before {
		t.Error("the commit noted nothing")
	}
	logOnly := gen.Gen()
	if err := db.Items().Commit(ctx, coreitems.Commit{Log: []coreitems.LogEntry{{Unit: slackThread, Item: "i1", Op: op, Provenance: items.Extracted, Outcome: "noop", At: at}}}); err != nil {
		t.Fatal(err)
	}
	if gen.Gen() != logOnly {
		t.Error("a log-only commit noted a change")
	}
	db.Close()
	db2, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	s := db2.Items()
	got, err := s.Load(ctx, []unit.Unit{slackThread, ghPull})
	if err != nil || len(got) != 1 || !reflect.DeepEqual(got[slackThread], []items.Item{it}) {
		t.Fatalf("load %v:\n%+v\nwant\n%+v", err, got[slackThread], it)
	}
	log, err := s.Log(ctx, slackThread, at)
	if err != nil || len(log) != 2 || log[0].Outcome != "proposed" || log[0].Reason != "confirmed" || !log[0].At.Equal(at) || *log[0].Op.Fields.Summary != "x" {
		t.Fatalf("log %+v %v", log, err)
	}
	if later, _ := s.Log(ctx, slackThread, at.Add(time.Nanosecond)); len(later) != 0 {
		t.Errorf("since after: %+v", later)
	}
	open, err := s.Proposals(ctx, coreitems.ProposalOpen)
	if err != nil || len(open) != 1 || open[0].ID == 0 || open[0].Unit != slackThread || !open[0].At.Equal(at) {
		t.Fatalf("proposals %+v %v", open, err)
	}
	if err := s.Commit(ctx, coreitems.Commit{Proposals: []coreitems.Proposal{{Unit: slackThread, Item: "i1", Op: op, Provenance: items.Turn, State: coreitems.ProposalOpen, At: at}}}); err != nil {
		t.Fatal(err)
	}
	if again, _ := s.Proposals(ctx, coreitems.ProposalOpen); len(again) != 1 {
		t.Errorf("a duplicate open proposal: %+v", again)
	}
	p, err := s.SetProposal(ctx, open[0].ID, coreitems.ProposalRejected)
	if err != nil || p.State != coreitems.ProposalRejected {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := s.SetProposal(ctx, open[0].ID, coreitems.ProposalAccepted); err == nil {
		t.Error("settled a proposal twice")
	}
	settle := coreitems.Commit{Items: []items.Item{sampleItem(ghPull, "i9", at)}, Proposals: []coreitems.Proposal{{ID: open[0].ID, Unit: slackThread, State: coreitems.ProposalAccepted}}}
	if err := s.Commit(ctx, settle); err == nil {
		t.Error("a commit settled a proposal that is not open")
	}
	if got, _ := s.Load(ctx, []unit.Unit{ghPull}); len(got[ghPull]) != 0 {
		t.Error("the refused commit wrote its items")
	}
	if _, err := s.SetProposal(ctx, 99, coreitems.ProposalRejected); err == nil {
		t.Error("set an unknown proposal")
	}
}

func TestQueryByWaitingOn(t *testing.T) {
	ctx := context.Background()
	db, _ := openTemp(t)
	at := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	mine := sampleItem(ghPull, "i1", at)
	mine.WaitingOn = []items.Person{{Key: "github:Me"}}
	alias := sampleItem(slackThread, "i1", at)
	alias.WaitingOn = []items.Person{{Key: "slack:U000AAA", Aliases: []string{"github:me"}}}
	resolved := sampleItem(slackThread, "i2", at)
	resolved.WaitingOn, resolved.State = []items.Person{{Key: "github:me"}}, items.Resolved
	other := sampleItem(slackThread, "i3", at)
	other.WaitingOn = []items.Person{{Key: "slack:u000aaa"}, {Key: "github:someone"}}
	nobody := sampleItem(ghPull, "i2", at)
	if err := db.Items().Commit(ctx, coreitems.Commit{Items: []items.Item{mine, alias, resolved, other, nobody}}); err != nil {
		t.Fatal(err)
	}
	got, err := db.Items().Query(ctx, coreitems.Query{WaitingOn: []string{"github:me", "slack:U000ZZZ"}, State: items.Open})
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, it := range got {
		keys = append(keys, it.Unit.String()+" "+it.ID)
	}
	if !slices.Equal(keys, []string{"github:o/r#1 i1", "slack:C000AAA/1.2 i1"}) {
		t.Errorf("got %v", keys)
	}
	got, _ = db.Items().Query(ctx, coreitems.Query{Providers: []unit.ProviderID{"slack"}, State: items.Open})
	if len(got) != 2 {
		t.Errorf("slack open: %d", len(got))
	}
	got, _ = db.Items().Query(ctx, coreitems.Query{Units: []unit.Unit{ghPull}})
	if len(got) != 2 {
		t.Errorf("of the ghPull request: %d", len(got))
	}
}

func ptrTo[T any](v T) *T { return &v }
