package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	ghsource "github.com/wloophq/wloop/internal/adapter/source/github"
	"github.com/wloophq/wloop/internal/adapter/store/inventory"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite"
	"github.com/wloophq/wloop/internal/adapter/store/sqlite/sqlitetest"
	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/config"
	"github.com/wloophq/wloop/internal/core/change"
	"github.com/wloophq/wloop/internal/core/port"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/core/turns/turnstest"
	dintent "github.com/wloophq/wloop/internal/domain/intent"
	dinv "github.com/wloophq/wloop/internal/domain/inventory"
	"github.com/wloophq/wloop/internal/domain/turn"
	"github.com/wloophq/wloop/internal/domain/unit"
	"github.com/wloophq/wloop/internal/github"
	"github.com/wloophq/wloop/internal/readmodel"
)

// feedPR is the open pull request the feed server stores, agentRef's.
func feedPR() dinv.PR {
	return dinv.PR{Ref: github.Ref(agentRef), Title: "Batch export", Author: "ana", State: "OPEN", HeadOid: "h2", ChecksState: "SUCCESS",
		ReviewRequests: []string{"me"}, Reasons: []string{"review-requested"}, URL: agentRef.URL()}
}

// feedServer is a Server whose store notes a change log, with agentRef
// stored open and a turns service.
func feedServer(t *testing.T) (*Server, *change.Log, *turns.Service) {
	t.Helper()
	log := change.NewLog(change.DefaultWindow, nil)
	db, err := sqlitetest.Open(filepath.Join(t.TempDir(), "inv.db"), log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	st := inventory.New(db)
	st.UseClock(func() time.Time { return agentNow })
	if err := st.UpsertPR(context.Background(), feedPR()); err != nil {
		t.Fatal(err)
	}
	repo := sqlite.New(db)
	svc := &turns.Service{Intents: turnstest.Intents(), Store: repo, Registry: repo, Sessions: turnstest.NewSessions(), Clock: agentClock{}, Command: brand.Name, FastJob: 2 * time.Minute}
	s := New(withStore(Deps{Turns: svc, Activity: db, Changes: log, Snapshot: db, Cfg: config.Defaults(), Version: "test", Now: func() time.Time { return agentNow },
		Identity: viewerIs("me")}, st))
	s.token = ""
	stopOnCleanup(t, s)
	return s, log, svc
}

// fakeRows serves one row per listed unit whose title is the current n.
type fakeRows struct {
	mu    sync.Mutex
	units []unit.Unit
	n     int
	fail  error
}

func (p *fakeRows) Provider() unit.ProviderID { return "fake" }

func (p *fakeRows) Listed(context.Context) ([]unit.Unit, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.units), p.fail
}

func (p *fakeRows) Load(_ context.Context, units []unit.Unit) (map[unit.Unit]any, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	in := map[unit.Unit]any{}
	for _, u := range units {
		in[u] = p.n
	}
	return in, nil
}

func (p *fakeRows) Derive(_ readmodel.Base, in any, _ time.Time) (api.Row, error) {
	return api.Row{Title: fmt.Sprint(in)}, nil
}

func (p *fakeRows) set(units []unit.Unit, n int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.units, p.n = units, n
}

// fakeServer is a Server whose read model serves p's rows over a change
// log.
func fakeServer(t *testing.T, p *fakeRows) (*Server, *change.Log, *readmodel.Model) {
	t.Helper()
	s := newServer(t)
	log := withLog(t, s)
	m := readmodel.New(log, nil, nil, nil, nil, p)
	m.UseClock(s.deps.Now)
	s.modelOnce.Do(func() { s.model = m })
	return s, log, m
}

// runFeeds runs both feeds until the test ends and returns once they wait
// for changes. It runs in a synctest bubble.
func runFeeds(t *testing.T, s *Server) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	t.Cleanup(func() {
		cancel()
		wg.Wait()
	})
	wg.Go(func() { s.feedRows(ctx) })
	wg.Go(func() { s.feedHealth(ctx) })
	synctest.Wait()
	if s.hub.position() == (api.Since{}) {
		t.Fatal("the rows feed never started")
	}
}

type message struct {
	id string
	c  api.Change
}

// pipeWriter is a streaming ResponseWriter whose body a reader takes from a
// pipe, so a stream runs inside a synctest bubble.
type pipeWriter struct {
	header http.Header
	body   *io.PipeWriter
}

func (w *pipeWriter) Header() http.Header         { return w.header }
func (w *pipeWriter) Write(b []byte) (int, error) { return w.body.Write(b) }
func (w *pipeWriter) WriteHeader(int)             {}
func (w *pipeWriter) Flush()                      {}

