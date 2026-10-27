package send

import (
	"context"
	"regexp"
	"slices"
	"strings"

	"github.com/wloophq/wloop/internal/slack"
)

var (
	// mentionCandidateRE is an email or an @handle; the class before the @
	// takes in what precedes it, so a handle's match starts at its @.
	mentionCandidateRE = regexp.MustCompile(`[A-Za-z0-9._%+-]*@[A-Za-z0-9._-]+`)
	emailRE            = regexp.MustCompile(`^[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)*\.[A-Za-z]{2,}$`)
	// mentionSkipRE are the parts of a line no mention is read in: code
	// spans, Slack tokens, Markdown links and URLs.
	mentionSkipRE = regexp.MustCompile("`[^`]*`|<[^<>]*>|\\[[^\\]\\n]*\\]\\([^()\\s]*\\)|(?i:https?://|mailto:)\\S+")
	broadcasts    = []string{"channel", "here", "everyone"}
)

// Mentions replaces each @handle, @<user id> and email of markdown that names
// one live user with mention(<user id>), outside code, Slack tokens, URLs
// and Markdown links. A name no user holds, or one a lookup fails on, stays
// text; one several users hold fails with ErrAmbiguous.
func (s *Service) Mentions(ctx context.Context, markdown string, mention func(user string) string) (string, error) {
	if s.Client == nil {
		return "", ErrNotLoggedIn
	}
	r := &mentionLookup{s: s}
	lines := strings.Split(markdown, "\n")
	inCode := false
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inCode = !inCode
			continue
		}
		if inCode {
			continue
		}
		var b strings.Builder
		at := 0
		for _, skip := range append(mentionSkipRE.FindAllStringIndex(line, -1), []int{len(line), len(line)}) {
			out, err := r.in(ctx, line[at:skip[0]], mention)
			if err != nil {
				return "", err
			}
			b.WriteString(out + line[skip[0]:skip[1]])
			at = skip[1]
		}
		lines[i] = b.String()
	}
	return strings.Join(lines, "\n"), nil
}

// mentionLookup resolves the names of one text: it reads the user listing
// fresh at most once, and a lookup that fails leaves its name as text.
type mentionLookup struct {
	s       *Service
	users   []slack.UserInfo
	read    bool // users holds a listing
	fresh   bool // users holds a fresh listing
	listErr bool
}

func (r *mentionLookup) in(ctx context.Context, text string, mention func(string) string) (string, error) {
	var b strings.Builder
	at := 0
	for _, m := range mentionCandidateRE.FindAllStringIndex(text, -1) {
		word := strings.TrimRight(text[m[0]:m[1]], ".-_")
		end := m[0] + len(word)
		if end < len(text) && text[end] == '/' {
			continue
		}
		var user string
		var err error
		if strings.HasPrefix(word, "@") {
			user, err = r.handle(ctx, word[1:])
		} else if emailRE.MatchString(word) {
			user = r.email(ctx, word)
		}
		if err != nil {
			return "", err
		}
		if user == "" {
			continue
		}
		b.WriteString(text[at:m[0]] + mention(user))
		at = end
	}
	b.WriteString(text[at:])
	return b.String(), nil
}

// handle is the live user handle names, "" for none: a user id only when a
// listed user holds it.
func (r *mentionLookup) handle(ctx context.Context, handle string) (string, error) {
	if handle == "" || slices.Contains(broadcasts, strings.ToLower(handle)) {
		return "", nil
	}
	holds := func(u slack.UserInfo) bool {
		return !u.Deleted && (u.ID == handle || strings.EqualFold(u.Name, handle) || strings.EqualFold(u.DisplayName, handle))
	}
	for {
		found := matching(r.users, holds)
		switch {
		case len(found) == 1:
			return found[0].ID, nil
		case len(found) > 1:
			return "", candidates("@"+handle, "", found, nil, func(u slack.UserInfo) string { return "@" + u.Name + " (" + u.ID + ")" })
		case r.fresh || r.listErr:
			return "", nil
		}
		users, fromCache, err := r.s.dir.users(ctx, r.s.Client, r.read)
		if err != nil {
			r.listErr = true
			r.s.logf("slack_mentions_unresolved error=%v", err)
			continue
		}
		r.users, r.read, r.fresh = users, true, !fromCache
	}
}

// email is the live user of email, "" for none or when the lookup fails.
func (r *mentionLookup) email(ctx context.Context, email string) string {
	u, err := r.s.Client.UserByEmail(ctx, email)
	switch {
	case code(err) == "users_not_found":
		return ""
	case err != nil:
		r.s.logf("slack_mentions_unresolved error=%v", err)
		return ""
	case u.Deleted:
		return ""
	}
	return u.ID
}

func (s *Service) logf(format string, args ...any) {
	if s.Log != nil {
		s.Log(format, args...)
	}
}
