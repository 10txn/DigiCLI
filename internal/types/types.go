// Package types holds the small value types shared across DigiCLI's packages:
// chat messages and the interaction mode. Keeping them here avoids import
// cycles between config, tui, llm and agent.
package types

import (
	"encoding/json"
	"fmt"
	"time"
)

// Role identifies who produced a Message.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleSystem    Role = "system"
	// RoleTool carries a tool's output back to the model.
	RoleTool Role = "tool"
)

// Message is a single entry in the chat history.
type Message struct {
	Role    Role      `json:"role"`
	Content string    `json:"content"`
	Time    time.Time `json:"time"`
	// Local marks a message that belongs to the session rather than the
	// conversation: a typed slash command, its output, an error notice.
	// These are displayed but never sent to the model, which would
	// otherwise try to interpret them as things the user said.
	Local bool `json:"local,omitempty"`

	// ToolCalls are the tools an assistant message asked to run.
	ToolCalls []ToolCall `json:"toolCalls,omitempty"`
	// ToolName is the tool a RoleTool message carries the output of.
	ToolName string `json:"toolName,omitempty"`
	// ToolCallID correlates a RoleTool message with the call it answers.
	ToolCallID string `json:"toolCallId,omitempty"`

	// Display, when set, is shown in place of Content. A tool result sends
	// a whole file to the model but shows the user one summary line.
	Display string `json:"-"`
}

// Text is what should be rendered for this message.
func (m Message) Text() string {
	if m.Display != "" {
		return m.Display
	}
	return m.Content
}

// NewMessage builds a Message stamped with the current time.
func NewMessage(role Role, content string) Message {
	return Message{Role: role, Content: content, Time: time.Now()}
}

// NewLocal builds a display-only Message that is never sent to the model.
func NewLocal(role Role, content string) Message {
	return Message{Role: role, Content: content, Time: time.Now(), Local: true}
}

// Mode controls how much the agent is allowed to do without asking. The
// filtering itself lands in internal/agent; for now the mode is display and
// persistence only.
type Mode int

const (
	// ModePlan is read-only: analyse code, never modify it.
	ModePlan Mode = iota
	// ModeManual asks for approval before each write or command.
	ModeManual
	// ModeAuto runs file writes and commands without prompting.
	ModeAuto
)

// modeNames is indexed by Mode and is the source of truth for both String and
// ParseMode, so adding a mode only means adding it here and to the const block.
var modeNames = [...]string{"plan", "manual", "auto"}

func (m Mode) String() string {
	if m < 0 || int(m) >= len(modeNames) {
		return "unknown"
	}
	return modeNames[m]
}

// Next cycles plan -> manual -> auto -> plan, for the Tab key.
func (m Mode) Next() Mode {
	return (m + 1) % Mode(len(modeNames))
}

// Valid reports whether m is a known mode.
func (m Mode) Valid() bool {
	return m >= 0 && int(m) < len(modeNames)
}

// ParseMode converts a config string such as "manual" into a Mode.
func ParseMode(s string) (Mode, error) {
	for i, name := range modeNames {
		if name == s {
			return Mode(i), nil
		}
	}
	return ModeManual, fmt.Errorf("unknown mode %q (want plan, manual or auto)", s)
}

// MarshalJSON stores the mode as a readable string so ~/.digicli/config.json
// stays hand-editable.
func (m Mode) MarshalJSON() ([]byte, error) {
	if !m.Valid() {
		return nil, fmt.Errorf("cannot marshal invalid mode %d", int(m))
	}
	return json.Marshal(m.String())
}

func (m *Mode) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	parsed, err := ParseMode(s)
	if err != nil {
		return err
	}
	*m = parsed
	return nil
}