// listen connects to the stream with the request changed by prepare and
// delivers each data message, once the stream said it is connected, until
// the test ends.
func listen(t *testing.T, s *Server, prepare func(*http.Request)) <-chan message {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/events?api="+api.Version, nil)
	if prepare != nil {
		prepare(req)
	}
	body, pw := io.Pipe()
	served := make(chan struct{})
	go func() {
		defer close(served)
		s.Handler().ServeHTTP(&pipeWriter{header: http.Header{}, body: pw}, req)
		pw.Close()
	}()
	out, stop, exited := make(chan message, 64), make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		cancel()
		close(stop)
		body.Close()
		<-served
		<-exited
	})
	r := bufio.NewReader(body)
	if line, _ := r.ReadString('\n'); !strings.HasPrefix(line, ": connected") {
		close(exited)
		t.Fatalf("first line %q", line)
	}
	go func() {
		defer close(exited)
		defer close(out)
		var id string
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimSuffix(line, "\n")
			switch {
			case strings.HasPrefix(line, "id: "):
				id = strings.TrimPrefix(line, "id: ")
			case strings.HasPrefix(line, "data: "):
				var c api.Change
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &c); err != nil {
					t.Errorf("data %q: %v", line, err)
					return
				}
				select {
				case out <- message{id: id, c: c}:
				case <-stop:
					return
				}
				id = ""
			}
		}
	}()
	return out
}

// collectFor gathers the messages that arrive within d.
func collectFor(msgs <-chan message, d time.Duration) []message {
	var got []message
	stop := time.After(d)
	for {
		select {
		case m, ok := <-msgs:
			if !ok {
				return got
			}
			got = append(got, m)
		case <-stop:
			return got
		}
	}
}

func TestIdleCyclePublishesNoRowChange(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		s, _, _ := feedServer(t)
		runFeeds(t, s)
		msgs := listen(t, s, nil)
		fast := newJob("fast", time.Hour, time.Minute, func(ctx context.Context) (string, string, error) {
			return ghsource.Outcome(ghsource.Report{Queries: 1}, s.store().UpsertPR(ctx, feedPR()))
		})
		fast.onEnd = s.jobEnded
		fast.once(context.Background(), func(string, ...any) {}, s.jobDone)
		got := collectFor(msgs, healthQuiet+time.Second)
		health := 0
		for _, m := range got {
			if len(m.c.Units) > 0 || m.c.All {
				t.Fatalf("an idle cycle published a row change: %+v", m.c)
			}
			if m.c.Health {
				health++
			}
		}
		if health != 1 {
			t.Fatalf("%d health messages: %+v", health, got)
		}
	})
}

func TestTurnEndPublishesItsUnit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, _, svc := feedServer(t)
		sendTurn(t, s, svc, agentRef, turn.Intent(dintent.Review), "abc123abc123")
		h := s.Handler()
		if rec := agentCall(t, h, "POST", api.AgentTurnsStart, "term_1", `{"turn":"abc123abc123","intent":"review"}`); rec.Code != 200 {
			t.Fatalf("start: %d %s", rec.Code, rec.Body)
		}
		runFeeds(t, s)
		msgs := listen(t, s, nil)
		ended := time.Now()
		if rec := agentCall(t, h, "POST", api.AgentTurnsEnd, "term_1", `{"turn":"abc123abc123","pr":"`+agentRef.URL()+`","record":`+doneReview+`}`); rec.Code != 200 {
			t.Fatalf("end: %d %s", rec.Code, rec.Body)
		}
		for m := range msgs {
			if slices.Contains(m.c.Units, agentRef.Unit()) {
				if took := time.Since(ended); took != readmodel.PokeQuiet {
					t.Fatalf("the turn end reached the stream after %v, want the read model's quiet period, %v", took, readmodel.PokeQuiet)
				}
				return
			}
		}
		t.Fatal("the stream ended")
	})
}

func TestBurstCoalesces(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		u := agentRef.Unit()
		p := &fakeRows{units: []unit.Unit{u}}
		s, log, _ := fakeServer(t, p)
		runFeeds(t, s)
		msgs := listen(t, s, nil)
		for i := 1; i <= 100; i++ {
			p.set([]unit.Unit{u}, i)
			log.Note(port.TopicPRs, u)
		}
		got := collectFor(msgs, 1500*time.Millisecond)
		if len(got) != 1 || !slices.Equal(got[0].c.Units, []unit.Unit{u}) || got[0].c.All {
			t.Fatalf("%d messages: %+v", len(got), got)
		}
	})
}

