package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/daemon"
	"github.com/wloophq/wloop/internal/domain/dossier"
)

const backupsDir = "backups"

var storeUsage = "usage: " + brand.Command + " store migrate [--dry-run] [--yes] | restore [--list] [<backup>] [--yes]"

var (
	migrateCommand = brand.Command + " store migrate"
	restoreCommand = brand.Command + " store restore"
)

// storeWaitEvery paces a daemon's checks of a store it cannot open yet.
var storeWaitEvery = 5 * time.Second

// storeReportWait bounds how long app install waits for the restarted
// daemon to say how it found the store.
var storeReportWait = 15 * time.Second

// storeLockWait bounds how long a store command waits for the daemon to
// let go of the store before stopping it.
var storeLockWait = 10 * time.Second

func (e *Env) storePath() string   { return filepath.Join(e.StateDir, storeFile) }
func (e *Env) backupsPath() string { return filepath.Join(e.StateDir, backupsDir) }

var describeSuffix = regexp.MustCompile(`-\d+-g[0-9a-f]+(-dirty)?$|-dirty$`)

// releaseBuild says version names a release, which migrates its store on
// its own; a build between tags or with local changes migrates only on
// request, so switching branches never moves the live store by surprise.
func releaseBuild(version string) bool {
	return strings.HasPrefix(version, "v") && !describeSuffix.MatchString(version)
}

// storeWaiting is why the daemon cannot open its store yet, and when to try
// again.
type storeWaiting struct {
	status api.StoreStatus
	ready  func() bool
}

// openStore opens the daemon's store, migrating it first on a release, and
// returns it with the release of its lock. Until the store can open, the
// daemon serves daemon.StoreWait; a nil store with no error is ctx ending.
func (e *Env) openStore(ctx context.Context, logf func(string, ...any)) (*sqlite.DB, func(), error) {
	for {
		// The daemon's lock, held while the store opens, keeps a second serve
		// from migrating it; Server.Run and StoreWait take it again.
		running, err := daemon.LockFile(filepath.Join(e.StateDir, daemonLock), daemon.ErrAlreadyRunning)
		if err != nil {
			return nil, nil, err
		}
		db, unlock, wait, err := e.tryStore(ctx, logf)
		running()
		if err != nil || db != nil {
			return db, unlock, err
		}
		logf("%s; serving only the health until that changes", daemon.StoreWarning(wait.status))
		if err := e.storeWait(ctx, wait); err != nil || ctx.Err() != nil {
			return nil, nil, err
		}
	}
}

func (e *Env) tryStore(ctx context.Context, logf func(string, ...any)) (*sqlite.DB, func(), storeWaiting, error) {
	path := e.storePath()
	unlock, err := e.waitStoreLock(ctx, path)
	if errors.Is(err, sqlite.ErrStoreBusy) {
		return nil, nil, storeWaiting{
			status: api.StoreStatus{Reads: sqlite.SchemaVersion, State: api.StoreBusy, Message: "a " + brand.Command + " store command has the store open"},
			ready:  func() bool { return storeLockFree(path) },
		}, nil
	}
	if err != nil {
		return nil, nil, storeWaiting{}, err
	}
	s, err := sqlite.Inspect(path)
	if err != nil {
		unlock()
		return nil, nil, e.storeFailed(path, s, err), nil
	}
	switch s.State() {
	case sqlite.StateOlder:
		if !releaseBuild(Version) {
			unlock()
			return nil, nil, e.waitFor(path, s, api.StoreStatus{
				State:   api.StoreNeedsMigration,
				Message: fmt.Sprintf("the store is at schema version %d and this build (%s) at %d; a build that is not a release migrates the store only on request", s.Version, Version, sqlite.SchemaVersion),
				Fix:     migrateCommand,
			}), nil
		}
		if err := e.migrateInDaemon(ctx, path, logf); err != nil {
			unlock()
			logf("store: %v", err)
			after, ierr := sqlite.Inspect(path)
			if ierr != nil {
				after = s
			}
			return nil, nil, e.storeFailed(path, after, err), nil
		}
	case sqlite.StateNewer, sqlite.StateUnknown:
		unlock()
		return nil, nil, e.waitFor(path, s, tooNewStatus(path, s)), nil
	}
	db, err := sqlite.Open(path, e.changes)
	if err != nil {
		unlock()
		var se sqlite.SchemaError
		if errors.As(err, &se) {
			return nil, nil, e.waitFor(path, se.Status, tooNewStatus(path, se.Status)), nil
		}
		return nil, nil, e.storeFailed(path, s, err), nil
	}
	if s, err = sqlite.Inspect(path); err != nil {
		db.Close()
		unlock()
		return nil, nil, storeWaiting{}, err
	}
	if s.Version >= taskRenamedAt {
		if err := renameDossierDirs(e.StateDir, "entry", "task"); err != nil {
			logf("store: %v; %s", err, dossierMoveFix(e.StateDir))
		}
		if err := e.applyDossierMoves(ctx, db); err != nil {
			logf("store: %v; %s", err, dossierMovesFix)
		}
	}
	st := e.storeOK(s)
	e.storeStatus = &st
	return db, unlock, storeWaiting{}, nil
}

