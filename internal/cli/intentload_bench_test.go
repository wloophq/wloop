package cli

import (
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/config"
	intentload "github.com/wloophq/wloop/internal/intent"
	"github.com/wloophq/wloop/internal/profile"
	"github.com/wloophq/wloop/internal/turn"
)

func BenchmarkIntentLoad(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		s, err := turn.NewShared()
		if err != nil {
			b.Fatal(err)
		}
		s.NewCache(func() []profile.Layer { return profile.Chain(config.Config{}) }, intentload.CacheOptions{Recheck: time.Hour}).Get()
	}
}