func TestResumeFromLastEventID(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		u := agentRef.Unit()
		p := &fakeRows{units: []unit.Unit{u}}
		s, log, m := fakeServer(t, p)
		ctx := context.Background()
		before, err := m.Since(ctx, api.Since{})
		if err != nil {
			t.Fatal(err)
		}
		p.set([]unit.Unit{u}, 1)
		log.Note(port.TopicPRs, u)
		id := func(c api.Change) string { return c.Epoch + ":" + strconv.FormatUint(c.Rev, 10) }
		msgs := listen(t, s, func(r *http.Request) { r.Header.Set("Last-Event-ID", id(before)) })
		got := collectFor(msgs, 300*time.Millisecond)
		want := before.Epoch + ":" + strconv.FormatUint(before.Rev+1, 10)
		if len(got) != 1 || got[0].id != want || !slices.Equal(got[0].c.Units, []unit.Unit{u}) || got[0].c.All {
			t.Fatalf("resume from %s: %+v, want one message with id %s naming %v", id(before), got, want, u)
		}
		current := got[0].id
		if again := collectFor(listen(t, s, func(r *http.Request) { r.Header.Set("Last-Event-ID", current) }), 300*time.Millisecond); len(again) != 0 {
			t.Fatalf("a client at the current revision got %+v", again)
		}
	})
}

func TestResumePastWindowIsAll(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		units := make([]unit.Unit, 1100)
		for i := range units {
			units[i] = github.Ref{Owner: "acme", Repo: "api", Number: i + 1}.Unit()
		}
		p := &fakeRows{units: units}
		s, log, m := fakeServer(t, p)
		before, err := m.Since(context.Background(), api.Since{})
		if err != nil {
			t.Fatal(err)
		}
		p.set(nil, 0)
		log.Note(port.TopicPRs)
		for _, c := range []struct {
			name    string
			prepare func(*http.Request)
		}{
			{"past the tombstones", func(r *http.Request) {
				r.URL.RawQuery += "&since=" + before.Epoch + ":" + strconv.FormatUint(before.Rev, 10)
			}},
			{"ahead of Rev", func(r *http.Request) {
				r.Header.Set("Last-Event-ID", before.Epoch+":"+strconv.FormatUint(before.Rev+1000, 10))
			}},
			{"another epoch", func(r *http.Request) { r.Header.Set("Last-Event-ID", "gone:1") }},
			{"an unreadable id", func(r *http.Request) { r.Header.Set("Last-Event-ID", "garbage") }},
		} {
			got := collectFor(listen(t, s, c.prepare), 300*time.Millisecond)
			if len(got) != 1 || !got[0].c.All || len(got[0].c.Units) != 0 {
				t.Fatalf("%s: %+v", c.name, got)
			}
		}
	})
}

func TestFailedResumeReadIsAll(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		u := agentRef.Unit()
		p := &fakeRows{units: []unit.Unit{u}}
		s, log, m := fakeServer(t, p)
		before, err := m.Since(context.Background(), api.Since{})
		if err != nil {
			t.Fatal(err)
		}
		p.mu.Lock()
		p.fail = errors.New("store gone")
		p.mu.Unlock()
		log.Note(port.TopicPRs, u)
		got := collectFor(listen(t, s, func(r *http.Request) {
			r.Header.Set("Last-Event-ID", before.Epoch+":"+strconv.FormatUint(before.Rev, 10))
		}), 300*time.Millisecond)
		if len(got) != 1 || !got[0].c.All {
			t.Fatalf("%+v", got)
		}
	})
}

func TestNoChangedCallSitesLeft(t *testing.T) {
	t.Parallel()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(src), ".changed()") {
			t.Errorf("%s still publishes with changed(); a write notes the change log instead", f)
		}
	}
}

func TestMergedChangesSayWhatBothSaid(t *testing.T) {
	t.Parallel()
	a, b := github.Ref{Owner: "o", Repo: "r", Number: 1}.Unit(), github.Ref{Owner: "o", Repo: "r", Number: 2}.Unit()
	c := api.Change{Type: "changed", Epoch: "e", Rev: 2, Units: []unit.Unit{b}}
	c.Merge(api.Change{Type: "changed", Health: true})
	c.Merge(api.Change{Type: "changed", Epoch: "e", Rev: 3, Units: []unit.Unit{a, b}})
	if c.Rev != 3 || !c.Health || c.All || !slices.Equal(c.Units, []unit.Unit{a, b}) {
		t.Fatalf("%+v", c)
	}
	c.Merge(api.Change{Type: "changed", Epoch: "f", Rev: 1})
	if !c.All || c.Units != nil || c.Epoch != "f" || c.Rev != 1 {
		t.Fatalf("another epoch: %+v", c)
	}
}