func (e *Env) migrateInDaemon(ctx context.Context, path string, logf func(string, ...any)) error {
	p, err := sqlite.PlanMigration(path, e.backupsPath(), e.Now())
	if err != nil {
		return err
	}
	logf("store: migrating from version %d to %d; backup at %s", p.From, p.To, p.Backup)
	if _, err := sqlite.Migrate(ctx, path, sqlite.MigrateOptions{Backups: e.backupsPath(), Now: e.Now, Backup: p.Backup,
		Step: func(label string, took time.Duration) { logf("store: %s ok (%s)", label, took.Round(time.Millisecond)) },
		Warn: func(err error) { logf("store: %v", err) },
	}); err != nil {
		return err
	}
	logf("store: at version %d", p.To)
	if err := e.renameTaskDossiers(p.From, p.To); err != nil {
		logf("store: %v; %s", err, dossierMoveFix(e.StateDir))
	}
	return nil
}

// dossierMoveFix is what finishes the move of the entries' dossiers to the tasks' when it stopped.
func dossierMoveFix(stateDir string) string {
	root := dossier.Root(stateDir)
	return fmt.Sprintf("move each %s to %s, and each %s to %s, resolving a name both sides hold; %s retries the move at every start",
		filepath.Join(root, "entry", "e-<id>#"), filepath.Join(root, "task", "t-<id>#"),
		filepath.Join(root, dossier.ArchiveDir, "entry", "e-<id>#.tar.gz"), filepath.Join(root, dossier.ArchiveDir, "task", "t-<id>#.tar.gz"), brand.Name)
}

// dossierMovesFix is what finishes a dossier move a merge of tasks recorded when it could not be done.
var dossierMovesFix = "move the directory to the path named, and its archive under " + dossier.ArchiveDir + " likewise, resolving a name both sides hold; " + brand.Name + " retries the move at every start"

// dossierMove is a dossier directory's move: from and to are paths under the dossiers root.
type dossierMove struct{ from, to string }

// moveDossierDirs moves dossier directories under the dossiers root of stateDir, in the order of moves,
// so a move can carry what an earlier one put in place (a merge into a task that is merged next). Parents
// are created. An archived dossier moves the same way under the archive directory, as the path plus
// .tar.gz. A move whose source is gone counts as done, so a retry finishes what an interrupted run left. A
// move onto a path that exists stops the run there and is reported, since the moves after it may build on
// it.
func moveDossierDirs(stateDir string, moves []dossierMove) error {
	root := dossier.Root(stateDir)
	for _, m := range moves {
		for _, p := range [][2]string{
			{filepath.Join(root, m.from), filepath.Join(root, m.to)},
			{filepath.Join(root, dossier.ArchiveDir, m.from+".tar.gz"), filepath.Join(root, dossier.ArchiveDir, m.to+".tar.gz")},
		} {
			if err := moveOne(p[0], p[1]); err != nil {
				return err
			}
		}
	}
	return nil
}

