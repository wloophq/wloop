package round

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

type PR struct {
	// Fingerprint is what the last round covered: on a PR I review, my last
	// review and the head; on a PR I develop, the newest review triaged.
	Fingerprint string
	SentAt      time.Time
	// RoundAt is when the last round of any kind was sent.
	RoundAt time.Time
	// ChecksHead is the head whose failing checks a triage round covered.
	ChecksHead string
	// ConflictHead is the head whose conflict with the base a triage round
	// covered.
	ConflictHead string
	// AgentHome is the agent home of the session that asked for the review,
	// reused by the rounds the watch starts.
	AgentHome string
	// FirstRoundSkipped is set when the user skipped the automatic first
	// round; the sweep does not plan another one.
	FirstRoundSkipped bool
}

type Day struct {
	Total int
	PerPR map[string]int
	// CapLogged lists PRs whose cap hit was already printed today.
	CapLogged []string
	// Noted lists the notes already printed today.
	Noted []string
}

// Fields names the fields of a PR that a setter changed.
type Fields uint8

const (
	FieldFingerprint Fields = 1 << iota
	FieldSentAt
	FieldRoundAt
	FieldChecksHead
	FieldAgentHome
	FieldFirstRoundSkipped
	FieldConflictHead
)

// The kinds of a Note.
const (
	NoteCap  = "cap"
	NoteText = "note"
)

// Count is one round counted against the caps of Day.
type Count struct{ Day, Key string }

type Note struct{ Day, Kind, Text string }

// Changes are what the setters changed since the store was loaded or last
// saved: a repository writes only these, so two snapshots that change
// different fields of one PR both keep their change.
type Changes struct {
	PRs    map[string]Fields
	Counts []Count
	Notes  []Note
}

// Store remembers which reviews already got their follow-up and how many
// follow-ups each day sent.
type Store struct {
	PRs  map[string]PR
	Days map[string]Day

	changes Changes
}

func New() *Store {
	return &Store{PRs: map[string]PR{}, Days: map[string]Day{}, changes: Changes{PRs: map[string]Fields{}}}
}

func (s *Store) Changes() Changes { return s.changes }

// Saved forgets the changes once a repository wrote them.
func (s *Store) Saved() { s.changes = Changes{PRs: map[string]Fields{}} }

func (s *Store) set(key string, f Fields, edit func(*PR)) {
	p := s.PRs[key]
	edit(&p)
	s.PRs[key] = p
	if s.changes.PRs == nil {
		s.changes.PRs = map[string]Fields{}
	}
	s.changes.PRs[key] |= f
}

// DayKey is the local calendar date: a cap that resets at midnight UTC would
// reset mid-evening in America/Sao_Paulo.
func DayKey(t time.Time) string { return t.Local().Format("2006-01-02") }

func (s *Store) Fingerprint(key string) string { return s.PRs[key].Fingerprint }

// CapReason returns why an automated round for key may not run today, or "".
func (s *Store) CapReason(key string, now time.Time, perPR int) string {
	if s.Days[DayKey(now)].PerPR[key] >= perPR {
		return fmt.Sprintf("per-PR daily cap reached (%d)", perPR)
	}
	return ""
}

// FirstCapHit records that the cap hit for key was printed today and reports
// whether this is the first time.
func (s *Store) FirstCapHit(key string, now time.Time) bool {
	return s.firstNote(NoteCap, key, now)
}

// FirstNote records that note was printed today and reports whether this is
// the first time.
func (s *Store) FirstNote(note string, now time.Time) bool {
	return s.firstNote(NoteText, note, now)
}

func (s *Store) firstNote(kind, text string, now time.Time) bool {
	day := DayKey(now)
	d := s.Days[day]
	list := &d.Noted
	if kind == NoteCap {
		list = &d.CapLogged
	}
	if slices.Contains(*list, text) {
		return false
	}
	*list = append(*list, text)
	s.Days[day] = d
	s.changes.Notes = append(s.changes.Notes, Note{Day: day, Kind: kind, Text: text})
	return true
}

