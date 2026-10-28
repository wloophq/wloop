package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/wloophq/wloop/internal/api"
	"github.com/wloophq/wloop/internal/brand"
	"github.com/wloophq/wloop/internal/core/turns"
	"github.com/wloophq/wloop/internal/domain/record"
	"github.com/wloophq/wloop/internal/domain/unit"
)

type fakeTurns struct {
	seen     string
	recorded int
	last     turns.RecordRequest
	answer   turns.Recorded
	err      error
}

func (f *fakeTurns) MCPSeen(_ context.Context, handle string) error {
	f.seen = handle
	return nil
}

// SessionOfHandle takes every handle as the id of its session.
func (f *fakeTurns) SessionOfHandle(_ context.Context, handle string) (string, bool, error) {
	return handle, true, nil
}

func (f *fakeTurns) RecordTurnEnd(_ context.Context, r turns.RecordRequest) (turns.Recorded, error) {
	f.recorded++
	f.last = r
	return f.answer, f.err
}

func call(t *testing.T, s *Server, sid, handle, body string, edit ...func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:47800/mcp", strings.NewReader(body))
	req.Host = "127.0.0.1:47800"
	if handle != "" {
		req.Header.Set(api.SessionHeader, handle)
	}
	if sid != "" {
		req.Header.Set("Mcp-Session-Id", sid)
	}
	for _, e := range edit {
		e(req)
	}
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	return rec
}

const initialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"claude-code","version":"2.1.302"}}}`

func TestSessionsOutsideTheWorkspaceManager(t *testing.T) {
	for _, h := range []string{"", "${ORCA_TERMINAL_HANDLE}", "  ", "a b"} {
		ft := &fakeTurns{seen: "untouched"}
		s := &Server{Texts: bundledTexts, Turns: ft, Port: 47800}
		rec := call(t, s, "", h, initialize)
		sid := rec.Header().Get("Mcp-Session-Id")
		if rec.Code != http.StatusOK || sid == "" {
			t.Fatalf("%q: %d %s", h, rec.Code, rec.Body)
		}
		if ft.seen != "untouched" {
			t.Errorf("%q: MCPSeen got %q", h, ft.seen)
		}
		list := `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`
		if rec := call(t, s, sid, h, list); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"turn_end"`) {
			t.Errorf("%q: tools/list %d %s", h, rec.Code, rec.Body)
		}
		if rec := call(t, s, sid, "term_1", list); rec.Code != http.StatusBadRequest {
			t.Errorf("%q: a handle-less session took a handle: %d", h, rec.Code)
		}
	}
}

func TestHandlelessSessionsDoNotReplaceEachOther(t *testing.T) {
	s := &Server{Texts: bundledTexts, Turns: &fakeTurns{}, Port: 47800}
	ping := `{"jsonrpc":"2.0","id":2,"method":"ping"}`
	first := call(t, s, "", "", initialize).Header().Get("Mcp-Session-Id")
	second := call(t, s, "", "${ORCA_TERMINAL_HANDLE}", initialize).Header().Get("Mcp-Session-Id")
	if first == "" || first == second {
		t.Fatalf("ids %q %q", first, second)
	}
	for _, sid := range []string{first, second} {
		if rec := call(t, s, sid, "", ping); rec.Code != http.StatusOK {
			t.Fatalf("%s: %d", sid, rec.Code)
		}
	}
	if rec := call(t, s, first, "", "", func(r *http.Request) { r.Method = http.MethodDelete }); rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE: %d", rec.Code)
	}
	if rec := call(t, s, second, "", ping); rec.Code != http.StatusOK {
		t.Fatalf("deleting one handle-less session dropped the other: %d", rec.Code)
	}
}

