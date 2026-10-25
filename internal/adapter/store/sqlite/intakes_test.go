package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/core/intake"
	"github.com/wloophq/wloop/internal/core/intake/intaketest"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/domain/event"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/turn"
)

var (
	intakeRef = pr.Ref{Owner: "acme", Repo: "api", Number: 7}
	intakeAt  = time.Date(2026, 10, 18, 13, 0, 0, 123, time.UTC)
)

func newIntakeDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "inv.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func newIntake(r pr.Ref, intent turn.Intent) intake.Intake {
	return intake.Intake{Unit: r.Unit(), Intent: intent, Source: "cli", Request: []byte(`{"a":1}`), State: intake.Accepted, AcceptedAt: intakeAt}
}

func accepted(i intake.Intake) port.Event {
	return port.Event{Unit: i.Unit, Kind: intake.KindAccepted, At: i.AcceptedAt}
}

type intakeBackend struct {
	intake.Store
	t  *testing.T
	db *DB
}

func (b intakeBackend) Events() []port.Event {
	b.t.Helper()
	rows, err := b.db.R().QueryContext(context.Background(), `SELECT provider, unit_key, kind, key, at, data FROM events WHERE substr(kind, 1, 7) = 'intake_' ORDER BY id`)
	if err != nil {
		b.t.Fatal(err)
	}
	defer rows.Close()
	var out []port.Event
	for rows.Next() {
		var ev port.Event
		var at string
		if err := rows.Scan(&ev.Unit.Provider, &ev.Unit.Key, &ev.Kind, &ev.Key, &at, &ev.Data); err != nil {
			b.t.Fatal(err)
		}
		if ev.At, err = time.Parse(event.TimeLayout, at); err != nil {
			b.t.Fatal(err)
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		b.t.Fatal(err)
	}
	return out
}

func (b intakeBackend) Put(i intake.Intake) intake.Intake {
	b.t.Helper()
	got, _, err := b.Store.Accept(context.Background(), i)
	if err != nil {
		b.t.Fatal(err)
	}
	return got
}

func newIntakeBackend(t *testing.T) intakeBackend {
	db := newIntakeDB(t)
	return intakeBackend{Store: db.Intakes(), t: t, db: db}
}

func kindsOf(evs []port.Event, kind string) []port.Event {
	var out []port.Event
	for _, ev := range evs {
		if ev.Kind == kind {
			out = append(out, ev)
		}
	}
	return out
}

func TestIntakeStoreContract(t *testing.T) {
	t.Parallel()
	stores := map[string]func(t *testing.T) intake.Store{
		"fake":   func(*testing.T) intake.Store { return intaketest.NewStore() },
		"sqlite": func(t *testing.T) intake.Store { return newIntakeBackend(t) },
	}
	cases := map[string]func(t *testing.T, s intake.Store){
		"AcceptJoinsTheOpenIntake": func(t *testing.T, s intake.Store) {
			ctx := context.Background()
			first, joined, err := s.Accept(ctx, newIntake(intakeRef, turn.Intent(dintent.Review)), accepted(newIntake(intakeRef, turn.Intent(dintent.Review))))
			if err != nil || joined || first.Seq == 0 {
				t.Fatalf("%+v %v %v", first, joined, err)
			}
			second, joined, err := s.Accept(ctx, newIntake(intakeRef, turn.Intent(dintent.Review)), accepted(newIntake(intakeRef, turn.Intent(dintent.Review))))
			if err != nil || !joined || second.Seq != first.Seq {
				t.Fatalf("%+v %v %v", second, joined, err)
			}
			other, joined, err := s.Accept(ctx, newIntake(intakeRef, turn.Intent(dintent.Address)), accepted(newIntake(intakeRef, turn.Intent(dintent.Address))))
			if err != nil || joined || other.Seq == first.Seq {
				t.Fatalf("another intent: %+v %v %v", other, joined, err)
			}
			elsewhere, joined, err := s.Accept(ctx, newIntake(pr.Ref{Owner: "acme", Repo: "api", Number: 8}, turn.Intent(dintent.Review)))
			if err != nil || joined || elsewhere.Seq == first.Seq {
				t.Fatalf("another pull request: %+v %v %v", elsewhere, joined, err)
			}
		},
		"AStartedIntakeNoLongerJoins": func(t *testing.T, s intake.Store) {
			ctx := context.Background()
			first, _, err := s.Accept(ctx, newIntake(intakeRef, turn.Intent(dintent.Review)))
			if err != nil {
				t.Fatal(err)
			}
			first.State, first.EndedAt = intake.Started, intakeAt.Add(time.Minute)
			if err := s.Save(ctx, first); err != nil {
				t.Fatal(err)
			}
			second, joined, err := s.Accept(ctx, newIntake(intakeRef, turn.Intent(dintent.Review)))
			if err != nil || joined || second.Seq == first.Seq {
				t.Fatalf("%+v %v %v", second, joined, err)
			}
		},
		"SaveRoundTripsEveryField": func(t *testing.T, s intake.Store) {
			ctx := context.Background()
			got, _, err := s.Accept(ctx, newIntake(intakeRef, turn.Intent(dintent.Review)))
			if err != nil {
				t.Fatal(err)
			}
			want := got
			want.Request = []byte("guidance:be brief")
			want.State, want.Brief, want.Note = intake.Failed, intake.BriefWithout, "a note"
			want.BriefingAt, want.StartingAt, want.EndedAt = intakeAt.Add(time.Second), intakeAt.Add(2*time.Second), intakeAt.Add(3*time.Second)
			want.TurnSeq, want.TurnID, want.Error = 11, "r-11", "boom"
			want.Log = []string{"one", "two \"quoted\"", "line\nbreak"}
			if err := s.Save(ctx, want); err != nil {
				t.Fatal(err)
			}
			read, ok, err := s.Intake(ctx, want.Seq)
			if err != nil || !ok {
				t.Fatal(ok, err)
			}
			if !sameIntake(read, want) {
				t.Fatalf("got  %+v\nwant %+v", read, want)
			}
			if _, ok, err := s.Intake(ctx, want.Seq+100); err != nil || ok {
				t.Fatalf("an unknown seq: %v %v", ok, err)
			}
		},
		"OpenListsOnlyOpenStates": func(t *testing.T, s intake.Store) {
			ctx := context.Background()
			var seqs []int64
			for n, state := range []intake.State{intake.Accepted, intake.Briefing, intake.Starting, intake.Started, intake.Failed, intake.Canceled} {
				i, _, err := s.Accept(ctx, newIntake(pr.Ref{Owner: "acme", Repo: "api", Number: 100 + n}, turn.Intent(dintent.Review)))
				if err != nil {
					t.Fatal(err)
				}
				i.State = state
				if err := s.Save(ctx, i); err != nil {
					t.Fatal(err)
				}
				seqs = append(seqs, i.Seq)
			}
			open, err := s.Open(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var got []int64
			for _, i := range open {
				got = append(got, i.Seq)
			}
			if !slices.Equal(got, seqs[:3]) {
				t.Fatalf("open %v, want %v", got, seqs[:3])
			}
		},
		"SaveOfAnUnknownIntakeFails": func(t *testing.T, s intake.Store) {
			i := newIntake(intakeRef, turn.Intent(dintent.Review))
			i.Seq = 99
			if err := s.Save(context.Background(), i); err == nil {
				t.Fatal("saved an intake that was never accepted")
			}
		},
		"AcceptWritesEveryEventKeyedBySeq": func(t *testing.T, s intake.Store) {
			i := newIntake(intakeRef, turn.Intent(dintent.Review))
			got, _, err := s.Accept(context.Background(), i, accepted(i), port.Event{Unit: i.Unit, Kind: intake.KindBriefing, At: intakeAt})
			if err != nil {
				t.Fatal(err)
			}
			evs := s.(intaketest.Backend).Events()
			if len(evs) != 2 {
				t.Fatalf("%+v", evs)
			}
			for _, ev := range evs {
				if ev.Key != strconv.FormatInt(got.Seq, 10) {
					t.Fatalf("%s is keyed %q, want %d", ev.Kind, ev.Key, got.Seq)
				}
			}
		},
		"AJoinWritesNoEvent": func(t *testing.T, s intake.Store) {
			i := newIntake(intakeRef, turn.Intent(dintent.Review))
			if _, _, err := s.Accept(context.Background(), i, accepted(i)); err != nil {
				t.Fatal(err)
			}
			if _, joined, err := s.Accept(context.Background(), i, accepted(i)); err != nil || !joined {
				t.Fatal(joined, err)
			}
			if evs := s.(intaketest.Backend).Events(); len(evs) != 1 {
				t.Fatalf("%+v", evs)
			}
		},
		"SaveWritesItsEvents": func(t *testing.T, s intake.Store) {
			ctx := context.Background()
			got, _, err := s.Accept(ctx, newIntake(intakeRef, turn.Intent(dintent.Review)))
			if err != nil {
				t.Fatal(err)
			}
			got.State = intake.Briefing
			if err := s.Save(ctx, got, port.Event{Unit: got.Unit, Kind: intake.KindBriefing, Key: "1", At: intakeAt}); err != nil {
				t.Fatal(err)
			}
			if evs := kindsOf(s.(intaketest.Backend).Events(), intake.KindBriefing); len(evs) != 1 {
				t.Fatalf("%+v", evs)
			}
			got.State = intake.Starting
			if err := s.Save(ctx, got, port.Event{Unit: got.Unit, Kind: intake.KindStarting}); err == nil {
				t.Fatal("saved an event without a time")
			}
			if read, _, _ := s.Intake(ctx, got.Seq); read.State != intake.Briefing {
				t.Fatalf("the failed save changed the row to %s", read.State)
			}
		},
		"SaveRefusesASecondOpenIntake": func(t *testing.T, s intake.Store) {
			ctx := context.Background()
			first, _, err := s.Accept(ctx, newIntake(intakeRef, turn.Intent(dintent.Review)))
			if err != nil {
				t.Fatal(err)
			}
			first.State = intake.Started
			if err := s.Save(ctx, first); err != nil {
				t.Fatal(err)
			}
			second, _, err := s.Accept(ctx, newIntake(intakeRef, turn.Intent(dintent.Review)))
			if err != nil {
				t.Fatal(err)
			}
			first.State = intake.Briefing
			if err := s.Save(ctx, first); err == nil {
				t.Fatalf("reopened intake %d beside the open %d", first.Seq, second.Seq)
			}
		},
		"ConcurrentAcceptsOpenOneIntake": func(t *testing.T, s intake.Store) {
			const n = 12
			var wg sync.WaitGroup
			var mu sync.Mutex
			var created int
			seqs := map[int64]bool{}
			for range n {
				wg.Add(1)
				go func() {
					defer wg.Done()
					i := newIntake(intakeRef, turn.Intent(dintent.Review))
					got, joined, err := s.Accept(context.Background(), i, accepted(i))
					mu.Lock()
					defer mu.Unlock()
					if err != nil {
						t.Error(err)
						return
					}
					if !joined {
						created++
					}
					seqs[got.Seq] = true
				}()
			}
			wg.Wait()
			open, err := s.Open(context.Background())
			if err != nil || created != 1 || len(seqs) != 1 || len(open) != 1 {
				t.Fatalf("created %d, seqs %v, open %d, %v", created, seqs, len(open), err)
			}
			if evs := s.(intaketest.Backend).Events(); len(evs) != 1 {
				t.Fatalf("%d events", len(evs))
			}
		},
		"ACanceledContextWritesNothing": func(t *testing.T, s intake.Store) {
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, _, err := s.Accept(ctx, newIntake(intakeRef, turn.Intent(dintent.Review))); !errors.Is(err, context.Canceled) {
				t.Fatalf("got %v", err)
			}
			open, err := s.Open(context.Background())
			if err != nil || len(open) != 0 {
				t.Fatalf("%v %v", open, err)
			}
		},
	}
	for sname, mk := range stores {
		for cname, run := range cases {
			t.Run(sname+"/"+cname, func(t *testing.T) {
				t.Parallel()
				run(t, mk(t))
			})
		}
	}
}

func sameIntake(a, b intake.Intake) bool {
	return a.Seq == b.Seq && a.Unit == b.Unit && a.Intent == b.Intent && a.Source == b.Source && string(a.Request) == string(b.Request) &&
		a.State == b.State && a.Brief == b.Brief && a.Note == b.Note &&
		a.AcceptedAt.Equal(b.AcceptedAt) && a.BriefingAt.Equal(b.BriefingAt) && a.StartingAt.Equal(b.StartingAt) && a.EndedAt.Equal(b.EndedAt) &&
		a.TurnSeq == b.TurnSeq && a.TurnID == b.TurnID && a.Error == b.Error && slices.Equal(a.Log, b.Log)
}

func TestIntakeServiceOnSqlite(t *testing.T) {
	t.Parallel()
	intaketest.Service(t, func(t *testing.T) intake.Store { return newIntakeBackend(t) })
}

func TestAnIntakeSurvivesARestart(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "inv.db")
	db, err := Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	got, _, err := db.Intakes().Accept(ctx, newIntake(intakeRef, turn.Intent(dintent.Review)))
	if err != nil {
		t.Fatal(err)
	}
	got.State, got.Brief, got.Note = intake.Briefing, intake.BriefReady, "waiting"
	got.Request = []byte("guidance:keep")
	got.BriefingAt = intakeAt.Add(time.Second)
	got.Log = []string{"waiting", "again"}
	if err := db.Intakes().Save(ctx, got); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	open, err := db.Intakes().Open(ctx)
	if err != nil || len(open) != 1 || !sameIntake(open[0], got) {
		t.Fatalf("%+v %v, want %+v", open, err, got)
	}
}

func TestAcceptRefusesAnIntakeWithoutAcceptedTime(t *testing.T) {
	t.Parallel()
	db := newIntakeDB(t)
	i := newIntake(intakeRef, turn.Intent(dintent.Review))
	i.AcceptedAt = time.Time{}
	if _, _, err := db.Intakes().Accept(context.Background(), i); err == nil {
		t.Fatal("accepted an intake with no time")
	}
}

func TestACorruptTimeFailsTheRead(t *testing.T) {
	t.Parallel()
	db := newIntakeDB(t)
	ctx := context.Background()
	got, _, err := db.Intakes().Accept(ctx, newIntake(intakeRef, turn.Intent(dintent.Review)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := execW(db, `UPDATE intakes SET starting_at = 'yesterday' WHERE seq = ?`, got.Seq); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.Intakes().Intake(ctx, got.Seq); err == nil {
		t.Fatal("read a corrupt time")
	}
}

func TestAFailedAcceptWritesNeitherRowNorEvent(t *testing.T) {
	t.Parallel()
	b := newIntakeBackend(t)
	i := newIntake(intakeRef, turn.Intent(dintent.Review))
	if _, _, err := b.Accept(context.Background(), i, port.Event{Unit: i.Unit, Kind: intake.KindAccepted}); err == nil {
		t.Fatal("accepted an event without a time")
	}
	open, err := b.Open(context.Background())
	if err != nil || len(open) != 0 || len(b.Events()) != 0 {
		t.Fatalf("%v %v", open, err)
	}
}
