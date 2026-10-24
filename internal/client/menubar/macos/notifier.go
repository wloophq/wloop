//go:build darwin

package macos

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/ebitengine/purego/objc"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/client/menubar"
)

const (
	defaultActionIdentifier = "com.apple.UNNotificationDefaultActionIdentifier"

	authBadge       = 1 << 0
	authSound       = 1 << 1
	authAlert       = 1 << 2
	authProvisional = 1 << 6

	presentSound  = 1 << 1
	presentList   = 1 << 3
	presentBanner = 1 << 4
)

type notifier struct {
	center   objc.ID
	imageDir string
	mu       sync.Mutex
	fn       func(menubar.Response)
	// catsMu keeps the categories registered in the order they changed.
	catsMu sync.Mutex
	cats   *menubar.Categories
}

var current *notifier

func newNotifier(imageDir string) *notifier {
	n := &notifier{center: class("UNUserNotificationCenter").Send(sel("currentNotificationCenter")), imageDir: imageDir}
	current = n
	n.center.Send(sel("setDelegate:"), delegateOnce())
	n.cats = menubar.NewCategories()
	n.register(n.cats.List())
	return n
}

// register replaces the categories the notification center knows with specs.
func (n *notifier) register(specs []menubar.CategorySpec) {
	categories := make([]objc.ID, len(specs))
	for i, spec := range specs {
		actions := make([]objc.ID, len(spec.Actions))
		for j, a := range spec.Actions {
			if a.TextInput {
				actions[j] = class("UNTextInputNotificationAction").Send(sel("actionWithIdentifier:title:options:textInputButtonTitle:textInputPlaceholder:"),
					nsString(a.ID), nsString(a.Title), uint(0), nsString(inputButton(a.ID)), nsString(placeholder(a.ID)))
				continue
			}
			actions[j] = class("UNNotificationAction").Send(sel("actionWithIdentifier:title:options:"), nsString(a.ID), nsString(a.Title), uint(0))
		}
		categories[i] = class("UNNotificationCategory").Send(sel("categoryWithIdentifier:actions:intentIdentifiers:options:"),
			nsString(string(spec.ID)), nsArray(actions...), nsArray(), uint(0))
	}
	n.center.Send(sel("setNotificationCategories:"), class("NSSet").Send(sel("setWithArray:"), nsArray(categories...)))
}

// awaitCategories waits until the notification center reads back the
// categories just set, which it applies asynchronously, so a question posted
// next shows its buttons.
func (n *notifier) awaitCategories() {
	got := make(chan struct{}, 1)
	b := objc.NewBlock(func(_ objc.Block, _ objc.ID) { got <- struct{}{} })
	withPool(func() { n.center.Send(sel("getNotificationCategoriesWithCompletionHandler:"), b) })
	select {
	case <-got:
		b.Release()
	case <-time.After(postTimeout):
		// the center may still call b, so it is never released
	}
}

func placeholder(action string) string {
	switch action {
	case "guide":
		return "Guidance for this round"
	case api.AttentionReply:
		return "Your reply, saved to the Outbox"
	case api.AttentionSendReply:
		return "Your reply, sent now"
	}
	return "Your answer to the agent"
}

// inputButton is the button that sends a text input's text.
func inputButton(action string) string {
	if action == api.AttentionReply {
		return "Save"
	}
	return "Send"
}

var delegateOnce = sync.OnceValue(newDelegate)

func newDelegate() objc.ID {
	c, err := objc.RegisterClass("LoopNotificationDelegate", objc.GetClass("NSObject"),
		[]*objc.Protocol{objc.GetProtocol("UNUserNotificationCenterDelegate")}, nil,
		[]objc.MethodDef{
			{Cmd: sel("userNotificationCenter:didReceiveNotificationResponse:withCompletionHandler:"),
				Fn: func(_ objc.ID, _ objc.SEL, _ objc.ID, r objc.ID, done objc.Block) {
					defer callBlock(done)
					current.respond(r)
				}},
			{Cmd: sel("userNotificationCenter:willPresentNotification:withCompletionHandler:"),
				Fn: func(_ objc.ID, _ objc.SEL, _ objc.ID, _ objc.ID, done objc.Block) {
					callBlock(done, presentBanner|presentList|presentSound)
				}},
		})
	if err != nil {
		panic(fmt.Sprintf("registering the notification delegate: %v", err))
	}
	return objc.ID(c).Send(sel("new"))
}

// response is what the user's pick of action on the notification id asks.
func response(id, action string) (menubar.Response, bool) {
	if action == defaultActionIdentifier {
		return menubar.Response{ID: id, Identifier: action, Action: menubar.ActionOpen}, true
	}
	return menubar.ResponseFor(id, action)
}

func (n *notifier) respond(r objc.ID) {
	id := goString(r.Send(sel("notification")).Send(sel("request")).Send(sel("identifier")))
	out, ok := response(id, goString(r.Send(sel("actionIdentifier"))))
	if !ok {
		return
	}
	if objc.Send[bool](r, sel("respondsToSelector:"), sel("userText")) {
		out.Text = goString(r.Send(sel("userText")))
	}
	n.mu.Lock()
	fn := n.fn
	n.mu.Unlock()
	if fn != nil {
		go fn(out)
	}
}

func (n *notifier) OnResponse(fn func(menubar.Response)) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.fn = fn
}

func permission(status int) menubar.Permission {
	switch status {
	case 1:
		return menubar.PermissionDenied
	case 2, 4: // authorized, ephemeral
		return menubar.PermissionAuthorized
	case 3:
		return menubar.PermissionProvisional
	}
	return menubar.PermissionNotDetermined
}