func TestTurnEndNeedsAHandle(t *testing.T) {
	ft := &fakeTurns{}
	s := &Server{Texts: bundledTexts, Turns: ft, Port: 47800}
	sid := call(t, s, "", "${ORCA_TERMINAL_HANDLE}", initialize).Header().Get("Mcp-Session-Id")
	body := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"turn_end","arguments":{"turn":"r-1","record":{"state":"settled","summary":"s"}}}}`
	r := decodeResult(t, call(t, s, sid, "${ORCA_TERMINAL_HANDLE}", body))
	if !r.Result.IsError || !strings.Contains(r.Result.Content[0].Text, api.SessionHeader) {
		t.Fatalf("%+v", r.Result)
	}
	if ft.recorded != 0 {
		t.Fatal("a handle-less turn_end was recorded")
	}
}

func TestForeignHostAndOriginAreRefused(t *testing.T) {
	s := &Server{Texts: bundledTexts, Turns: &fakeTurns{}, Port: 47800}
	if rec := call(t, s, "", "term_1", initialize, func(r *http.Request) { r.Host = "evil.example:47800" }); rec.Code != http.StatusForbidden {
		t.Errorf("host: %d", rec.Code)
	}
	if rec := call(t, s, "", "term_1", initialize, func(r *http.Request) { r.Header.Set("Origin", "http://127.0.0.1:47800") }); rec.Code != http.StatusForbidden {
		t.Errorf("origin: %d", rec.Code)
	}
	if rec := call(t, s, "", "term_1", initialize, func(r *http.Request) { r.Host = "localhost:47800" }); rec.Code != http.StatusOK {
		t.Errorf("localhost: %d", rec.Code)
	}
}

func TestUnknownSessionIDIsNotFound(t *testing.T) {
	ft := &fakeTurns{}
	s := &Server{Texts: bundledTexts, Turns: ft, Port: 47800}
	rec := call(t, s, "", "term_1", initialize)
	sid := rec.Header().Get("Mcp-Session-Id")
	if rec.Code != http.StatusOK || sid == "" || ft.seen != "term_1" {
		t.Fatalf("%d %q %q", rec.Code, sid, ft.seen)
	}
	list := `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`
	if rec := call(t, s, sid, "term_1", list); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"turn_end"`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	restarted := &Server{Texts: bundledTexts, Turns: ft, Port: 47800}
	if rec := call(t, restarted, sid, "term_1", list); rec.Code != http.StatusNotFound {
		t.Fatalf("a restarted server knew an old session id: %d", rec.Code)
	}
}

func TestDiscoverFallsBackToInitialize(t *testing.T) {
	s := &Server{Texts: bundledTexts, Turns: &fakeTurns{}, Port: 47800}
	rec := call(t, s, "", "term_1", `{"jsonrpc":"2.0","id":0,"method":"server/discover"}`)
	if !strings.Contains(rec.Body.String(), "-32601") {
		t.Fatalf("%s", rec.Body)
	}
}