func moveOne(src, dst string) error {
	if _, err := os.Lstat(src); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("moving the dossier %s: %s exists", src, dst)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	return os.Rename(src, dst)
}

// applyDossierMoves moves the dossier directories db records as moved, in the order they were recorded,
// and forgets each move once its source is gone.
func (e *Env) applyDossierMoves(ctx context.Context, db *sqlite.DB) error {
	recorded, err := db.DossierMoves(ctx)
	if err != nil || len(recorded) == 0 {
		return err
	}
	moves := make([]dossierMove, len(recorded))
	for i, m := range recorded {
		moves[i] = dossierMove{m.From, m.To}
	}
	err = moveDossierDirs(e.StateDir, moves)
	root := dossier.Root(e.StateDir)
	for _, m := range recorded {
		if !pathExists(filepath.Join(root, m.From)) && !pathExists(filepath.Join(root, dossier.ArchiveDir, m.From+".tar.gz")) {
			err = errors.Join(err, db.DossierMoved(ctx, m.Seq))
		}
	}
	return err
}

func pathExists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// pendingDossierMoves names the dossier moves db records and that are not done on disk.
func pendingDossierMoves(ctx context.Context, stateDir string, db *sqlite.DB) []check {
	moves, err := db.DossierMoves(ctx)
	if err != nil {
		return []check{{markInfo, "dossier moves", err.Error(), ""}}
	}
	root := dossier.Root(stateDir)
	var out []check
	for _, m := range moves {
		info := filepath.Join(root, m.From) + " waits to move to " + filepath.Join(root, m.To)
		if pathExists(filepath.Join(root, m.To)) {
			info += ", which exists"
		}
		out = append(out, check{markMissing, "dossiers", info, dossierMovesFix})
	}
	return out
}

// leftoverEntryDossiers names the directories of entries' dossiers a store at version 7 or later left
// behind, with the paths their move would take that exist already.
func leftoverEntryDossiers(stateDir string) []check {
	var out []check
	root := dossier.Root(stateDir)
	for _, base := range []string{root, filepath.Join(root, dossier.ArchiveDir)} {
		src := filepath.Join(base, "entry")
		ents, err := os.ReadDir(src)
		if err != nil {
			continue
		}
		info := src + " holds dossiers of entries, which are tasks now"
		for _, ent := range ents {
			name := ent.Name()
			if rest, ok := strings.CutPrefix(name, taskIDPrefixes["entry"]); ok {
				name = taskIDPrefixes["task"] + rest
			}
			target := filepath.Join(base, "task", name)
			if _, err := os.Lstat(target); err == nil {
				info += "; " + target + " exists"
			}
		}
		out = append(out, check{markMissing, "dossiers", info, dossierMoveFix(stateDir)})
	}
	return out
}

// taskRenamedAt is the schema version that made the entries tasks, and taskIDPrefixes the prefix of a
// task's id under each provider name a task had.
const taskRenamedAt = 7

var taskIDPrefixes = map[string]string{"entry": "e-", "task": "t-"}

// renameTaskDossiers moves the tasks' dossier directories along with a store that moved from version
// from to to across the rename of entries to tasks, either way.
func (e *Env) renameTaskDossiers(from, to uint32) error {
	switch {
	case from < taskRenamedAt && to >= taskRenamedAt:
		return renameDossierDirs(e.StateDir, "entry", "task")
	case from >= taskRenamedAt && to < taskRenamedAt:
		return renameDossierDirs(e.StateDir, "task", "entry")
	}
	return nil
}

