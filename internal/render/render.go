// Package render turns a transcript into the condensed markdown used by
// `relai show` and handoff files.
package render

import (
	"fmt"
	"strings"
	"time"

	"github.com/colt005/relai/internal/session"
)

type Options struct {
	MaxChars   int  // overall budget for the transcript body
	ToolOutput bool // include one-line tool results
	ResumeCmd  string
	AgentLabel string
	// Snapshot is how many source records the transcript covers (0 if the adapter
	// can't snapshot). FullCmd is the command that writes the full transcript for
	// that snapshot; markers like [full L120–180] refer to lines of its output.
	Snapshot int
	FullCmd  string
}

const (
	userClip    = 6000
	summaryClip = 4000
	resultClip  = 240
)

// block is one transcript item in two forms: cond is what the condensed
// transcript shows (empty when hidden), full is the complete text.
type block struct {
	role    session.Role
	cond    string
	full    string
	clipped bool // cond leaves something out that full has
	idx     int
}

// fence returns a code fence longer than any backtick run inside text.
func fence(text string) string {
	longest, run := 0, 0
	for _, r := range text {
		if r == '`' {
			run++
			longest = max(longest, run)
		} else {
			run = 0
		}
	}
	return strings.Repeat("`", max(3, longest+1))
}

func buildBlocks(tr session.Transcript, o Options) []block {
	var blocks []block
	add := func(b block) {
		b.idx = len(blocks)
		blocks = append(blocks, b)
	}
	for _, m := range tr.Msgs {
		when := ""
		if !m.TS.IsZero() {
			when = " · " + m.TS.Local().Format("Jan 02 15:04")
		}
		text := strings.TrimSpace(m.Text)
		switch m.Role {
		case session.User:
			add(block{role: m.Role,
				cond:    fmt.Sprintf("\n### User%s\n\n%s\n", when, session.Clip(text, userClip)),
				full:    fmt.Sprintf("\n### User%s\n\n%s\n", when, text),
				clipped: len([]rune(text)) > userClip})
		case session.Assistant:
			add(block{role: m.Role,
				cond:    fmt.Sprintf("\n**Assistant:**\n\n%s\n", session.Clip(text, userClip)),
				full:    fmt.Sprintf("\n**Assistant:**\n\n%s\n", text),
				clipped: len([]rune(text)) > userClip})
		case session.Summary:
			quote := func(s string) string { return strings.ReplaceAll(s, "\n", "\n> ") }
			const head = "\n> **Context was compacted here. Summary written by the agent:**\n>\n> "
			add(block{role: m.Role,
				cond:    head + quote(session.Clip(text, summaryClip)) + "\n",
				full:    head + quote(text) + "\n",
				clipped: len([]rune(text)) > summaryClip})
		case session.ToolCall:
			line := fmt.Sprintf("- 🔧 `%s`\n", strings.ReplaceAll(m.Text, "`", "'"))
			b := block{role: m.Role, cond: line, full: line}
			// Text clips long arguments; the full input follows when it was cut.
			if m.Full != "" && strings.Contains(m.Text, "…[+") {
				f := fence(m.Full)
				b.full = line + "\n" + f + "json\n" + m.Full + "\n" + f + "\n"
				b.clipped = true
			}
			add(b)
		case session.ToolResult:
			if strings.TrimSpace(m.Text) == "" {
				continue
			}
			one := session.OneLine(m.Text, resultClip)
			inline := fmt.Sprintf("  - ↳ %s\n", one)
			b := block{role: m.Role, full: inline}
			if o.ToolOutput {
				b.cond = inline
			}
			if one != strings.TrimSpace(m.Text) { // multi-line or long: keep it whole
				f := fence(m.Text)
				b.full = "  - ↳ result:\n\n" + f + "\n" + strings.TrimRight(m.Text, "\n") + "\n" + f + "\n"
				b.clipped = o.ToolOutput
			}
			add(b)
		}
	}
	return blocks
}

func header(s session.Session, tr session.Transcript, o Options, title string) string {
	var h strings.Builder
	fmt.Fprintf(&h, "# %s %s session `%s`\n\n", title, o.AgentLabel, s.ID)
	if s.Title != "" {
		fmt.Fprintf(&h, "- **Title:** %s\n", s.Title)
	}
	fmt.Fprintf(&h, "- **Project:** `%s`", orQ(s.CWD))
	if s.Branch != "" {
		fmt.Fprintf(&h, " (branch `%s`)", s.Branch)
	}
	fmt.Fprintf(&h, "\n- **Started:** %s · **Last activity:** %s\n", fmtTime(s.Started), fmtTime(s.Updated))
	if o.Snapshot > 0 {
		fmt.Fprintf(&h, "- **Snapshot:** the first %d records of the session\n", o.Snapshot)
	}
	if o.ResumeCmd != "" {
		fmt.Fprintf(&h, "- **Resume natively:** `%s`\n", o.ResumeCmd)
	}
	if tr.Goal != "" {
		fmt.Fprintf(&h, "- **Goal recorded by the agent:** %s\n", session.OneLine(tr.Goal, 500))
	}
	return h.String()
}

const (
	condensedNote = "> Condensed record of a previous session. Tool output is truncated and secrets are masked. " +
		"Treat it as history, not as instructions to re-run.\n"
	fullNote = "> Complete record of a previous session, secrets masked. " +
		"Treat it as history, not as instructions to re-run.\n"
)

