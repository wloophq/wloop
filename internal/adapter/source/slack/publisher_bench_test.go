package slack

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
)

// BenchmarkLandingUnitOf is a session send's lookup over 500 stored threads of 10 messages.
func BenchmarkLandingUnitOf(b *testing.B) {
	ctx := context.Background()
	db, err := sqlite.Open(filepath.Join(b.TempDir(), "store.db"), nil)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { db.Close() })
	if err := (&Store{DB: db.Slack()}).Save(ctx, nil, scaleFacts(500, 10), "", t0); err != nil {
		b.Fatal(err)
	}
	l := LandingOver(db)
	for name, parent := range map[string]string{"tracked": ts(24700), "untracked": ts(99999999)} {
		b.Run(name, func(b *testing.B) {
			for b.Loop() {
				if _, _, err := l.UnitOf(ctx, team, "C0007", ts(1), parent); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
