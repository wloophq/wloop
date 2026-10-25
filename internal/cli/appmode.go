package cli

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/wloophq/wloop/internal/api/client"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/client/menubar"
	"github.com/wloophq/wloop/internal/client/menubar/macos"
)

var errNotMacOS = errors.New("the menu bar needs macOS")

// runAppClient runs the menu bar as a client of the daemon, on the calling
// goroutine, which must be main. Quit ends the menu bar and leaves the daemon
// serving.
func runAppClient(ctx context.Context, e *Env) error {
	if !e.NativeAvailable() {
		return errNotMacOS
	}
	if !macos.InBundle(e.Executable) {
		return errors.New(bundleHint(e.Executable))
	}
	logf := func(format string, args ...any) {
		fmt.Fprintf(e.Err, "%s %s\n", e.stamp(), fmt.Sprintf(format, args...))
	}
	if rebuilt, err := e.syncBundle(ctx); err != nil {
		logf("checking "+brand.App+".app against its source: %v", err)
	} else if rebuilt {
		// In place: an exit would wait out the LaunchAgent's ThrottleInterval.
		logf("%s.app rebuilt from a newer binary; restarting in place", brand.App)
		if err := e.Reexec(); err != nil {
			logf("restarting in place: %v", err)
		}
		return errRebuilt
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	upgraded := watchBinary(ctx, e.bundleSource())
	go func() {
		select {
		case <-upgraded:
			// Exits for the LaunchAgent to start a fresh process: AppKit's
			// terminate exits 0, which it does not restart, and an exec under
			// a running AppKit leaves the status item blank.
			logf("%s was replaced; exiting for the LaunchAgent to rebuild "+brand.App+".app", e.bundleSource())
			e.Exit(exitRebuilt)
		case <-ctx.Done():
		}
	}()
	digestAt := ""
	if cfg, err := e.loadConfig(); err != nil {
		logf("reading the config: %v; the daily digest is off", err)
	} else {
		digestAt = cfg.Notifications.DigestAt
	}
	e.RunNative(func(tray menubar.Tray, notes menubar.Notifier) {
		dc := client.New(e.Socket)
		c := menubar.New(menubar.Config{
			Daemon:     dc,
			Tray:       tray,
			Notifier:   notes,
			OpenURL:    func(u string) error { return e.openInBrowser(context.Background(), u) },
			CockpitURL: func(route string) (string, error) { return cockpitURL(e.StateDir, route) },
			Quit:       cancel,
			Copy:       copyToClipboard,
			Now:        e.Now,
			Log:        logf,
			SentPath:   filepath.Join(e.StateDir, "notified.json"),
			DigestAt:   digestAt,
		})
		go func() {
			<-ctx.Done()
			e.QuitNative()
		}()
		go func() {
			_ = bootMenubar(ctx, dc.Ping, func(ctx context.Context) error { return waitForDaemon(ctx, dc) }, c)
		}()
	})
	return nil
}

type menubarApp interface {
	Refresh(ctx context.Context)
	Run(ctx context.Context) error
}

// bootMenubar paints the daemon-down menu first only when the daemon does
// not answer, then runs the app, whose first refresh is the boot load.
func bootMenubar(ctx context.Context, ping, wait func(context.Context) error, app menubarApp) error {
	if ping(ctx) != nil {
		app.Refresh(ctx)
		if err := wait(ctx); err != nil {
			return err
		}
	}
	return app.Run(ctx)
}

func waitForDaemon(ctx context.Context, c *client.Client) error {
	for {
		if err := c.Ping(ctx); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func bundleHint(exe string) string {
	return fmt.Sprintf("not running from "+brand.App+".app (%s); run '"+brand.Command+" app install'", filepath.Base(exe))
}

func copyToClipboard(text string) error {
	cmd := exec.Command("pbcopy")
	cmd.Stdin = strings.NewReader(text)
	return cmd.Run()
}
