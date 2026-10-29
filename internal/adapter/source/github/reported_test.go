package ghsource

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/inventory/inventorytest"

	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/pr"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/readmodel/prrow"
	"github.com/wloophq/wloop/pkg/plugin"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// fixedReports are snapshots a plugin reported, by unit.
type fixedReports map[unit.Unit]src.Snapshot

func (f fixedReports) Units(_ context.Context, p unit.ProviderID) ([]unit.Unit, error) {
	var out []unit.Unit
	for u := range f {
		if u.Provider == p {
			out = append(out, u)
		}
	}
	return out, nil
}

func (f fixedReports) Reported(_ context.Context, units []unit.Unit) (map[unit.Unit]src.Snapshot, error) {
	out := map[unit.Unit]src.Snapshot{}
	for _, u := range units {
		if s, ok := f[u]; ok {
			out[u] = s
		}
	}
	return out, nil
}

// reportOf is the snapshot of p with detail d read at readAt in version v.
func reportOf(t *testing.T, p dinv.PR, d *inventory.Detail, v int, readAt time.Time) src.Snapshot {
	t.Helper()
	raw, err := json.Marshal(prrow.ReportedData{PR: p, Detail: d, DetailVersion: v, DetailReadAt: readAt})
	if err != nil {
		t.Fatal(err)
	}
	return src.Snapshot{Unit: wireOf(p.Ref.Unit()), Data: raw}
}

func wireOf(u unit.Unit) plugin.Unit { return plugin.Unit{Provider: string(u.Provider), Key: u.Key} }

func TestReportedInputsReadTheRowsAndFullReadsThePluginReported(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	if err := st.SetViewer(ctx, unit.GitHub, "ana"); err != nil {
		t.Fatal(err)
	}
	fresh, old := pr.Ref{Owner: "acme", Repo: "api", Number: 7}, pr.Ref{Owner: "acme", Repo: "api", Number: 8}
	gone, empty := pr.Ref{Owner: "acme", Repo: "api", Number: 9}, pr.Ref{Owner: "acme", Repo: "api", Number: 10}
	if err := inventorytest.Of(st).UpsertPR(ctx, dinv.PR{Ref: fresh, Title: "the host's copy", Author: "bruno", State: "CLOSED"}); err != nil {
		t.Fatal(err)
	}
	review := pr.Review{ID: "r1", State: pr.ReviewApproved, Author: "ana", SubmittedAt: now.Add(-time.Hour), CommitOid: "h7"}
	reports := fixedReports{
		fresh.Unit(): reportOf(t, dinv.PR{Ref: fresh, Title: "Batch export", Author: "bruno", State: "OPEN", HeadOid: "h7"},
			&inventory.Detail{AuthorAssociation: "MEMBER", Reviews: []pr.Review{review}}, inventory.DetailVersion, now.Add(-time.Minute)),
		old.Unit():   reportOf(t, dinv.PR{Ref: old, Author: "ana", State: "OPEN"}, &inventory.Detail{AuthorAssociation: "OWNER"}, inventory.DetailVersion-1, now),
		gone.Unit():  {Unit: wireOf(gone.Unit()), Gone: true},
		empty.Unit(): {Unit: wireOf(empty.Unit())},
	}
	in := ReportedInputs{Reports: reports, Store: st}
	rows, err := in.Rows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var refs []pr.Ref
	for _, r := range rows {
		refs = append(refs, r.Ref)
	}
	if !slices.Equal(refs, []pr.Ref{fresh, old}) || rows[0].Title != "Batch export" || rows[0].State != "OPEN" {
		t.Fatalf("rows %+v, want the reported ones, not the host's copy", rows)
	}
	viewer, err := in.Viewer(ctx)
	if err != nil || viewer != "ana" {
		t.Fatalf("viewer %q %v", viewer, err)
	}
	facts, err := in.Facts(ctx, rows, viewer)
	if err != nil {
		t.Fatal(err)
	}
	f := facts[fresh.Unit()]
	if !f.ReadAt.Equal(now.Add(-time.Minute)) || f.AuthorAssociation != "MEMBER" || len(f.Reviews) != 1 || !f.Reviews[0].ByViewer {
		t.Fatalf("the fresh full read %+v", f)
	}
	if o := facts[old.Unit()]; !o.ReadAt.IsZero() || o.AuthorAssociation != "OWNER" {
		t.Fatalf("a full read of another version %+v, want no ReadAt", o)
	}
	got, ok, err := in.PR(ctx, fresh)
	if err != nil || !ok || got.Title != "Batch export" {
		t.Fatalf("PR %+v %v %v", got, ok, err)
	}
	for _, ref := range []pr.Ref{gone, empty, {Owner: "acme", Repo: "api", Number: 11}} {
		if _, ok, err := in.PR(ctx, ref); err != nil || ok {
			t.Errorf("%s: reported %v %v", ref, ok, err)
		}
	}
}