// HeldToday lists the PRs a cap held back today.
func (s *Store) HeldToday(now time.Time) []string { return s.Days[DayKey(now)].CapLogged }

func (s *Store) RoundAt(key string) time.Time { return s.PRs[key].RoundAt }

func (s *Store) AgentHome(key string) string { return s.PRs[key].AgentHome }

func (s *Store) SetAgentHome(key, dir string) {
	s.set(key, FieldAgentHome, func(p *PR) { p.AgentHome = dir })
}

// RecordRound notes a round the user started by hand: it does not count
// against the caps.
func (s *Store) RecordRound(key string, now time.Time) {
	s.set(key, FieldRoundAt, func(p *PR) { p.RoundAt = now.UTC() })
}

func (s *Store) SetFingerprint(key, fingerprint string) {
	s.set(key, FieldFingerprint, func(p *PR) { p.Fingerprint = fingerprint })
}

func (s *Store) ChecksHead(key string) string { return s.PRs[key].ChecksHead }

func (s *Store) SetChecksHead(key, head string) {
	s.set(key, FieldChecksHead, func(p *PR) { p.ChecksHead = head })
}

func (s *Store) ConflictHead(key string) string { return s.PRs[key].ConflictHead }

func (s *Store) SetConflictHead(key, head string) {
	s.set(key, FieldConflictHead, func(p *PR) { p.ConflictHead = head })
}

// SetHeads marks the failing checks and the conflict of the heads an
// address round covered; an empty head leaves its mark as it was.
func (s *Store) SetHeads(key, checksHead, conflictHead string) {
	if checksHead != "" {
		s.SetChecksHead(key, checksHead)
	}
	if conflictHead != "" {
		s.SetConflictHead(key, conflictHead)
	}
}

// RecordFollowup keeps the previous fingerprint when the round was not
// started by a review.
func (s *Store) RecordFollowup(key, fingerprint string, now time.Time) {
	f := FieldSentAt | FieldRoundAt
	if fingerprint != "" {
		f |= FieldFingerprint
	}
	s.set(key, f, func(p *PR) {
		if fingerprint != "" {
			p.Fingerprint = fingerprint
		}
		p.SentAt, p.RoundAt = now.UTC(), now.UTC()
	})
	day := DayKey(now)
	d := s.Days[day]
	if d.PerPR == nil {
		d.PerPR = map[string]int{}
	}
	d.PerPR[key]++
	d.Total++
	s.Days[day] = d
	s.changes.Counts = append(s.changes.Counts, Count{Day: day, Key: key})
}

func (s *Store) FirstRoundSkipped(key string) bool { return s.PRs[key].FirstRoundSkipped }

func (s *Store) SkipFirstRound(key string) {
	s.set(key, FieldFirstRoundSkipped, func(p *PR) { p.FirstRoundSkipped = true })
}

func (s *Store) ClearFirstRoundSkipped(key string) {
	if p, ok := s.PRs[key]; ok && p.FirstRoundSkipped {
		s.set(key, FieldFirstRoundSkipped, func(p *PR) { p.FirstRoundSkipped = false })
	}
}

// Pause holds every workflow in apply mode and board moves everywhere when
// On, else on the repositories in Repos. A pull request's own pause is its
// override's Hold.
type Pause struct {
	On    bool
	Repos []string // owner/repo
	At    time.Time
	Why   string
}

func (p Pause) Holds(owner, repo string) bool {
	return p.On || p.HoldsRepo(owner+"/"+repo)
}

// HoldsRepo says whether Repos names the owner/repo, under any case.
func (p Pause) HoldsRepo(name string) bool {
	return slices.ContainsFunc(p.Repos, func(r string) bool { return strings.EqualFold(r, name) })
}

// Any says whether the pause holds anything.
func (p Pause) Any() bool { return p.On || len(p.Repos) > 0 }
