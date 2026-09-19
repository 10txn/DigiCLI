package agent

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/10txn/digicli/internal/types"
)

// A local model does not always get its tool call into the channel meant for
// one. The call is templated into the reply as text, Ollama's parser misses it,
// and it arrives here as prose — which for write_file means the whole file the
// model meant to write is printed into the transcript and nothing is written.
// It is intermittent by nature: the same model and the same prompt will emit a
// proper call one turn and a textual one the next, and a long argument makes
// the textual one more likely. It is not a sign of a model that cannot call
// tools; a model whose template handles tool calls perfectly well still drifts
// out of the format mid-reply.
//
// Recover finds those calls and hands them back as real ones, so they go
// through the same policy, the same approval prompt and the same tools as any
// other call.
//
// How certain the recovery is varies, and the difference matters enough to
// report rather than average away:
//
//   - Delimited. The call sits inside the wrapper a template uses for exactly
//     this (<tool_call>, [TOOL_CALLS]), or it is the entire reply. Nothing else
//     is being said, so there is nothing else it could be.
//   - Inferred. The reply is prose with a fenced JSON call in the middle of it.
//     A model that means to call a tool writes this, and so does a model
//     explaining the call it would make — the two are not reliably different in
//     the text. Recovering it is a judgement, so the caller is told, and treats
//     a change to disk as something to ask about rather than assume.
//
// Either way a recovered call is a proposal, never a permission: the mode
// policy and the guard rule on it exactly as they would on a real one.

// Recovery is what a reply turned out to contain.
type Recovery struct {
	// Calls are the tool calls written into the reply as text.
	Calls []types.ToolCall
	// Text is the reply with the calls taken out — what the model actually
	// said, which is worth keeping and showing.
	Text string
	// Delimited reports that the call announced itself, rather than being
	// inferred from a fenced block in the middle of prose.
	Delimited bool
}

// Recover extracts tool calls a model wrote as text instead of emitting. It
// reports false when the text is just text, leaving the reply untouched.
func Recover(text string, known []string) (Recovery, bool) {
	payloads, remaining, delimited, ok := isolate(text)
	if !ok {
		return Recovery{Text: text}, false
	}

	var calls []types.ToolCall
	for _, payload := range payloads {
		found, ok := parseCalls(payload, known)
		if !ok {
			// One unparseable payload makes the whole reply suspect: a
			// half-recovered turn would run some of what the model asked for
			// and silently drop the rest.
			return Recovery{Text: text}, false
		}
		calls = append(calls, found...)
	}
	if len(calls) == 0 {
		return Recovery{Text: text}, false
	}

	// IDs are synthesised the same way the Ollama client does for a provider
	// that supplies none, so a result can be matched back to its call.
	for i := range calls {
		calls[i].ID = fmt.Sprintf("%s-%d", calls[i].Name, i)
	}
	return Recovery{
		Calls:     calls,
		Text:      strings.TrimSpace(remaining),
		Delimited: delimited,
	}, true
}

// The wrappers models put around a call they are emitting as text. Qwen and
// Hermes templates use <tool_call>; Mistral's uses [TOOL_CALLS].
const (
	openTag   = "<tool_call>"
	closeTag  = "</tool_call>"
	callsMark = "[TOOL_CALLS]"
)

// isolate finds the parts of a reply that are a tool call rather than text, and
// returns them alongside whatever the model said around them.
func isolate(text string) (payloads []string, remaining string, delimited, ok bool) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return nil, text, false, false
	}

	// A wrapper says plainly which part is the call, so the surrounding text is
	// safe to keep and show: the model often explains itself around it.
	if strings.Contains(trimmed, openTag) {
		var rest strings.Builder
		for {
			start := strings.Index(trimmed, openTag)
			if start < 0 {
				break
			}
			rest.WriteString(trimmed[:start])
			trimmed = trimmed[start+len(openTag):]

			// A missing closing tag means the reply was cut off mid-call; the
			// rest of the message is the payload, and may still parse.
			end := strings.Index(trimmed, closeTag)
			if end < 0 {
				payloads = append(payloads, trimmed)
				trimmed = ""
				break
			}
			payloads = append(payloads, trimmed[:end])
			trimmed = trimmed[end+len(closeTag):]
		}
		rest.WriteString(trimmed)
		return payloads, rest.String(), true, len(payloads) > 0
	}

	if i := strings.Index(trimmed, callsMark); i >= 0 {
		return []string{trimmed[i+len(callsMark):]}, trimmed[:i], true, true
	}

	// The whole reply is the call and nothing else — fenced or not. A model
	// with nothing to say but the call is not explaining anything.
	if body, rest, found := fence(trimmed); found && strings.TrimSpace(rest) == "" {
		return []string{body}, "", true, true
	}
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return []string{trimmed}, "", true, true
	}

	// A fenced call in the middle of prose. This is the judgement: a model
	// working through a task writes exactly this, and so does one describing
	// the call it would make. Recovered, but marked as inferred.
	if body, rest, found := fence(trimmed); found {
		return []string{body}, rest, false, true
	}

	// Bare JSON inline in a sentence is left alone. A model emitting a call
	// fences it; one quoting a call mid-sentence is talking about it.
	return nil, text, false, false
}

