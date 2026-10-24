package slack

import (
	"context"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	dslack "github.com/wloophq/wloop/internal/domain/slack"
)

// Watches is the dslack.Watcher over slack_objects: a watched object the
// store lacks is read on the next pass.
type Watches struct{ DB *sqlite.SlackStore }

var _ dslack.Watcher = Watches{}

func (w Watches) Watch(ctx context.Context, obj dslack.ObjectRef, reason dslack.WatchReason) error {
	o, err := checkObject(obj)
	if err != nil {
		return err
	}
	return w.DB.SetWatched(ctx, o, true, string(reason))
}

func (w Watches) Unwatch(ctx context.Context, obj dslack.ObjectRef) error {
	o, err := checkObject(obj)
	if err != nil {
		return err
	}
	return w.DB.SetWatched(ctx, o, false, "")
}

func checkObject(obj dslack.ObjectRef) (string, error) {
	o := obj.Object()
	_, _, err := dslack.ParseObject(o)
	return o, err
}
