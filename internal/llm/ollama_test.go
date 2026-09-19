package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/10txn/digicli/internal/types"
)

// collect drains a stream into the full reply text.
func collect(t *testing.T, stream Stream) (string, error) {
	t.Helper()
	var b strings.Builder
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return b.String(), nil
		}
		if err != nil {
			return b.String(), err
		}
		b.WriteString(chunk.Text)
	}
}

// frames writes newline-delimited JSON the way Ollama streams it.
func frames(w http.ResponseWriter, chunks ...string) {
	for _, chunk := range chunks {
		json.NewEncoder(w).Encode(chatChunk{Message: chatMessage{Role: "assistant", Content: chunk}})
	}
	json.NewEncoder(w).Encode(chatChunk{Done: true})
}

func TestChatStreamsChunksInOrder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("got path %s, want /api/chat", r.URL.Path)
		}

		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decoding request: %v", err)
		}
		if !req.Stream {
			t.Error("request did not ask for a stream")
		}
		if req.Model != "test-model" {
			t.Errorf("got model %q, want test-model", req.Model)
		}
		// The system prompt must survive into the request.
		if len(req.Messages) != 2 || req.Messages[0].Role != "system" {
			t.Errorf("unexpected messages: %+v", req.Messages)
		}

		frames(w, "Hello", ", ", "world")
	}))
	defer server.Close()

	stream, err := NewOllama(server.URL, "test-model").Chat(context.Background(), []types.Message{
		{Role: types.RoleSystem, Content: "be brief"},
		{Role: types.RoleUser, Content: "hi"},
	}, nil)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	defer stream.Close()

	got, err := collect(t, stream)
	if err != nil {
		t.Fatalf("draining the stream: %v", err)
	}
	if want := "Hello, world"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Ollama sends a role-only frame before the text; it must not surface as an
// empty chunk.
func TestChatSkipsEmptyFrames(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		frames(w, "", "text", "")
	}))
	defer server.Close()

	stream, err := NewOllama(server.URL, "m").Chat(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	defer stream.Close()

	chunk, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv: %v", err)
	}
	if chunk.Text != "text" {
		t.Errorf("got %q, want the first non-empty chunk", chunk.Text)
	}
}

// A missing model is the most likely failure, so the message has to name it
// and say how to fix it.
func TestChatReportsMissingModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{
			"error": `model "ghost" not found`,
		})
	}))
	defer server.Close()

	_, err := NewOllama(server.URL, "ghost").Chat(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("expected an error for a missing model")
	}
	if !strings.Contains(err.Error(), "ollama pull ghost") {
		t.Errorf("error should say how to fix it, got: %v", err)
	}
}

// An error can also arrive mid-stream, after a 200.
func TestChatReportsErrorFrame(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(chatChunk{Message: chatMessage{Content: "partial"}})
		json.NewEncoder(w).Encode(chatChunk{Error: "out of memory"})
	}))
	defer server.Close()

	stream, err := NewOllama(server.URL, "m").Chat(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	defer stream.Close()

	got, err := collect(t, stream)
	if got != "partial" {
		t.Errorf("text before the error: got %q, want %q", got, "partial")
	}
	if err == nil || !strings.Contains(err.Error(), "out of memory") {
		t.Errorf("got error %v, want it to mention out of memory", err)
	}
}

func TestChatCancellationStopsTheStream(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(chatChunk{Message: chatMessage{Content: "first"}})
		w.(http.Flusher).Flush()
		<-release // hold the connection open until the test is done
	}))
	defer server.Close()
	defer close(release)

	ctx, cancel := context.WithCancel(context.Background())
	stream, err := NewOllama(server.URL, "m").Chat(ctx, nil, nil)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	defer stream.Close()

	if chunk, err := stream.Recv(); err != nil || chunk.Text != "first" {
		t.Fatalf("first chunk: %q, %v", chunk.Text, err)
	}

	cancel()

	// The next receive must fail rather than block forever.
	done := make(chan error, 1)
	go func() {
		_, err := stream.Recv()
		done <- err
	}()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("got %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Recv did not return after the context was cancelled")
	}
}

func TestChatReportsUnreachableServer(t *testing.T) {
	// A port nothing is listening on.
	_, err := NewOllama("http://127.0.0.1:1", "m").Chat(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("expected an error for an unreachable server")
	}
	if !strings.Contains(err.Error(), "is it running") {
		t.Errorf("error should suggest starting Ollama, got: %v", err)
	}
}

func TestOllamaURL(t *testing.T) {
	tests := []struct {
		endpoint string
		want     string
		wantErr  bool
	}{
		{"http://localhost:11434", "http://localhost:11434/api/chat", false},
		{"http://localhost:11434/", "http://localhost:11434/api/chat", false},
		{"https://ollama.example.com/proxy", "https://ollama.example.com/proxy/api/chat", false},
		{"localhost:11434", "", true}, // no scheme, so no host
		{"", "", true},
	}
	for _, tt := range tests {
		got, err := ollamaURL(tt.endpoint, "/api/chat")
		if (err != nil) != tt.wantErr {
			t.Errorf("ollamaURL(%q): error %v, wantErr %v", tt.endpoint, err, tt.wantErr)
			continue
		}
		if !tt.wantErr && got != tt.want {
			t.Errorf("ollamaURL(%q) = %q, want %q", tt.endpoint, got, tt.want)
		}
	}
}