func TestToolCallRefusesAMalformedRecord(t *testing.T) {
	ft := &fakeTurns{}
	s := &Server{Texts: bundledTexts, Turns: ft, Port: 47800}
	sid := call(t, s, "", "term_1", initialize).Header().Get("Mcp-Session-Id")
	body := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"turn_end","arguments":{"turn":"abc","record":{"state":"asked","summary":"two\nlines","question":{"kind":"choice","text":"q","options":[]}}}}}`
	rec := call(t, s, sid, "term_1", body)
	out := rec.Body.String()
	for _, want := range []string{`"isError":true`, "summary: one line", "question.options: 2 to 5", "resume: required"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in %s", want, out)
		}
	}
	if ft.recorded != 0 {
		t.Fatal("a malformed record reached the turns")
	}
}

type toolResult struct {
	Result struct {
		Content []struct {
			Type, Text string
		} `json:"content"`
		IsError bool `json:"isError"`
	} `json:"result"`
	Error *struct {
		Code int `json:"code"`
	} `json:"error"`
}

func decodeResult(t *testing.T, rec *httptest.ResponseRecorder) toolResult {
	t.Helper()
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("%d %q %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body)
	}
	var r toolResult
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil || r.Error != nil || len(r.Result.Content) != 1 {
		t.Fatalf("%v %s", err, rec.Body)
	}
	return r
}

func TestToolCallRecords(t *testing.T) {
	ft := &fakeTurns{answer: turns.Recorded{Unit: unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}, State: record.Settled, Added: true}}
	s := &Server{Texts: bundledTexts, Turns: ft, Port: 47800}
	sid := call(t, s, "", "term_1", initialize).Header().Get("Mcp-Session-Id")
	body := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"turn_end","arguments":{"turn":"r-1","record":{"state":"settled","summary":"nothing new since my review"}}}}`

	r := decodeResult(t, call(t, s, sid, "term_1", body))
	if r.Result.IsError || r.Result.Content[0].Text != "Recorded settled for acme/api#7. End your turn." {
		t.Fatalf("%+v", r.Result)
	}
	want := turns.RecordRequest{Session: "term_1", Turn: "r-1", Via: "mcp", Record: record.Record{State: record.Settled, Summary: "nothing new since my review"}}
	if !reflect.DeepEqual(ft.last, want) {
		t.Fatalf("request %+v", ft.last)
	}

	ft.answer.Note = "this turn was replaced by turn r-2; your record changes nothing"
	r = decodeResult(t, call(t, s, sid, "term_1", body))
	if r.Result.IsError || !strings.Contains(r.Result.Content[0].Text, ft.answer.Note) {
		t.Fatalf("the late note is not shown: %+v", r.Result)
	}

	ft.err = turns.Refusal{Reason: "this turn belongs to another session; record only the turn your instructions name"}
	r = decodeResult(t, call(t, s, sid, "term_1", body))
	if !r.Result.IsError || !strings.Contains(r.Result.Content[0].Text, "belongs to another session") {
		t.Fatalf("refusal: %+v", r.Result)
	}

	ft.err = record.Problems{"outcome.verdict: a done review names its verdict"}
	r = decodeResult(t, call(t, s, sid, "term_1", body))
	if !r.Result.IsError || !strings.Contains(r.Result.Content[0].Text, "outcome.verdict") {
		t.Fatalf("problems: %+v", r.Result)
	}

	ft.err = errors.New("database is locked")
	r = decodeResult(t, call(t, s, sid, "term_1", body))
	if !r.Result.IsError || !strings.Contains(r.Result.Content[0].Text, "database is locked") {
		t.Fatalf("store error: %+v", r.Result)
	}

	unknown := `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"settle","arguments":{}}}`
	if rec := call(t, s, sid, "term_1", unknown); !strings.Contains(rec.Body.String(), "-32602") {
		t.Fatalf("unknown tool: %s", rec.Body)
	}
	if rec := call(t, s, sid, "term_2", body); rec.Code != http.StatusBadRequest {
		t.Fatalf("another handle on the same MCP session: %d", rec.Code)
	}
	if rec := call(t, s, "", "term_1", body); rec.Code != http.StatusBadRequest {
		t.Fatalf("a tool call without a session id: %d", rec.Code)
	}
}

func TestNotificationsAndPing(t *testing.T) {
	s := &Server{Texts: bundledTexts, Turns: &fakeTurns{}, Port: 47800}
	sid := call(t, s, "", "term_1", initialize).Header().Get("Mcp-Session-Id")
	if rec := call(t, s, sid, "term_1", `{"jsonrpc":"2.0","method":"notifications/initialized"}`); rec.Code != http.StatusAccepted || rec.Body.Len() != 0 {
		t.Fatalf("notification: %d %q", rec.Code, rec.Body)
	}
	if rec := call(t, s, sid, "term_1", `{"jsonrpc":"2.0","id":9,"method":"ping"}`); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"result":{}`) {
		t.Fatalf("ping: %d %s", rec.Code, rec.Body)
	}
	if rec := call(t, s, sid, "term_1", "", func(r *http.Request) { r.Method = http.MethodGet }); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET: %d", rec.Code)
	}
	if rec := call(t, s, sid, "term_1", "", func(r *http.Request) { r.Method = http.MethodDelete }); rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE: %d", rec.Code)
	}
	if rec := call(t, s, sid, "term_1", `{"jsonrpc":"2.0","id":10,"method":"ping"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("after DELETE: %d", rec.Code)
	}
}

