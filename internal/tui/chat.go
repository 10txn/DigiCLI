package tui

import (
	"strings"

	"github.com/10txn/digicli/internal/types"
	"github.com/charmbracelet/lipgloss"
)

// Messages carry no speaker labels, so the role has to read from the styling
// alone: user text sits flush left in the user colour, replies are indented
// and plain, and local notices are dimmed.
const (
	userIndent   = 0
	replyIndent  = 2
	systemIndent = 2
)

func styleFor(role types.Role) (lipgloss.Style, int) {
	switch role {
	case types.RoleUser:
		return userMessageStyle, userIndent
	case types.RoleAssistant:
		return messageStyle, replyIndent
	case types.RoleTool:
		return toolMessageStyle, systemIndent
	default:
		return systemMessageStyle, systemIndent
	}
}

// renderMessage wraps one message to width and applies its indent.
func renderMessage(msg types.Message, text string, width int) string {
	style, indent := styleFor(msg.Role)

	bodyWidth := width - indent
	if bodyWidth < minBodyWidth {
		bodyWidth = minBodyWidth
	}

	body := style.Width(bodyWidth).Render(text)
	if indent == 0 {
		return body
	}

	pad := strings.Repeat(" ", indent)
	lines := strings.Split(body, "\n")
	for i := range lines {
		lines[i] = pad + lines[i]
	}
	return strings.Join(lines, "\n")
}

// streamCursor marks the end of a reply that is still arriving.
const streamCursor = "▍"

// renderMessages joins the whole history into the viewport body, with a blank
// line between entries. While streaming, the last message gets a cursor so an
// empty or paused reply still looks alive.
func renderMessages(msgs []types.Message, width int, streaming bool) string {
	if len(msgs) == 0 {
		return ""
	}
	blocks := make([]string, 0, len(msgs))
	for i, msg := range msgs {
		text := msg.Text()
		if streaming && i == len(msgs)-1 && msg.Role == types.RoleAssistant {
			text += streamCursor
		}
		// An assistant turn that only called a tool has nothing to show.
		if text == "" {
			continue
		}
		blocks = append(blocks, renderMessage(msg, text, width))
	}
	return strings.Join(blocks, "\n\n")
}

// estimateTokens approximates how much of the context window the conversation
// occupies. Roughly four characters per token is close enough for a status
// readout; a real tokenizer arrives with the provider clients.
func estimateTokens(msgs []types.Message) int {
	chars := 0
	for _, msg := range msgs {
		if msg.Local {
			continue // never sent, so it costs no context
		}
		// Content, not Text: a tool result sends the whole file even
		// though it displays one line.
		chars += len(msg.Content)
	}
	return chars / 4
}