// renameDossierDirs moves the dossiers and archived dossiers of provider from's units under stateDir to
// provider to, each task id taking to's prefix. A directory already at its new name is left alone.
func renameDossierDirs(stateDir, from, to string) error {
	root := dossier.Root(stateDir)
	for _, base := range []string{root, filepath.Join(root, dossier.ArchiveDir)} {
		src, dst := filepath.Join(base, from), filepath.Join(base, to)
		ents, err := os.ReadDir(src)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if err := os.MkdirAll(dst, 0o700); err != nil {
			return err
		}
		for _, ent := range ents {
			name := ent.Name()
			if rest, ok := strings.CutPrefix(name, taskIDPrefixes[from]); ok {
				name = taskIDPrefixes[to] + rest
			}
			target := filepath.Join(dst, name)
			if _, err := os.Lstat(target); err == nil {
				return fmt.Errorf("moving the dossier %s: %s exists", filepath.Join(src, ent.Name()), target)
			}
			if err := os.Rename(filepath.Join(src, ent.Name()), target); err != nil {
				return err
			}
		}
		if err := os.Remove(src); err != nil {
			return err
		}
	}
	return nil
}

func tooNewStatus(path string, s sqlite.Status) api.StoreStatus {
	st := api.StoreStatus{State: api.StoreTooNew, Fix: restoreCommand}
	if s.State() != sqlite.StateNewer {
		st.Message = sqlite.Refuse(path, s).Error()
		return st
	}
	st.Message = fmt.Sprintf("the store at %s is at schema version %d, newer than this release reads (%d); upgrade %s with `brew upgrade %s`, or go back to the store from before that upgrade", path, s.Version, sqlite.SchemaVersion, brand.Name, brand.Name)
	return st
}

// storeFailed waits on a store that failed to migrate or open. A store that
// opens as it is, or is damaged, gets past it only by a restore.
func (e *Env) storeFailed(path string, s sqlite.Status, err error) storeWaiting {
	fix := migrateCommand
	if s.Opens() || errors.Is(err, sqlite.ErrDamaged) || s.Version == 0 {
		fix = restoreCommand
	}
	return e.waitFor(path, s, api.StoreStatus{State: api.StoreFailed, Message: err.Error(), Fix: fix})
}

// waitFor waits until the store's version moves or the store is gone,
// which a store command or the user did, and its lock is free.
func (e *Env) waitFor(path string, s sqlite.Status, st api.StoreStatus) storeWaiting {
	st.Version, st.Reads = s.Version, sqlite.SchemaVersion
	e.addBackup(&st)
	return storeWaiting{status: st, ready: func() bool {
		if !storeLockFree(path) {
			return false
		}
		now, err := sqlite.Inspect(path)
		return err == nil && now != s
	}}
}

func storeLockFree(path string) bool {
	unlock, err := sqlite.LockStore(path)
	if err == nil {
		unlock()
	}
	return err == nil
}

func (e *Env) storeOK(s sqlite.Status) api.StoreStatus {
	st := api.StoreStatus{Version: s.Version, Reads: sqlite.SchemaVersion, State: api.StoreOK}
	e.addBackup(&st)
	return st
}

func (e *Env) addBackup(st *api.StoreStatus) {
	if all, err := sqlite.Backups(e.backupsPath()); err == nil && len(all) > 0 {
		st.LastBackup, st.LastBackupAt = all[0].Path, all[0].Taken
	}
}

func (e *Env) storeWait(ctx context.Context, wait storeWaiting) error {
	port := 0
	if cfg, err := e.readConfig(); err == nil {
		port = cfg.HTTP.Port
	}
	return daemon.StoreWait{
		Version:  Version,
		StateDir: e.StateDir,
		Socket:   e.Socket,
		LockPath: filepath.Join(e.StateDir, daemonLock),
		InfoPath: filepath.Join(e.StateDir, httpInfoFile),
		HTTPPort: port,
		Status:   func() api.StoreStatus { return wait.status },
		Ready:    wait.ready,
		Every:    storeWaitEvery,
	}.Run(ctx)
}

