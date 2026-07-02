package cli

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"reasonix/internal/i18n"
)

// answerTailLines caps how many trailing raw answer lines the inline activity
// area shows while the current markdown block is still streaming.
const answerTailLines = 6

// streamAnswerInline commits the streamed answer's completed markdown blocks
// (flushableMarkdownPrefix) straight into the terminal scrollback as they
// close. Scrollback lines are immutable, so unlike the alt-screen path nothing
// is rewritten in place: each closed block is rendered independently and
// appended exactly once; the still-open tail stays visible in the pinned
// activity area until it closes.
func (m *chatTUI) streamAnswerInline() {
	prefix := flushableMarkdownPrefix(m.pending.String())
	if len(prefix) <= m.answerFlushed {
		return
	}
	seg := m.pending.String()[m.answerFlushed:len(prefix)]
	m.answerFlushed = len(prefix)
	m.commitAnswerSegment(seg)
}

// commitAnswerSegment renders one closed markdown segment and appends it to
// the scrollback queue. Follow-up segments of the same answer are rendered
// independently, so re-create the single blank line a one-shot render would
// have put between them (spacer + leading-newline trim).
func (m *chatTUI) commitAnswerSegment(seg string) {
	if strings.TrimSpace(seg) == "" {
		return
	}
	if m.answerSegmented {
		seg = strings.Trim(seg, "\n")
	}
	rendered := m.renderer.Render(seg)
	if rendered == "" {
		rendered = seg
	}
	block := strings.TrimRight(rendered, "\n")
	if m.answerSegmented {
		m.commitSpacer()
		block = strings.TrimLeft(block, "\n")
	}
	m.commitLine(block)
	m.answerSegmented = true
}

// renderInlineActivity is the inline renderer's live region: the pinned rows
// that show the in-flight state the alt-screen path rewrites in place — the
// streaming thought tail, a running tool's spinner/output tail, and the
// still-open markdown block of the streaming answer. Committed history above
// it lives in the terminal's own scrollback and is never repainted.
func (m chatTUI) renderInlineActivity() string {
	if !m.nativeScrollback {
		return ""
	}
	var blocks []string
	if m.reasoningNative {
		lines := []string{dim("  ▎ " + i18n.M.ChatThinking)}
		if strings.TrimSpace(string(m.reasoningView)) != "" {
			lines = append(lines, reasoningBlock(string(m.reasoningView), m.width, reasoningTailLines))
		}
		blocks = append(blocks, strings.Join(lines, "\n"))
	}
	if m.toolStreamID != "" {
		blocks = append(blocks, m.renderInlineToolTail())
	}
	if tail := m.inlineAnswerTail(); tail != "" {
		blocks = append(blocks, tail)
	}
	return strings.Join(blocks, "\n")
}

// renderInlineToolTail mirrors the fullscreen live tool block: a braille
// "working · Ns" line until the first output arrives, then the last
// toolStreamTailLines output lines under the ⎿ connector.
func (m chatTUI) renderInlineToolTail() string {
	vis := m.toolTail
	if m.toolPartial != "" {
		vis = append(append([]string{}, m.toolTail...), m.toolPartial)
	}
	if len(vis) == 0 {
		frame := toolWorkingFrames[m.toolStreamFrame%len(toolWorkingFrames)]
		secs := int(time.Since(m.toolStreamStart).Seconds())
		return connectorBlock([]string{dim(fmt.Sprintf(i18n.M.ChatToolWorkingFmt, frame, secs))})
	}
	lines := make([]string, len(vis))
	for i, ln := range vis {
		lines[i] = dim(clampPlain(ln, m.width-len([]rune(connector))))
	}
	return connectorBlock(lines)
}

// inlineAnswerTail shows the not-yet-committed remainder of the streaming
// answer (raw markdown source, like the fullscreen path's buffered tail): the
// last answerTailLines wrapped lines beyond the flushed prefix.
func (m chatTUI) inlineAnswerTail() string {
	if m.pending == nil {
		return ""
	}
	raw := m.pending.String()
	if len(raw) <= m.answerFlushed {
		return ""
	}
	tail := strings.Trim(raw[m.answerFlushed:], "\n")
	if strings.TrimSpace(tail) == "" {
		return ""
	}
	w := max(m.width-2, 8)
	var lines []string
	for _, ln := range strings.Split(tail, "\n") {
		lines = append(lines, strings.Split(ansi.Wrap(expandTabs(ln), w, ""), "\n")...)
	}
	if len(lines) > answerTailLines {
		lines = lines[len(lines)-answerTailLines:]
	}
	for i, ln := range lines {
		lines[i] = "  " + ln
	}
	return strings.Join(lines, "\n")
}
