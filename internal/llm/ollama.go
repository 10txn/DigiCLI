package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/10txn/digicli/internal/types"
)

// Ollama talks to a local Ollama server over its HTTP API.
type Ollama struct {
	Endpoint string
	Model    string
	// client has no timeout of its own: streaming replies run for as long
	// as the caller's context allows.
	client *http.Client
}

// NewOllama builds a provider for a given endpoint and model.
func NewOllama(endpoint, model string) *Ollama {
	return &Ollama{
		Endpoint: endpoint,
		Model:    model,
		client:   &http.Client{},
	}
}

func (o *Ollama) Name() string { return "ollama" }

// ollamaURL joins an API path onto a configured endpoint.
func ollamaURL(endpoint, path string) (string, error) {
	base, err := url.Parse(strings.TrimSuffix(endpoint, "/"))
	if err != nil || base.Host == "" {
		return "", fmt.Errorf("invalid Ollama endpoint %q", endpoint)
	}
	base.Path = strings.TrimSuffix(base.Path, "/") + path
	return base.String(), nil
}

// unreachable turns a transport error into something worth showing a user,
// since the overwhelmingly common cause is that Ollama is not running.
func unreachable(endpoint string, err error) error {
	if errors.Is(err, context.Canceled) {
		return err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("Ollama at %s did not respond in time", endpoint)
	}
	return fmt.Errorf("could not reach Ollama at %s — is it running? (try: ollama serve)", endpoint)
}

// chatRequest is the payload for POST /api/chat.
type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
	Tools    []ollamaTool  `json:"tools,omitempty"`
}

type chatMessage struct {
	Role      string           `json:"role"`
	Content   string           `json:"content"`
	ToolCalls []ollamaToolCall `json:"tool_calls,omitempty"`
	// ToolName identifies which tool a role:"tool" message answers. Ollama
	// has no call IDs, so the name is the only correlation available.
	ToolName string `json:"tool_name,omitempty"`
}

// ollamaTool is a tool definition in Ollama's (OpenAI-shaped) schema.
type ollamaTool struct {
	Type     string             `json:"type"`
	Function ollamaToolFunction `json:"function"`
}

type ollamaToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type ollamaToolCall struct {
	// ID is present on recent Ollama versions and absent on older ones.
	ID       string `json:"id,omitempty"`
	Function struct {
		Name string `json:"name"`
		// Ollama sends arguments as a JSON object, not the encoded
		// string OpenAI uses.
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

// chatChunk is one newline-delimited JSON frame of a streamed reply.
type chatChunk struct {
	Message chatMessage `json:"message"`
	Done    bool        `json:"done"`
	Error   string      `json:"error"`
}

// Chat streams a reply from the configured model.
func (o *Ollama) Chat(ctx context.Context, messages []types.Message, tools []types.ToolDefinition) (Stream, error) {
	endpoint, err := ollamaURL(o.Endpoint, "/api/chat")
	if err != nil {
		return nil, err
	}

	payload := chatRequest{Model: o.Model, Stream: true}
	for _, msg := range messages {
		payload.Messages = append(payload.Messages, toOllamaMessage(msg))
	}
	for _, tool := range tools {
		payload.Tools = append(payload.Tools, ollamaTool{
			Type: "function",
			Function: ollamaToolFunction{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.Parameters,
			},
		})
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encoding the chat request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, unreachable(o.Endpoint, err)
	}

	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, o.responseError(resp)
	}

	return &ollamaStream{resp: resp, decoder: json.NewDecoder(resp.Body)}, nil
}

// toOllamaMessage maps one history entry onto Ollama's message shape.
func toOllamaMessage(msg types.Message) chatMessage {
	out := chatMessage{
		Role:     string(msg.Role),
		Content:  msg.Content,
		ToolName: msg.ToolName,
	}
	for _, call := range msg.ToolCalls {
		var oc ollamaToolCall
		oc.ID = call.ID
		oc.Function.Name = call.Name
		oc.Function.Arguments = call.Arguments
		out.ToolCalls = append(out.ToolCalls, oc)
	}
	return out
}

// responseError reads Ollama's error body, which carries the genuinely useful
// message — a missing model names itself and says to pull it.
func (o *Ollama) responseError(resp *http.Response) error {
	var payload struct {
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err == nil && payload.Error != "" {
		if resp.StatusCode == http.StatusNotFound && strings.Contains(payload.Error, "not found") {
			return fmt.Errorf("%s — pull it first: ollama pull %s", payload.Error, o.Model)
		}
		return fmt.Errorf("Ollama: %s", payload.Error)
	}
	return fmt.Errorf("Ollama returned %s", resp.Status)
}

// ollamaStream decodes the newline-delimited JSON frames of a chat reply.
type ollamaStream struct {
	resp    *http.Response
	decoder *json.Decoder
}

func (s *ollamaStream) Recv() (Chunk, error) {
	for {
		var frame chatChunk
		if err := s.decoder.Decode(&frame); err != nil {
			if errors.Is(err, io.ErrUnexpectedEOF) {
				// A truncated frame means the server went away mid-reply.
				return Chunk{}, io.EOF
			}
			return Chunk{}, err
		}
		if frame.Error != "" {
			return Chunk{}, fmt.Errorf("Ollama: %s", frame.Error)
		}

		calls := toToolCalls(frame.Message.ToolCalls)

		// The final frame can still carry tool calls, so check for those
		// before treating done as the end of the reply.
		if len(calls) > 0 || frame.Message.Content != "" {
			return Chunk{Text: frame.Message.Content, ToolCalls: calls}, nil
		}
		if frame.Done {
			return Chunk{}, io.EOF
		}
		// A role-only frame with nothing in it; keep reading rather than
		// surfacing an empty chunk.
	}
}

// toToolCalls converts Ollama's tool calls, giving each a local ID since
// Ollama does not supply one.
func toToolCalls(in []ollamaToolCall) []types.ToolCall {
	if len(in) == 0 {
		return nil
	}
	out := make([]types.ToolCall, 0, len(in))
	for i, call := range in {
		// Normalised here rather than only at the point of use, because the
		// call goes back into the history too: arguments that stayed
		// double-encoded would be echoed to the model in that shape next turn,
		// teaching it to keep producing them.
		args := types.NormalizeArguments(call.Function.Arguments)
		if len(args) == 0 {
			args = json.RawMessage(`{}`)
		}
		id := call.ID
		if id == "" {
			// Older Ollama versions send no id; synthesise a stable one.
			id = fmt.Sprintf("%s-%d", call.Function.Name, i)
		}
		out = append(out, types.ToolCall{
			ID:        id,
			Name:      call.Function.Name,
			Arguments: args,
		})
	}
	return out
}

func (s *ollamaStream) Close() error {
	return s.resp.Body.Close()
}

// Model describes a model available from a provider.
type Model struct {
	Name       string
	Size       int64
	Parameters string
	Quantized  string
	// ContextLength is the model's context window in tokens, or 0 when the
	// provider does not report one.
	ContextLength int
	Modified      time.Time
}

// tagsResponse mirrors the parts of Ollama's /api/tags payload we use.
type tagsResponse struct {
	Models []struct {
		Name       string    `json:"name"`
		Size       int64     `json:"size"`
		ModifiedAt time.Time `json:"modified_at"`
		Details    struct {
			ParameterSize     string `json:"parameter_size"`
			QuantizationLevel string `json:"quantization_level"`
			ContextLength     int    `json:"context_length"`
		} `json:"details"`
	} `json:"models"`
}

// ListOllamaModels asks a local Ollama server which models it has pulled.
func ListOllamaModels(ctx context.Context, endpoint string) ([]Model, error) {
	target, err := ollamaURL(endpoint, "/api/tags")
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, unreachable(endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Ollama returned %s", resp.Status)
	}

	var payload tagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("could not parse the Ollama model list: %w", err)
	}

	models := make([]Model, 0, len(payload.Models))
	for _, m := range payload.Models {
		models = append(models, Model{
			Name:          m.Name,
			Size:          m.Size,
			Parameters:    m.Details.ParameterSize,
			Quantized:     m.Details.QuantizationLevel,
			ContextLength: m.Details.ContextLength,
			Modified:      m.ModifiedAt,
		})
	}
	return models, nil
}