// storeInteractive says whether e reads from a terminal; tests replace it.
var storeInteractive = func(e *Env) bool { return uninstallInteractive(e) }

func runStore(ctx context.Context, e *Env, args []string) error {
	if len(args) == 0 {
		return usageError{usage: storeUsage}
	}
	switch args[0] {
	case "migrate":
		return e.storeMigrate(ctx, args[1:])
	case "restore":
		return e.storeRestore(ctx, args[1:])
	}
	return usageError{usage: storeUsage}
}

func (e *Env) storeMigrate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("store migrate", flag.ContinueOnError)
	fs.SetOutput(e.Err)
	dryRun := fs.Bool("dry-run", false, "print what it would do and change nothing")
	yes := fs.Bool("yes", false, "migrate without asking")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return usageError{usage: storeUsage}
	}
	path := e.storePath()
	p, err := sqlite.PlanMigration(path, e.backupsPath(), e.Now())
	if err != nil {
		return storeCommandError(path, err)
	}
	if !p.Pending() {
		fmt.Fprintf(e.Out, "store: at schema version %d; nothing to migrate\n", p.From)
		return nil
	}
	fmt.Fprintf(e.Out, "store: version %d → %d (%s)\n", p.From, p.To, plural(len(p.Steps), "migration", "migrations"))
	if !releaseBuild(Version) {
		fmt.Fprintf(e.Out, "this build (%s) is not a release: a release at version %d will not read the store afterwards; `%s` goes back\n", Version, p.From, restoreCommand)
	}
	fmt.Fprintf(e.Out, "backup: %s (%s)\n", p.Backup, sqlite.Megabytes(p.Size))
	for _, s := range p.Steps {
		fmt.Fprintf(e.Out, "  %s\n", s)
	}
	if p.Short() {
		return fmt.Errorf("the backup needs about %s free in %s, and %s is free; free some space and run `%s` again", sqlite.Megabytes(2*p.Size), e.backupsPath(), sqlite.Megabytes(p.Free), migrateCommand)
	}
	if *dryRun {
		fmt.Fprintln(e.Out, "dry run: nothing changed")
		return nil
	}
	if err := e.confirmStore("migrate", *yes); err != nil {
		return err
	}
	return e.withStoreLock(ctx, func() error { return e.migrateStore(ctx, path, p.Backup) })
}

func (e *Env) migrateStore(ctx context.Context, path, backup string) error {
	p, err := sqlite.Migrate(ctx, path, sqlite.MigrateOptions{Backups: e.backupsPath(), Now: e.Now, Backup: backup,
		Step: func(label string, took time.Duration) {
			fmt.Fprintf(e.Out, "  %s ... ok (%s)\n", label, took.Round(time.Millisecond))
		},
		Warn: func(err error) { fmt.Fprintf(e.Err, "warning: %v\n", err) },
	})
	if err != nil {
		return err
	}
	if p.Pending() {
		fmt.Fprintln(e.Out, "check: integrity ok")
		fmt.Fprintf(e.Out, "store: version %d\n", p.To)
	}
	if err := e.renameTaskDossiers(p.From, p.To); err != nil {
		fmt.Fprintf(e.Err, "warning: %v; %s\n", err, dossierMoveFix(e.StateDir))
	}
	if p.To >= taskRenamedAt {
		db, err := sqlite.Open(path, e.changes)
		if err != nil {
			return err
		}
		defer db.Close()
		if err := e.applyDossierMoves(ctx, db); err != nil {
			fmt.Fprintf(e.Err, "warning: %v; %s\n", err, dossierMovesFix)
		}
	}
	return nil
}

