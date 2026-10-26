package daemon

import (
	"testing"

	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
)

func TestByHandUnitUsesTheCodecs(t *testing.T) {
	t.Parallel()
	units := unit.NewRegistry(pr.Codec{}, unit.OpaqueCodec{ID: "slack"})
	for raw, want := range map[string]unit.Unit{
		"https://github.com/acme/api/pull/7": {Provider: unit.GitHub, Key: "acme/api#7"},
		"slack:T1/C1/1.2":                    {Provider: "slack", Key: "T1/C1/1.2"},
	} {
		if got, err := unitOfAddress(units, raw); err != nil || got != want {
			t.Errorf("%s: %v %v, want %v", raw, got, err, want)
		}
	}
	for _, raw := range []string{"https://example.com/x", "jira:X-1", ""} {
		if u, err := unitOfAddress(units, raw); err == nil {
			t.Errorf("%q resolved to %v", raw, u)
		}
	}
}