func TestInitializeNegotiatesTheVersion(t *testing.T) {
	s := &Server{Texts: bundledTexts, Turns: &fakeTurns{}, Port: 47800, Version: "1.2.3"}
	for client, want := range map[string]string{"2025-03-26": "2025-03-26", "2025-06-18": "2025-06-18", "2099-01-01": "2025-06-18"} {
		body := strings.Replace(initialize, "2025-06-18", client, 1)
		var got struct {
			Result struct {
				ProtocolVersion string         `json:"protocolVersion"`
				Capabilities    map[string]any `json:"capabilities"`
				ServerInfo      struct{ Name, Version string }
			} `json:"result"`
		}
		rec := call(t, s, "", "term_1", body)
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		r := got.Result
		if r.ProtocolVersion != want || r.ServerInfo.Name != brand.Name || r.ServerInfo.Version != "1.2.3" || r.Capabilities["tools"] == nil {
			t.Errorf("%s: %s", client, rec.Body)
		}
	}
}

func TestOversizedBodyIsRefused(t *testing.T) {
	s := &Server{Texts: bundledTexts, Turns: &fakeTurns{}, Port: 47800}
	big := `{"jsonrpc":"2.0","id":1,"method":"ping","params":{"x":"` + strings.Repeat("a", maxBody) + `"}}`
	if rec := call(t, s, "", "term_1", big); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("%d", rec.Code)
	}
}

// jsonFields lists the JSON paths of every field of t, nested types
// included.
func jsonFields(t reflect.Type, prefix string) []string {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	var out []string
	for i := range t.NumField() {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		out = append(out, prefix+name)
		out = append(out, jsonFields(f.Type, prefix+name+".")...)
	}
	return out
}

func schemaFields(t *testing.T, s *schema, prefix string) []string {
	if s.Items != nil {
		s = s.Items
	}
	if s.Type == "object" && (s.AdditionalProperties == nil || *s.AdditionalProperties) {
		t.Errorf("%q allows additional properties", prefix)
	}
	var out []string
	for name, p := range s.Properties {
		out = append(out, prefix+name)
		out = append(out, schemaFields(t, p, prefix+name+".")...)
	}
	return out
}

func TestSchemaMatchesTheRecord(t *testing.T) {
	in := inputSchema()
	if !slices.Equal(in.Required, []string{"turn", "record"}) || len(in.Properties) != 2 {
		t.Fatalf("arguments: %+v", in)
	}
	got := schemaFields(t, in.Properties["record"], "")
	want := jsonFields(reflect.TypeFor[record.Record](), "")
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("schema fields\n%v\nrecord fields\n%v", got, want)
	}
	if _, err := json.Marshal(in); err != nil {
		t.Fatal(err)
	}
}

func TestOneMCPSessionPerHandle(t *testing.T) {
	s := &Server{Texts: bundledTexts, Turns: &fakeTurns{}, Port: 47800}
	ping := `{"jsonrpc":"2.0","id":2,"method":"ping"}`
	first := call(t, s, "", "term_1", initialize).Header().Get("Mcp-Session-Id")
	other := call(t, s, "", "term_2", initialize).Header().Get("Mcp-Session-Id")
	second := call(t, s, "", "term_1", initialize).Header().Get("Mcp-Session-Id")
	if rec := call(t, s, first, "term_1", ping); rec.Code != http.StatusNotFound {
		t.Fatalf("the replaced id still answers: %d", rec.Code)
	}
	for sid, h := range map[string]string{second: "term_1", other: "term_2"} {
		if rec := call(t, s, sid, h, ping); rec.Code != http.StatusOK {
			t.Fatalf("%s: %d", h, rec.Code)
		}
	}
	if len(s.ids) != 2 || len(s.byHandle) != 2 {
		t.Fatalf("ids %v by handle %v", s.ids, s.byHandle)
	}
}