func (e *Env) confirmStore(verb string, yes bool) error {
	if yes {
		return nil
	}
	if !storeInteractive(e) {
		return fmt.Errorf("store %s asks first; pass --yes when standard input is not a terminal", verb)
	}
	if !e.confirmed(verb + "?") {
		fmt.Fprintln(e.Out, "Nothing changed.")
		return exitCode(1)
	}
	return nil
}

func (e *Env) storeRestore(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("store restore", flag.ContinueOnError)
	fs.SetOutput(e.Err)
	list := fs.Bool("list", false, "list the backups")
	yes := fs.Bool("yes", false, "restore without asking")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 1 || (*list && len(pos) > 0) {
		return usageError{usage: storeUsage}
	}
	all, err := sqlite.Backups(e.backupsPath())
	if err != nil {
		return err
	}
	if *list {
		e.printBackups(all)
		return nil
	}
	var b sqlite.Backup
	if len(pos) == 1 {
		if b, err = e.namedBackup(all, pos[0]); err != nil {
			return err
		}
	} else {
		var ok bool
		if b, ok = sqlite.Restorable(all); !ok {
			return fmt.Errorf("no backup in %s that this release reads (schema version %d or older); `%s --list` lists them", e.backupsPath(), sqlite.SchemaVersion, restoreCommand)
		}
	}
	fmt.Fprintf(e.Out, "restore %s (version %d, taken %s)\n", b.Path, b.Version, b.Taken.Local().Format("2006-01-02 15:04"))
	fmt.Fprintf(e.Out, "what changed in %s since then is lost: sessions, notes, ledger entries, drafts; pull requests come back from GitHub on the next sync\n", brand.Name)
	fmt.Fprintf(e.Out, "the current store is kept in %s as a pre-restore copy\n", e.backupsPath())
	if err := e.confirmStore("restore", *yes); err != nil {
		return err
	}
	path := e.storePath()
	return e.withStoreLock(ctx, func() error {
		// The dossier directories follow the store they sit beside; one this release cannot read is taken
		// for one it wrote.
		was := uint32(sqlite.SchemaVersion)
		if s, err := sqlite.Inspect(path); err == nil && s.Version != 0 {
			was = s.Version
		}
		got, err := sqlite.Restore(ctx, path, b.Path, e.backupsPath(), e.Now())
		if err != nil {
			return err
		}
		if got.Kept != "" {
			fmt.Fprintf(e.Out, "kept the replaced store as %s\n", got.Kept)
		}
		fmt.Fprintf(e.Out, "store: restored at version %d\n", got.From.Version)
		if err := e.renameTaskDossiers(was, got.From.Version); err != nil {
			fmt.Fprintf(e.Err, "warning: %v; the restored store names its dossiers under %s\n", err, filepath.Join(dossier.Root(e.StateDir), "entry"))
		}
		if got.From.Version < sqlite.SchemaVersion {
			return e.migrateStore(ctx, path, "")
		}
		return nil
	})
}

func (e *Env) namedBackup(all []sqlite.Backup, name string) (sqlite.Backup, error) {
	abs := name
	if !filepath.IsAbs(abs) && !strings.ContainsRune(name, os.PathSeparator) {
		abs = filepath.Join(e.backupsPath(), name)
	}
	abs, _ = filepath.Abs(abs)
	for _, b := range all {
		if b.Path == abs {
			return b, nil
		}
	}
	s, err := sqlite.Inspect(abs)
	if err != nil {
		return sqlite.Backup{}, err
	}
	if s.Empty {
		return sqlite.Backup{}, fmt.Errorf("%s is not a store", abs)
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return sqlite.Backup{}, err
	}
	return sqlite.Backup{Path: abs, Version: s.Version, Taken: fi.ModTime(), Size: fi.Size()}, nil
}

