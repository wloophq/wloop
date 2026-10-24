package claude

import "testing"

func TestProtocolDecodesSessionStartWithAnUnknownField(t *testing.T) {
	sess, source, cwd, err := Protocol{}.SessionStart([]byte(`{"session_id":"s","source":"startup","cwd":"/c","model":"x"}`))
	if err != nil || sess != "s" || source != "startup" || cwd != "/c" {
		t.Fatalf("got %q %q %q %v", sess, source, cwd, err)
	}
}

func TestProtocolDecodesStopWithAnUnknownField(t *testing.T) {
	sess, prompt, last, err := Protocol{}.Stop([]byte(`{"session_id":"s","prompt_id":"p","last_assistant_message":"m","stop_hook_active":true}`))
	if err != nil || sess != "s" || prompt != "p" || last != "m" {
		t.Fatalf("got %q %q %q %v", sess, prompt, last, err)
	}
}

func TestProtocolRefusesMalformedInput(t *testing.T) {
	if _, _, _, err := (Protocol{}).Stop([]byte("not json")); err == nil {
		t.Fatal("decoded")
	}
	if _, _, _, err := (Protocol{}).SessionStart([]byte("{")); err == nil {
		t.Fatal("decoded")
	}
}

func TestTranscriptPath(t *testing.T) {
	for raw, want := range map[string]string{
		`{"session_id":"s","transcript_path":"/h/projects/-w/s.jsonl","cwd":"/w"}`: "/h/projects/-w/s.jsonl",
		`{"session_id":"s"}`: "",
		`not json`:           "",
	} {
		if got := (Protocol{}).Transcript([]byte(raw)); got != want {
			t.Errorf("Transcript(%s) = %q, want %q", raw, got, want)
		}
	}
}

func TestProtocolBlockBytes(t *testing.T) {
	want := `{"decision":"block","reason":"a ` + `\` + `u0026 \"b\""}` + "\n"
	if got := string(Protocol{}.Block(`a & "b"`)); got != want {
		t.Fatalf("got %q", got)
	}
}

func TestProtocolReadsTheEnvironment(t *testing.T) {
	env := map[string]string{"CLAUDE_CODE_SESSION_ID": "s", "CLAUDE_CONFIG_DIR": "/h"}
	getenv := func(k string) string { return env[k] }
	if got := (Protocol{}).SessionID(getenv); got != "s" {
		t.Fatalf("session %q", got)
	}
	if got := (Protocol{}).Home(getenv); got != "/h" {
		t.Fatalf("home %q", got)
	}
}