// splitFence finds the first ``` block and takes the reply apart around it:
// what came before, the block itself, its contents, and what follows. Callers
// that want to drop the block join prefix and suffix; callers that want to keep
// it put raw back between them.
func splitFence(text string) (prefix, raw, body, suffix string, found bool) {
	start := strings.Index(text, "```")
	if start < 0 {
		return text, "", "", "", false
	}
	prefix = text[:start]
	after := text[start+3:]

	end := strings.Index(after, "```")
	if end < 0 {
		// An unclosed fence, from a reply still arriving or cut off.
		body, suffix = after, ""
	} else {
		body, suffix = after[:end], after[end+3:]
	}
	raw = text[start : len(text)-len(suffix)]

	// Drop the language tag, which is on the same line as the opening fence.
	if i := strings.IndexByte(body, '\n'); i >= 0 {
		if tag := strings.TrimSpace(body[:i]); tag == "" || !strings.ContainsAny(tag, " \t{[") {
			body = body[i+1:]
		}
	}
	return prefix, raw, strings.TrimSpace(body), suffix, true
}

// fence pulls the first ``` block out of a reply, returning the block's body
// and the text around it.
func fence(text string) (body, rest string, found bool) {
	prefix, _, body, suffix, found := splitFence(text)
	return body, prefix + suffix, found
}

// textualCall is the shape a model writes a call in. The field names vary by
// template, and a model that is already off the rails varies them further, so
// every spelling that carries the same meaning is accepted.
type textualCall struct {
	Name     string `json:"name"`
	ToolName string `json:"tool_name"`
	Tool     string `json:"tool"`

	Arguments  json.RawMessage `json:"arguments"`
	Parameters json.RawMessage `json:"parameters"`
	Args       json.RawMessage `json:"args"`
	Input      json.RawMessage `json:"input"`

	// Function and ToolCall are the nested spellings: {"function": {...}} is
	// OpenAI's, {"tool_call": {...}} is what a model writing one out by hand
	// tends to produce.
	Function *textualCall `json:"function"`
	ToolCall *textualCall `json:"tool_call"`
}

// parseCalls turns one payload into calls, or reports that it is not one.
func parseCalls(payload string, known []string) ([]types.ToolCall, bool) {
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return nil, false
	}

	// Either a single call or a list of them, which is what the [TOOL_CALLS]
	// marker is always followed by.
	var raw []textualCall
	if strings.HasPrefix(payload, "[") {
		if err := json.Unmarshal([]byte(payload), &raw); err != nil {
			return nil, false
		}
	} else {
		var one textualCall
		if err := json.Unmarshal([]byte(payload), &one); err != nil {
			return nil, false
		}
		raw = []textualCall{one}
	}

	calls := make([]types.ToolCall, 0, len(raw))
	for _, entry := range raw {
		call, ok := entry.resolve(known)
		if !ok {
			return nil, false
		}
		calls = append(calls, call)
	}
	return calls, len(calls) > 0
}

