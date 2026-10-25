package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/api/client"
)

// storeChecks are the store's schema version and newest backup, read from
// the files, so they hold with the daemon down.
func (e *Env) storeChecks() []check {
	path := e.storePath()
	s, err := sqlite.Inspect(path)
	var c check
	switch {
	case err != nil:
		c = check{markMissing, "store", err.Error(), ""}
	case s.Empty:
		c = check{markInfo, "store", "none yet; the daemon creates it", ""}
	default:
		switch s.State() {
		case sqlite.StateCurrent:
			c = check{markOK, "store", fmt.Sprintf("schema version %d", s.Version), ""}
		case sqlite.StateReadable:
			c = check{markInfo, "store", fmt.Sprintf("schema version %d, written by a newer release; this release reads it", s.Version), ""}
		case sqlite.StateOlder:
			c = check{markMissing, "store", fmt.Sprintf("schema version %d; this release migrates it to %d", s.Version, sqlite.SchemaVersion), migrateCommand}
		default:
			st := tooNewStatus(path, s)
			c = check{markMissing, "store", st.Message, st.Fix}
		}
	}
	return []check{c, e.backupCheck()}
}

func (e *Env) backupCheck() check {
	all, err := sqlite.Backups(e.backupsPath())
	if err != nil {
		return check{markInfo, "store backup", err.Error(), ""}
	}
	if len(all) == 0 {
		return check{markInfo, "store backup", "none yet; one is taken before each migration", ""}
	}
	ago := "just now"
	if age := e.Now().Sub(all[0].Taken); age >= time.Minute {
		ago = shortAge(age, false) + " ago"
	}
	return check{markInfo, "store backup", fmt.Sprintf("%s, %s (%d kept in %s)", filepath.Base(all[0].Path), ago, len(all), e.backupsPath()), ""}
}

// storeDown is the daemon down with a store this release cannot open, the
// likelier reason it is down.
type storeDown struct {
	error
	store check
}

func (s storeDown) Unwrap() error { return s.error }

func (s storeDown) Detail() string {
	d := "the store: " + s.store.info
	if s.store.fix != "" {
		d += "\nfix with: " + s.store.fix
	}
	return d
}

func (s storeDown) DetailSource() string { return "" }

// downWithStore adds the store's problem, when it has one, to the daemon
// being down.
func (e *Env) downWithStore(down error) error {
	if c := e.storeChecks()[0]; c.mark == markMissing {
		return storeDown{down, c}
	}
	return down
}

// waitingStore is the store problem a daemon serving only its health
// reports, as doctor's one section.
func (e *Env) waitingStore(ctx context.Context, c *client.Client) ([]checkSection, bool) {
	h, err := c.Health(ctx)
	if err != nil || h.Store == nil || h.Store.State == api.StoreOK {
		return nil, false
	}
	m := markMissing
	if h.Store.State == api.StoreBusy {
		m = markInfo
	}
	return []checkSection{{"Store", []check{{m, "store", h.Store.Message, h.Store.Fix}, e.backupCheck()}}}, true
}