func (e *Env) printBackups(all []sqlite.Backup) {
	if len(all) == 0 {
		fmt.Fprintf(e.Out, "no backups in %s\n", e.backupsPath())
		return
	}
	for _, b := range all {
		var notes []string
		if b.PreRestore {
			notes = append(notes, "before a restore")
		}
		if b.Version > sqlite.SchemaVersion {
			notes = append(notes, "newer than this release")
		}
		note := ""
		if len(notes) > 0 {
			note = "  (" + strings.Join(notes, ", ") + ")"
		}
		fmt.Fprintf(e.Out, "%s  version %d  %s  %s%s\n", filepath.Base(b.Path), b.Version, b.Taken.Local().Format("2006-01-02 15:04"), sqlite.Megabytes(b.Size), note)
	}
}

// withStoreLock runs fn holding the store's lock. A daemon that keeps the
// store open past storeLockWait is stopped through its LaunchAgent and
// started again after fn.
func (e *Env) withStoreLock(ctx context.Context, fn func() error) error {
	path := e.storePath()
	wait := storeLockWait
	if e.daemonServing(ctx) {
		wait = 0
	}
	unlock, err := e.waitStoreLockFor(ctx, path, wait)
	if err == nil {
		defer unlock()
		return fn()
	}
	if !errors.Is(err, sqlite.ErrStoreBusy) {
		return err
	}
	plist := e.agentPath(launchAgentLabel)
	if _, statErr := os.Stat(plist); goos != "darwin" || statErr != nil {
		return fmt.Errorf("%s's daemon has the store open; stop the `%s serve` you started and run this again", brand.Name, brand.Command)
	}
	fmt.Fprintln(e.Out, "stopping the daemon, which has the store open")
	e.bootOut(ctx, launchAgentLabel)
	unlock, err = e.waitStoreLockFor(ctx, path, storeLockWait)
	if err == nil {
		err = fn()
		unlock()
	}
	fmt.Fprintln(e.Out, "starting the daemon")
	return errors.Join(err, e.bootstrap(ctx, plist))
}

// daemonServing says a daemon answers with its store open, a store it
// keeps until it stops.
func (e *Env) daemonServing(ctx context.Context) bool {
	c, err := e.daemonClient(ctx)
	if err != nil {
		return false
	}
	h, err := c.Health(ctx)
	return err == nil && h.Store != nil && h.Store.State == api.StoreOK
}

func (e *Env) waitStoreLock(ctx context.Context, path string) (func(), error) {
	return e.waitStoreLockFor(ctx, path, storeLockWait)
}

func (e *Env) waitStoreLockFor(ctx context.Context, path string, wait time.Duration) (func(), error) {
	deadline := time.Now().Add(wait)
	for {
		unlock, err := sqlite.LockStore(path)
		if !errors.Is(err, sqlite.ErrStoreBusy) || !time.Now().Before(deadline) {
			return unlock, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// storeCommandError adds the way forward to a store this release cannot
// migrate.
func storeCommandError(path string, err error) error {
	var se sqlite.SchemaError
	if !errors.As(err, &se) {
		return err
	}
	return errors.New(tooNewStatus(path, se.Status).Message + "; `" + restoreCommand + "` restores it")
}

// reportStore prints the store's state once the daemon answers, which on a
// release follows any migration it ran at start.
func (e *Env) reportStore(ctx context.Context) {
	deadline := time.Now().Add(storeReportWait)
	for {
		if c, err := e.daemonClient(ctx); err == nil {
			if h, err := c.Health(ctx); err == nil && h.Store != nil && (h.Store.State != api.StoreBusy || !time.Now().Before(deadline)) {
				if h.Store.State == api.StoreOK {
					fmt.Fprintf(e.Out, "store: schema version %d.\n", h.Store.Version)
				} else {
					fmt.Fprintf(e.Out, "%s.\n", daemon.StoreWarning(*h.Store))
				}
				return
			}
		}
		if !time.Now().Before(deadline) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
}