func TestListModelsParsesDetails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			t.Errorf("got path %s, want /api/tags", r.URL.Path)
		}
		io.WriteString(w, `{"models":[{"name":"qwen2.5:3b","size":1929912432,
			"details":{"parameter_size":"3.1B","quantization_level":"Q4_K_M","context_length":32768}}]}`)
	}))
	defer server.Close()

	models, err := ListOllamaModels(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("ListOllamaModels: %v", err)
	}
	if len(models) != 1 {
		t.Fatalf("got %d models, want 1", len(models))
	}
	got := models[0]
	if got.Name != "qwen2.5:3b" || got.Parameters != "3.1B" || got.ContextLength != 32768 {
		t.Errorf("unexpected model: %+v", got)
	}
}

// toolFrames is a tool call as Ollama streams one: its own frame, then the
// frame that ends the reply.
func toolFrames(call string) string {
	return `{"message":{"role":"assistant","content":"","tool_calls":[` + call + `]},"done":false}` + "\n" +
		`{"message":{"role":"assistant","content":""},"done":true,"done_reason":"stop"}` + "\n"
}

// serveRaw replays literal frames, so these tests exercise the wire shapes
// Ollama actually produces rather than whatever our own structs encode.
func serveRaw(t *testing.T, body string) Stream {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)

	stream, err := NewOllama(server.URL, "m").Chat(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	t.Cleanup(func() { stream.Close() })
	return stream
}

// firstCall drains a stream and returns the single tool call it carried.
func firstCall(t *testing.T, stream Stream) types.ToolCall {
	t.Helper()
	var calls []types.ToolCall
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		calls = append(calls, chunk.ToolCalls...)
	}
	if len(calls) != 1 {
		t.Fatalf("got %d tool calls, want 1", len(calls))
	}
	return calls[0]
}

func TestChatParsesAToolCall(t *testing.T) {
	stream := serveRaw(t, toolFrames(
		`{"function":{"name":"write_file","arguments":{"path":"new.go","content":"package new\n"}}}`))

	got := firstCall(t, stream)
	if got.Name != "write_file" {
		t.Errorf("got tool %q, want write_file", got.Name)
	}
	if path, ok := got.StringArg("path"); !ok || path != "new.go" {
		t.Errorf("path = %q (ok=%v), want new.go", path, ok)
	}
	if content, ok := got.StringArg("content"); !ok || content != "package new\n" {
		t.Errorf("content = %q (ok=%v)", content, ok)
	}
	if got.ID == "" {
		t.Error("a call with no id of its own was not given one")
	}
}

// Arguments encoded into a string rather than sent as an object. Undecoded,
// every argument reads as missing and a perfectly good write comes back as
// "write_file needs a path argument".
func TestChatParsesDoubleEncodedArguments(t *testing.T) {
	stream := serveRaw(t, toolFrames(
		`{"function":{"name":"write_file","arguments":"{\"path\":\"new.go\",\"content\":\"package new\\n\"}"}}`))

	got := firstCall(t, stream)
	if path, ok := got.StringArg("path"); !ok || path != "new.go" {
		t.Errorf("path = %q (ok=%v), want new.go", path, ok)
	}
	if content, ok := got.StringArg("content"); !ok || content != "package new\n" {
		t.Errorf("content = %q (ok=%v), want the file contents", content, ok)
	}
	// The call goes back into the history too, so it must be normalised there
	// rather than only where it is read.
	if string(got.Arguments[0]) == `"` {
		t.Errorf("arguments are still double-encoded: %s", got.Arguments)
	}
}

// The final frame can carry a call as well as done, and the call must not be
// lost to the end of the reply.
func TestChatKeepsAToolCallOnTheFinalFrame(t *testing.T) {
	stream := serveRaw(t,
		`{"message":{"role":"assistant","content":"Writing it.","tool_calls":[{"function":{"name":"write_file","arguments":{"path":"new.go","content":"x"}}}]},"done":true}`+"\n")

	if got := firstCall(t, stream); got.Name != "write_file" {
		t.Errorf("got tool %q, want write_file", got.Name)
	}
}

// Several calls in one frame keep their own identities, or their results
// cannot be matched back to them.
func TestChatParsesSeveralToolCalls(t *testing.T) {
	stream := serveRaw(t, toolFrames(
		`{"id":"call_1","function":{"name":"read_file","arguments":{"path":"a.go"}}},`+
			`{"function":{"name":"write_file","arguments":{"path":"b.go","content":"y"}}}`))

	var calls []types.ToolCall
	for {
		chunk, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Recv: %v", err)
		}
		calls = append(calls, chunk.ToolCalls...)
	}

	if len(calls) != 2 {
		t.Fatalf("got %d calls, want 2", len(calls))
	}
	if calls[0].ID != "call_1" {
		t.Errorf("an id Ollama supplied was discarded: %q", calls[0].ID)
	}
	if calls[0].ID == calls[1].ID {
		t.Errorf("both calls share the id %q", calls[0].ID)
	}
}
