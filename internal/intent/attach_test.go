package intent

import (
	"strings"
	"testing"
)

func TestManifestAttachKeys(t *testing.T) {
	l, problems := loadIntentWith(t, "outcome = [\"url\"]\nattach_url = true\n\n[drafts]\nattach_posts = true\n")
	if len(problems) > 0 {
		t.Fatal(problems.Error())
	}
	d, _ := l.Registry.Get("ask-author")
	if !d.Record.AttachURL || !d.AttachPosts {
		t.Fatalf("attach_url = %t, attach_posts = %t; want both set", d.Record.AttachURL, d.AttachPosts)
	}
	l, _ = loadIntentWith(t, "")
	if d, _ := l.Registry.Get("ask-author"); d.Record.AttachURL || d.AttachPosts {
		t.Fatalf("a manifest without them attaches: %+v", d)
	}
}

func TestAttachURLNeedsTheURLOutcome(t *testing.T) {
	_, problems := loadIntentWith(t, "attach_url = true\n")
	if !strings.Contains(problems.Error(), "record.attach_url") {
		t.Fatalf("problems = %v, want one on record.attach_url", problems)
	}
}
