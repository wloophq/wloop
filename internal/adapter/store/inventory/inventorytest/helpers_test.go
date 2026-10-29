package inventorytest

import (
	"time"

	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/usage"
)

var runAt = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

func fullRun() dinv.BriefRun {
	return dinv.BriefRun{PR: ref, Trigger: "wanted", Intake: 4, Version: "b2-abc", AttemptOf: "b2-abc h1 2026-10-18T11:00:00Z 3", HeadOid: "h1",
		BodyEditedAt: runAt.Add(-time.Hour), ChangedFiles: 3, StartedAt: runAt}
}

func ptr[T any](v T) *T { return &v }

func endOK(r dinv.BriefRun, id int64) dinv.BriefRun {
	r.ID, r.EndedAt = id, runAt.Add(9*time.Second)
	r.InputMS, r.CallMS, r.AgentMS = ptr[int64](800), ptr[int64](8200), ptr[int64](7900)
	r.Outcome, r.Model, r.CostUSD = "ok", "haiku", ptr(0.011)
	r.Tokens = &usage.Tokens{Input: 1200, Output: 300, CacheRead: 9000, CacheWrite: 40}
	r.DiffBytes, r.DiffCut = ptr(51200), true
	r.Brief = `{"goal":"x","tags":["change:fix","area:store"]}`
	r.Files, r.Tags, r.DroppedTags = []string{"a.go", "b.go"}, []string{"change:fix", "area:store"}, []string{"vibe:good"}
	return r
}

const eventColumns = `provider, unit_key, kind, key, actor, at, data`
