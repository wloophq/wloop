package value

import (
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/domain/workflow"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

func TestAReportedValueIsTheHostsFact(t *testing.T) {
	fri := time.Date(2026, 10, 2, 20, 0, 0, 0, time.UTC)
	if got := Fact(src.After(fri, 6*time.Hour, src.WorkingDays)); got != (workflow.Temporal{Kind: workflow.TemporalAfter, Time: fri,
		Threshold: 6 * time.Hour, WorkingDays: true}) {
		t.Errorf("after in working days: %#v", got)
	}
	if got := Fact(src.List()); got == nil || len(got.([]string)) != 0 {
		t.Errorf("an empty list: %#v", got)
	}
	mon := time.Date(2026, 10, 5, 3, 0, 0, 0, time.UTC)
	if Resolve(src.After(fri, 6*time.Hour, src.WorkingDays), mon) != true || Resolve(src.After(fri, 6*time.Hour, src.Calendar), mon) != true ||
		Resolve(src.After(fri, 6*time.Hour, src.WorkingDays), mon.Add(-2*time.Hour)) != false {
		t.Error("After resolves with the host's clock")
	}
	if Resolve(src.Since(time.Time{}), mon) != time.Duration(0) || Resolve(src.At(time.Time{}), mon) != false {
		t.Error("a zero time resolves to its type's zero")
	}
}
