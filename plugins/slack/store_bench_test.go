package slack

import (
	"context"
	"fmt"
	"github.com/wloophq/wloop/plugins/slack/store"
	"testing"
	"time"

	dslack "github.com/wloophq/wloop/plugins/slack/domain"
)

// scaleFacts are threads threads of perThread messages each, in 20 channels.
func scaleFacts(threads, perThread int) dslack.Facts {
	f := dslack.Facts{Team: team}
	for i := range threads {
		conv := fmt.Sprintf("C%04d", i%20)
		parent := i * 100
		var ms []dslack.Message
		ms = append(ms, msg(parent, "text"))
		for j := 1; j < perThread; j++ {
			m := reply(parent+j, parent, "text")
			m.Reactions = []dslack.Reaction{{Name: "eyes", Users: []string{"U0002"}}}
			ms = append(ms, m)
		}
		f.Threads = append(f.Threads, thread(conv, parent, ms...).Threads...)
	}
	return f
}

func benchStore(b *testing.B) *Store {
	db, err := store.Open(context.Background(), b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { db.Close() })
	return &Store{DB: db.Slack(), Now: func() time.Time { return t0 }}
}

func BenchmarkSaveSweep(b *testing.B) {
	ctx := context.Background()
	f := scaleFacts(500, 10)
	for _, c := range []struct {
		name string
		seed bool
	}{{"new", false}, {"unchanged", true}} {
		b.Run(c.name, func(b *testing.B) {
			for b.Loop() {
				b.StopTimer()
				st := benchStore(b)
				if c.seed {
					if err := st.Save(ctx, nil, f, "", t0); err != nil {
						b.Fatal(err)
					}
				}
				b.StartTimer()
				if err := st.Save(ctx, nil, f, "", t0); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkReads(b *testing.B) {
	ctx := context.Background()
	st := benchStore(b)
	if err := st.Save(ctx, nil, scaleFacts(500, 10), "", t0); err != nil {
		b.Fatal(err)
	}
	u := dslack.ThreadUnit(team, "C0007", ts(24700))
	b.Run("thread messages", func(b *testing.B) {
		for b.Loop() {
			if ms, err := st.Messages(ctx, u, "", 0); err != nil || len(ms) != 10 {
				b.Fatal(len(ms), err)
			}
		}
	})
	b.Run("watched due", func(b *testing.B) {
		st.Now = func() time.Time { return t0.Add(time.Hour) }
		for b.Loop() {
			if ws, err := st.Watched(ctx); err != nil || len(ws) != 500 {
				b.Fatal(len(ws), err)
			}
		}
	})
	b.Run("changes page of 500", func(b *testing.B) {
		for b.Loop() {
			if cs, err := st.Changes(ctx, 1000, 500); err != nil || len(cs) != 500 {
				b.Fatal(len(cs), err)
			}
		}
	})
}