// resolve pulls a name and arguments out of one written-out call, following the
// nested spellings, and checks the name against the tools this session has.
func (c textualCall) resolve(known []string) (types.ToolCall, bool) {
	inner := c
	for depth := 0; depth < 3; depth++ {
		switch {
		case inner.Function != nil:
			inner = *inner.Function
		case inner.ToolCall != nil:
			inner = *inner.ToolCall
		default:
			depth = 3
		}
	}

	name := firstNonEmpty(inner.Name, inner.ToolName, inner.Tool, c.Name, c.ToolName, c.Tool)
	if name == "" || !slices.Contains(known, name) {
		return types.ToolCall{}, false
	}

	args := firstRaw(inner.Arguments, inner.Parameters, inner.Args, inner.Input)
	if len(args) == 0 {
		// A call that genuinely takes nothing is still a call.
		args = json.RawMessage(`{}`)
	}
	args = types.NormalizeArguments(args)

	// Arguments have to be an object, or the tool has nothing to read them
	// from — and a bare string here usually means the model wrote prose where
	// the arguments go.
	var fields map[string]any
	if err := json.Unmarshal(args, &fields); err != nil {
		return types.ToolCall{}, false
	}

	return types.ToolCall{Name: name, Arguments: args}, true
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func firstRaw(values ...json.RawMessage) json.RawMessage {
	for _, v := range values {
		if len(v) > 0 && string(v) != "null" {
			return v
		}
	}
	return nil
}

// Visible strips the parts of a reply that are a tool call rather than
// something the user is meant to read.
//
// Recover already takes a call out of the reply it finishes in, but a reply is
// on screen long before it finishes: the text streams in token by token, so a
// call templated into it is printed, character by character, in front of the
// user — a wall of JSON for a write, since the whole file goes through the
// arguments. Rendering through this keeps it off the screen from the start.
//
// It runs on every frame of a stream, so it is written to do nothing at all in
// the common case: a reply with no marker in it is returned untouched.
func Visible(text string, known []string) string {
	// A marker arrives a character at a time, so the frames before it is
	// complete would otherwise show "<tool_ca" on the end of the reply. This
	// runs before the cheap test below, which only recognises a whole marker.
	text = trimPartial(text, openTag, callsMark)
	if !mightHoldCall(text) {
		return text
	}

	// The template wrappers are never something to read. They are stripped
	// whether or not what is inside them parses, and whether or not the
	// closing tag has arrived yet — mid-stream it has not, and the whole point
	// is to hide the call before it is complete.
	out := strip(text, openTag, closeTag)
	out = strip(out, callsMark, "")

	// A fenced call is only hidden once it is clear that it is one, because a
	// fence is also how a coding assistant shows code it is talking about — and
	// hiding those would gut the tool. An unclosed fence whose body has already
	// started to look like a call is hidden too: waiting for the closing fence
	// would print the file first, which is the thing being fixed.
	//
	// Every fence is examined, not just the first: a reply that shows code and
	// then calls a tool has one of each.
	var kept strings.Builder
	for {
		prefix, raw, body, suffix, found := splitFence(out)
		if !found {
			kept.WriteString(prefix)
			break
		}
		kept.WriteString(prefix)
		if _, isCall := parseCalls(body, known); !isCall && !partialCall(body, known) {
			kept.WriteString(raw) // ordinary code, which is the reply's substance
		}
		out = suffix
	}
	return kept.String()
}

// mightHoldCall is the cheap test that keeps Visible off the critical path for
// ordinary prose.
func mightHoldCall(text string) bool {
	return strings.Contains(text, openTag) ||
		strings.Contains(text, callsMark) ||
		strings.Contains(text, "```")
}

// strip removes every open..close region, and an unterminated one to the end
// of the text. An empty close means the marker runs to the end.
func strip(text, open, close string) string {
	var b strings.Builder
	for {
		start := strings.Index(text, open)
		if start < 0 {
			b.WriteString(text)
			return b.String()
		}
		b.WriteString(text[:start])

		rest := text[start+len(open):]
		if close == "" {
			return b.String()
		}
		end := strings.Index(rest, close)
		if end < 0 {
			// Still arriving; everything after the marker is the call.
			return b.String()
		}
		text = rest[end+len(close):]
	}
}

// trimPartial removes a marker that has only half arrived from the end of the
// text. Two characters is the shortest it will act on: a reply ending in "<"
// or "[" is far more likely to be prose than the start of a tool call, and
// hiding a character of what the model said is its own small lie.
func trimPartial(text string, markers ...string) string {
	for _, marker := range markers {
		for n := len(marker) - 1; n >= 2; n-- {
			if strings.HasSuffix(text, marker[:n]) {
				return text[:len(text)-n]
			}
		}
	}
	return text
}

// partialCall reports that a fence which has not closed yet is already on its
// way to being a call, so it can be hidden before the file inside it is
// printed.
func partialCall(body string, known []string) bool {
	body = strings.TrimSpace(body)
	if !strings.HasPrefix(body, "{") && !strings.HasPrefix(body, "[") {
		return false
	}
	// The name is the first thing a model writes and the only part that
	// distinguishes a call from any other JSON.
	for _, name := range known {
		if strings.Contains(body, `"`+name+`"`) {
			return true
		}
	}
	return false
}
