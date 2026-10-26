package round

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// The marks the rounds of a pull request write, by what they covered.
const (
	// Reviewed is my last review and the head on a PR I review.
	Reviewed = "reviewed"
	// AddressedReviews is the newest review triaged on a PR I develop.
	AddressedReviews = "addressed.reviews"
	// AddressedChecks is the head whose failing checks a triage round covered.
	AddressedChecks = "addressed.checks"
	// AddressedConflict is the head whose conflict with the base a triage
	// round covered.
	AddressedConflict = "addressed.conflict"
)

type PR struct {
	// Marks are what the rounds covered, by mark name; see mark.Handled.
	Marks  map[string]string
	SentAt time.Time
	// RoundAt is when the last round of any kind was sent.
	RoundAt time.Time
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
	FieldSentAt Fields = 1 << iota
	FieldRoundAt
	FieldAgentHome
	FieldFirstRoundSkipped
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
	PRs map[string]Fields
	// Marks are the marks set or removed, by PR.
	Marks  map[string]map[string]bool
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
	return &Store{PRs: map[string]PR{}, Days: map[string]Day{}, changes: Changes{PRs: map[string]Fields{}, Marks: map[string]map[string]bool{}}}
}

func (s *Store) Changes() Changes { return s.changes }

// Saved forgets the changes once a repository wrote them.
func (s *Store) Saved() {
	s.changes = Changes{PRs: map[string]Fields{}, Marks: map[string]map[string]bool{}}
}

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

// Mark is key's mark name; empty when none.
func (s *Store) Mark(key, name string) string { return s.PRs[key].Marks[name] }

// SetMark sets key's mark name; an empty value removes it.
func (s *Store) SetMark(key, name, value string) {
	p := s.PRs[key]
	if value == "" {
		delete(p.Marks, name)
	} else {
		if p.Marks == nil {
			p.Marks = map[string]string{}
		}
		p.Marks[name] = value
	}
	s.PRs[key] = p
	if s.changes.Marks == nil {
		s.changes.Marks = map[string]map[string]bool{}
	}
	if s.changes.Marks[key] == nil {
		s.changes.Marks[key] = map[string]bool{}
	}
	s.changes.Marks[key][name] = true
}

// CapReached starts the refusal of a unit whose turns.per_unit_per_day is
// spent.
const CapReached = "turns.per_unit_per_day reached"

// CapReason returns why an automated round for key may not run today, or "".
func (s *Store) CapReason(key string, now time.Time, perPR int) string {
	if s.Days[DayKey(now)].PerPR[key] >= perPR {
		return fmt.Sprintf("%s (%d)", CapReached, perPR)
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

// RecordRound notes a round the user started by hand.
func (s *Store) RecordRound(key string, now time.Time) {
	s.set(key, FieldRoundAt, func(p *PR) { p.RoundAt = now.UTC() })
}

// SetHeads marks the failing checks and the conflict of the heads an
// address round covered; an empty head leaves its mark as it was.
func (s *Store) SetHeads(key, checksHead, conflictHead string) {
	if checksHead != "" {
		s.SetMark(key, AddressedChecks, checksHead)
	}
	if conflictHead != "" {
		s.SetMark(key, AddressedConflict, conflictHead)
	}
}

// RecordSent notes an automated round sent and sets its mark to
// fingerprint; an empty fingerprint keeps the mark as it was, for a round
// no review started. It counts nothing: Count does.
func (s *Store) RecordSent(key, mark, fingerprint string, now time.Time) {
	if fingerprint != "" {
		s.SetMark(key, mark, fingerprint)
	}
	s.set(key, FieldSentAt|FieldRoundAt, func(p *PR) { p.SentAt, p.RoundAt = now.UTC(), now.UTC() })
}

// Count spends one of key's turns of now's day, which CapReason reads.
func (s *Store) Count(key string, now time.Time) {
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

// Pause holds every workflow that does not observe and board moves everywhere when
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