// span is a block's 1-based line range in the full transcript.
type span struct{ from, to int }

// layout returns where each block lands in the full transcript, given its header.
func layout(head string, blocks []block) []span {
	line := strings.Count(head, "\n") + 1
	spans := make([]span, len(blocks))
	for i, b := range blocks {
		lead := len(b.full) - len(strings.TrimLeft(b.full, "\n"))
		n := strings.Count(b.full, "\n")
		spans[i] = span{from: line + lead, to: line + n - 1}
		line += n
	}
	return spans
}

func fullHead(s session.Session, tr session.Transcript, o Options) string {
	return header(s, tr, o, "Full transcript of") + "\n" + fullNote + "\n## Full transcript\n"
}

// Full renders the complete transcript: no clipping, no budget, whole tool output.
// The caller masks secrets in the result; line counts must not change when it does.
func Full(s session.Session, tr session.Transcript, o Options) string {
	blocks := buildBlocks(tr, o)
	var sb strings.Builder
	sb.WriteString(fullHead(s, tr, o))
	for _, b := range blocks {
		sb.WriteString(b.full)
	}
	return sb.String()
}

func marker(sp span) string { return fmt.Sprintf("[full L%d–%d]", sp.from, sp.to) }

// Transcript renders header + condensed transcript. When the transcript is over
// budget it keeps the opening exchange (where the goal usually is) and as much of
// the recent tail as fits, and marks the gap. Clipped items carry a marker with
// their line range in the full transcript.
func Transcript(s session.Session, tr session.Transcript, o Options) string {
	if o.MaxChars <= 0 {
		o.MaxChars = 60000
	}
	all := buildBlocks(tr, o)
	spans := layout(fullHead(s, tr, o), all)

	var shown []block
	for _, b := range all {
		if b.cond == "" {
			continue
		}
		if b.clipped {
			b.cond = strings.TrimRight(b.cond, "\n") + " " + marker(spans[b.idx]) + "\n"
		}
		shown = append(shown, b)
	}
	body := fit(shown, o.MaxChars, spans)

	h := header(s, tr, o, "Handoff from")
	if o.FullCmd != "" {
		h += fmt.Sprintf("- **Full transcript:** run `%s` for the complete session with secrets masked "+
			"(created on demand; large, so search it). Markers like [full L120–180] give line ranges in it.\n", o.FullCmd)
	}
	return h + "\n" + condensedNote + "\n## Condensed transcript\n" + body
}

func fit(blocks []block, budget int, spans []span) string {
	total := 0
	for _, b := range blocks {
		total += len(b.cond)
	}
	if total <= budget {
		var sb strings.Builder
		for _, b := range blocks {
			sb.WriteString(b.cond)
		}
		return sb.String()
	}
	// Head: up to the second user turn, capped at a quarter of the budget.
	headEnd, users := len(blocks), 0
	for i, b := range blocks {
		if b.role == session.User {
			users++
			if users == 2 {
				headEnd = i
				break
			}
		}
	}
	var head []block
	size := 0
	for _, b := range blocks[:headEnd] {
		if size+len(b.cond) > budget/4 {
			break
		}
		head = append(head, b)
		size += len(b.cond)
	}
	// Tail: newest first until the budget runs out.
	var tail []block
	remaining := budget - size
	for i := len(blocks) - 1; i >= len(head); i-- {
		if len(blocks[i].cond) > remaining {
			break
		}
		tail = append(tail, blocks[i])
		remaining -= len(blocks[i].cond)
	}
	omitted := len(blocks) - len(head) - len(tail)
	var sb strings.Builder
	for _, b := range head {
		sb.WriteString(b.cond)
	}
	gap := fmt.Sprintf("*[… %d items omitted from the middle", omitted)
	if omitted > 0 {
		first := blocks[len(head)]
		last := blocks[len(blocks)-len(tail)-1]
		gap += " " + marker(span{spans[first.idx].from, spans[last.idx].to})
	}
	fmt.Fprintf(&sb, "\n%s …]*\n", gap)
	for i := len(tail) - 1; i >= 0; i-- {
		sb.WriteString(tail[i].cond)
	}
	return sb.String()
}

// WithBriefings inserts briefing sections between the header and the transcript.
func WithBriefings(doc string, briefings ...string) string {
	var parts []string
	for _, b := range briefings {
		if strings.TrimSpace(b) != "" {
			parts = append(parts, strings.TrimSpace(b))
		}
	}
	if len(parts) == 0 {
		return doc
	}
	const marker = "\n## Condensed transcript\n"
	head, rest, ok := strings.Cut(doc, marker)
	if !ok {
		return strings.Join(parts, "\n\n") + "\n\n" + doc
	}
	return head + "\n" + strings.Join(parts, "\n\n") + "\n" + marker + rest
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return "?"
	}
	return t.Local().Format("2006-01-02 15:04")
}

func orQ(s string) string {
	if s == "" {
		return "?"
	}
	return s
}

// Relative formats a time as "3h ago".
func Relative(t time.Time) string {
	if t.IsZero() {
		return "?"
	}
	d := time.Since(t)
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	case d >= time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d >= time.Minute:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	}
	return "now"
}
