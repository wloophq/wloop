package source

import (
	"context"
	"time"

	"github.com/wloophq/wloop/internal/brief"
	"github.com/wloophq/wloop/internal/domain/unit"
	src "github.com/wloophq/wloop/pkg/plugin/source"
)

// Briefs is a source plugin's Briefer as the brief queue reads it.
type Briefs struct {
	ID   string
	Kind *Kind
}

var _ brief.BriefSource = Briefs{}

func (b Briefs) Input(ctx context.Context, u unit.Unit) (brief.UnitInput, error) {
	c, err := b.Kind.Client(b.ID)
	if err != nil {
		return brief.UnitInput{}, err
	}
	in, err := c.BriefInput(ctx, Wire(u))
	return brief.UnitInput{Text: in.Text, Bytes: int(in.Bytes), Cut: in.Cut, Parts: in.Parts}, err
}

func (b Briefs) Revision(ctx context.Context, u unit.Unit) (brief.At, error) {
	c, err := b.Kind.Client(b.ID)
	if err != nil {
		return brief.At{}, err
	}
	r, err := c.BriefRevision(ctx, Wire(u))
	return atOf(r), err
}

// Describe is in's one line; "" when the plugin cannot say.
func (b Briefs) Describe(in brief.UnitInput) string {
	c, err := b.Kind.Client(b.ID)
	if err != nil {
		return ""
	}
	s, _ := c.BriefDescribe(context.Background(), src.BriefInput{Text: in.Text, Bytes: int64(in.Bytes), Cut: in.Cut, Parts: in.Parts})
	return s
}

// Stale says whether w is due again; a plugin that cannot say keeps it.
func (b Briefs) Stale(w brief.Written, cur brief.At, version string, now time.Time) bool {
	c, err := b.Kind.Client(b.ID)
	if err != nil {
		return false
	}
	stale, err := c.BriefStale(context.Background(), src.BriefStaleRequest{
		Written: src.BriefWritten{At: revisionOf(w.At), Version: w.Version, Error: w.Error,
			Failures: w.Failures, AttemptedAt: w.AttemptedAt, AttemptOf: w.AttemptOf},
		Current: revisionOf(cur), Version: version, Now: now})
	return err == nil && stale
}

func atOf(r src.BriefRevision) brief.At {
	return brief.At{Revision: r.Revision, Parts: r.Parts, Attempt: r.Attempt}
}

func revisionOf(a brief.At) src.BriefRevision {
	return src.BriefRevision{Revision: a.Revision, Parts: a.Parts, Attempt: a.Attempt}
}