func TestDeleteRefusesAnotherHandlesSession(t *testing.T) {
	s := &Server{Texts: bundledTexts, Turns: &fakeTurns{}, Port: 47800}
	sid := call(t, s, "", "term_1", initialize).Header().Get("Mcp-Session-Id")
	del := func(r *http.Request) { r.Method = http.MethodDelete }
	if rec := call(t, s, sid, "term_2", "", del); rec.Code != http.StatusBadRequest {
		t.Fatalf("DELETE by another handle: %d", rec.Code)
	}
	if rec := call(t, s, sid, "term_1", `{"jsonrpc":"2.0","id":2,"method":"ping"}`); rec.Code != http.StatusOK {
		t.Fatalf("the session was forgotten: %d", rec.Code)
	}
	if rec := call(t, s, sid, "term_1", "", del); rec.Code != http.StatusNoContent || len(s.byHandle) != 0 {
		t.Fatalf("DELETE: %d %v", rec.Code, s.byHandle)
	}
}

func TestToolCallRefusesExtraArgumentsAndTrimsTheRound(t *testing.T) {
	ft := &fakeTurns{answer: turns.Recorded{Unit: unit.Unit{Provider: unit.GitHub, Key: "acme/api#7"}, State: record.Settled}}
	s := &Server{Texts: bundledTexts, Turns: ft, Port: 47800}
	sid := call(t, s, "", "term_1", initialize).Header().Get("Mcp-Session-Id")
	extra := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"turn_end","arguments":{"turn":"r-1","pr":"acme/api#7","state":"done","record":{"state":"settled","summary":"s"}}}}`
	r := decodeResult(t, call(t, s, sid, "term_1", extra))
	if !r.Result.IsError || !strings.Contains(r.Result.Content[0].Text, "arguments: only turn and record, not pr, state") || ft.recorded != 0 {
		t.Fatalf("%+v", r.Result)
	}
	padded := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"turn_end","arguments":{"turn":"  r-1\n","record":{"state":"settled","summary":"s"}}}}`
	if r := decodeResult(t, call(t, s, sid, "term_1", padded)); r.Result.IsError || ft.last.Turn != "r-1" {
		t.Fatalf("%+v %q", r.Result, ft.last.Turn)
	}
	badType := `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"turn_end","arguments":{"turn":7,"record":{"state":"settled","summary":"s"}}}}`
	if r := decodeResult(t, call(t, s, sid, "term_1", badType)); !r.Result.IsError || !strings.Contains(r.Result.Content[0].Text, "turn: a string") {
		t.Fatalf("%+v", r.Result)
	}
}

func TestOnlyJSONRPC20IsServed(t *testing.T) {
	s := &Server{Texts: bundledTexts, Turns: &fakeTurns{}, Port: 47800}
	for _, body := range []string{
		`{"id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"1.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":1,"method":"ping","params":{}}`,
	} {
		rec := call(t, s, "", "term_1", body)
		ok := strings.Contains(body, `"2.0"`)
		if ok != (rec.Code == http.StatusOK) || (!ok && !strings.Contains(rec.Body.String(), "-32600")) {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}
}

func TestTurnEndEnumsAreTheFullTables(t *testing.T) {
	rec := inputSchema().Properties["record"]
	if got, want := rec.Properties["state"].Enum, []string{"done", "settled", "asked", "failed"}; !slices.Equal(got, want) {
		t.Errorf("state enum %v, want %v", got, want)
	}
	if got, want := rec.Properties["outcome"].Properties["verdict"].Enum, []string{"APPROVE", "COMMENT", "REQUEST_CHANGES"}; !slices.Equal(got, want) {
		t.Errorf("verdict enum %v, want %v", got, want)
	}
	if got, want := rec.Properties["question"].Properties["kind"].Enum, []string{"choice", "confirm", "free"}; !slices.Equal(got, want) {
		t.Errorf("question kind enum %v, want %v", got, want)
	}
	if got := rec.Properties["artifact"].Properties["kind"].Enum; !slices.Equal(got, record.ArtifactKinds) {
		t.Errorf("artifact kind enum %v, want %v", got, record.ArtifactKinds)
	}
}
