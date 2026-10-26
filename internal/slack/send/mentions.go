package send

import (
	"context"
	"fmt"
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
	// spans, Slack tokens and Markdown link targets.
	mentionSkipRE = regexp.MustCompile("`[^`]*`|<[^<>]*>|\\]\\([^()\\s]*\\)")
	broadcasts    = []string{"channel", "here", "everyone"}
)

// Mentions replaces each @handle, @<user id> and email of markdown that names
// one live user with mention(<user id>), outside code, Slack tokens and link
// targets. A name no user holds stays text; one several users hold fails
// with ErrAmbiguous.
func (s *Service) Mentions(ctx context.Context, markdown string, mention func(user string) string) (string, error) {
	if s.Client == nil {
		return "", ErrNotLoggedIn
	}
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
			out, err := s.mentionsIn(ctx, line[at:skip[0]], mention)
			if err != nil {
				return "", loggedOut(err)
			}
			b.WriteString(out + line[skip[0]:skip[1]])
			at = skip[1]
		}
		lines[i] = b.String()
	}
	return strings.Join(lines, "\n"), nil
}

func (s *Service) mentionsIn(ctx context.Context, text string, mention func(string) string) (string, error) {
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
			user, err = s.userByHandle(ctx, word[1:])
		} else if emailRE.MatchString(word) {
			user, err = s.userByEmail(ctx, word)
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

// userByHandle is the live user handle names, "" for none.
func (s *Service) userByHandle(ctx context.Context, handle string) (string, error) {
	switch {
	case handle == "" || slices.Contains(broadcasts, strings.ToLower(handle)):
		return "", nil
	case isUserID(handle):
		return handle, nil
	}
	holds := func(u slack.UserInfo) bool {
		return !u.Deleted && (strings.EqualFold(u.Name, handle) || strings.EqualFold(u.DisplayName, handle))
	}
	for refresh := false; ; refresh = true {
		users, fromCache, err := s.dir.users(ctx, s.Client, refresh)
		if err != nil {
			return "", err
		}
		found := matching(users, holds)
		switch {
		case len(found) == 1:
			return found[0].ID, nil
		case len(found) > 1:
			return "", candidates("@"+handle, "", found, nil, func(u slack.UserInfo) string { return "@" + u.Name + " (" + u.ID + ")" })
		case !fromCache:
			return "", nil
		}
	}
}

// userByEmail is the live user of email, "" for none.
func (s *Service) userByEmail(ctx context.Context, email string) (string, error) {
	u, err := s.Client.UserByEmail(ctx, email)
	switch {
	case code(err) == "users_not_found":
		return "", nil
	case err != nil:
		return "", fmt.Errorf("looking up %s: %w", email, err)
	case u.Deleted:
		return "", nil
	}
	return u.ID, nil
}