func (n *notifier) Permission(ctx context.Context) (menubar.Permission, error) {
	got := make(chan int, 1)
	b := objc.NewBlock(func(_ objc.Block, settings objc.ID) {
		got <- objc.Send[int](settings, sel("authorizationStatus"))
	})
	defer b.Release()
	withPool(func() { n.center.Send(sel("getNotificationSettingsWithCompletionHandler:"), b) })
	select {
	case s := <-got:
		return permission(s), nil
	case <-ctx.Done():
		return menubar.PermissionNotDetermined, ctx.Err()
	}
}

// request asks for options and waits for the user's answer. The error is
// ctx's, or the system's reason when it refused to ask.
func (n *notifier) request(ctx context.Context, options uint) (bool, error) {
	type answer struct {
		granted bool
		err     error
	}
	got := make(chan answer, 1)
	b := objc.NewBlock(func(_ objc.Block, granted bool, err objc.ID) { got <- answer{granted, nsError(err)} })
	defer b.Release()
	withPool(func() { n.center.Send(sel("requestAuthorizationWithOptions:completionHandler:"), options, b) })
	select {
	case a := <-got:
		return a.granted, a.err
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

// Authorize asks once; a dialog dismissed without an answer falls back to
// provisional authorization, which delivers quietly instead of never. When
// the system refuses both requests, its reason is the error.
func (n *notifier) Authorize(ctx context.Context) (menubar.Permission, error) {
	if p, err := n.Permission(ctx); err != nil || p != menubar.PermissionNotDetermined {
		return p, err
	}
	ok, refused := n.request(ctx, authAlert|authSound|authBadge)
	if ctx.Err() != nil {
		return menubar.PermissionNotDetermined, ctx.Err()
	}
	if ok {
		return n.Permission(ctx)
	}
	if p, err := n.Permission(ctx); err != nil || p != menubar.PermissionNotDetermined {
		return p, err
	}
	ok, provisionalRefused := n.request(ctx, authProvisional)
	if ctx.Err() != nil {
		return menubar.PermissionNotDetermined, ctx.Err()
	}
	p, err := n.Permission(ctx)
	if err == nil && !ok && refused != nil && provisionalRefused != nil {
		err = refused
	}
	return p, err
}

// nsError is err's localized description as a Go error; nil for no error.
func nsError(err objc.ID) error {
	if err == 0 {
		return nil
	}
	if desc := goString(err.Send(sel("localizedDescription"))); desc != "" {
		return errors.New(desc)
	}
	return errors.New("unknown error")
}

var errPostTimeout = errors.New("the notification center did not answer")

const postTimeout = 5 * time.Second

func (n *notifier) Post(note menubar.Notification) error {
	got := make(chan error, 1)
	b := objc.NewBlock(func(_ objc.Block, err objc.ID) { got <- nsError(err) })
	defer b.Release()
	n.catsMu.Lock()
	if n.cats.Post(note) {
		withPool(func() { n.register(n.cats.List()) })
		n.awaitCategories()
	}
	n.catsMu.Unlock()
	withPool(func() {
		c := class("UNMutableNotificationContent").Send(sel("new"))
		defer c.Send(sel("release"))
		c.Send(sel("setTitle:"), nsString(note.Title))
		if note.Subtitle != "" {
			c.Send(sel("setSubtitle:"), nsString(note.Subtitle))
		}
		c.Send(sel("setBody:"), nsString(note.Body))
		c.Send(sel("setCategoryIdentifier:"), nsString(string(note.Category)))
		if a := n.attachment(note.Image); a != 0 {
			c.Send(sel("setAttachments:"), nsArray(a))
		}
		req := class("UNNotificationRequest").Send(sel("requestWithIdentifier:content:trigger:"), nsString(note.ID), c, objc.ID(0))
		n.center.Send(sel("addNotificationRequest:withCompletionHandler:"), req, b)
	})
	select {
	case err := <-got:
		if err != nil {
			return fmt.Errorf("the notification center refused the notification: %w", err)
		}
		return nil
	case <-time.After(postTimeout):
		return errPostTimeout
	}
}

var attachmentFailed sync.Once

// attachment is the image of a kind, or 0 when it has none or macOS refused
// it; the notification then goes without it.
func (n *notifier) attachment(img menubar.Image) objc.ID {
	path, err := imageFile(n.imageDir, img)
	if path == "" {
		if err != nil {
			attachmentFailed.Do(func() { fmt.Fprintf(os.Stderr, "notification image: %v; posting without images\n", err) })
		}
		return 0
	}
	url := class("NSURL").Send(sel("fileURLWithPath:"), nsString(path))
	a := class("UNNotificationAttachment").Send(sel("attachmentWithIdentifier:URL:options:error:"), nsString(string(img)), url, objc.ID(0), objc.ID(0))
	if a == 0 {
		os.Remove(path)
		attachmentFailed.Do(func() { fmt.Fprintf(os.Stderr, "notification image: macOS refused %s; posting without images\n", path) })
	}
	return a
}

func (n *notifier) Withdraw(ids []string) {
	n.catsMu.Lock()
	if n.cats.Withdraw(ids) {
		withPool(func() { n.register(n.cats.List()) })
	}
	n.catsMu.Unlock()
	withPool(func() {
		items := make([]objc.ID, len(ids))
		for i, id := range ids {
			items[i] = nsString(id)
		}
		n.center.Send(sel("removeDeliveredNotificationsWithIdentifiers:"), nsArray(items...))
	})
}

func (n *notifier) WithdrawAll() {
	withPool(func() { n.center.Send(sel("removeAllDeliveredNotifications")) })
}
