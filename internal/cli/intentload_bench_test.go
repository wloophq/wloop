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
		c, err := turn.NewCache(func() []profile.Layer { return profile.Chain(config.Config{}) }, intentload.CacheOptions{Recheck: time.Hour, Shared: &intentload.Shared{}})
		if err != nil {
			b.Fatal(err)
		}
		c.Get()
	}
}
