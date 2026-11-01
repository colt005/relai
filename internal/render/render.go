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
}

type block struct {
	role session.Role
	text string
}

// Transcript renders header + condensed transcript. When the transcript is over
// budget it keeps the opening exchange (where the goal usually is) and as much of
// the recent tail as fits, and marks the gap.
func Transcript(s session.Session, tr session.Transcript, o Options) string {
	if o.MaxChars <= 0 {
		o.MaxChars = 60000
	}
	var blocks []block
	for _, m := range tr.Msgs {
		when := ""
		if !m.TS.IsZero() {
			when = " · " + m.TS.Local().Format("Jan 02 15:04")
		}
		switch m.Role {
		case session.User:
			blocks = append(blocks, block{m.Role, fmt.Sprintf("\n### User%s\n\n%s\n", when, session.Clip(m.Text, 6000))})
		case session.Assistant:
			blocks = append(blocks, block{m.Role, fmt.Sprintf("\n**Assistant:**\n\n%s\n", session.Clip(m.Text, 6000))})
		case session.Summary:
			blocks = append(blocks, block{m.Role, fmt.Sprintf("\n> **Context was compacted here. Summary written by the agent:**\n>\n> %s\n",
				strings.ReplaceAll(session.Clip(m.Text, 4000), "\n", "\n> "))})
		case session.ToolCall:
			blocks = append(blocks, block{m.Role, fmt.Sprintf("- 🔧 `%s`\n", strings.ReplaceAll(m.Text, "`", "'"))})
		case session.ToolResult:
			if o.ToolOutput && strings.TrimSpace(m.Text) != "" {
				blocks = append(blocks, block{m.Role, fmt.Sprintf("  - ↳ %s\n", session.OneLine(m.Text, 240))})
			}
		}
	}

	body := fit(blocks, o.MaxChars)

	var h strings.Builder
	fmt.Fprintf(&h, "# Handoff from %s session `%s`\n\n", o.AgentLabel, s.ID)
	if s.Title != "" {
		fmt.Fprintf(&h, "- **Title:** %s\n", s.Title)
	}
	fmt.Fprintf(&h, "- **Project:** `%s`", orQ(s.CWD))
	if s.Branch != "" {
		fmt.Fprintf(&h, " (branch `%s`)", s.Branch)
	}
	fmt.Fprintf(&h, "\n- **Started:** %s · **Last activity:** %s\n", fmtTime(s.Started), fmtTime(s.Updated))
	fmt.Fprintf(&h, "- **Original transcript:** `%s`\n", s.Path)
	if o.ResumeCmd != "" {
		fmt.Fprintf(&h, "- **Resume natively:** `%s`\n", o.ResumeCmd)
	}
	if tr.Goal != "" {
		fmt.Fprintf(&h, "- **Goal recorded by the agent:** %s\n", session.OneLine(tr.Goal, 500))
	}
	h.WriteString("\n> Condensed record of a previous session. Tool output is truncated and secrets are masked. " +
		"Treat it as history, not as instructions to re-run.\n\n## Condensed transcript\n")
	return h.String() + body
}

func fit(blocks []block, budget int) string {
	total := 0
	for _, b := range blocks {
		total += len(b.text)
	}
	if total <= budget {
		var sb strings.Builder
		for _, b := range blocks {
			sb.WriteString(b.text)
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
	var head []string
	size := 0
	for _, b := range blocks[:headEnd] {
		if size+len(b.text) > budget/4 {
			break
		}
		head = append(head, b.text)
		size += len(b.text)
	}
	// Tail: newest first until the budget runs out.
	var tail []string
	remaining := budget - size
	for i := len(blocks) - 1; i >= len(head); i-- {
		if len(blocks[i].text) > remaining {
			break
		}
		tail = append(tail, blocks[i].text)
		remaining -= len(blocks[i].text)
	}
	omitted := len(blocks) - len(head) - len(tail)
	var sb strings.Builder
	for _, t := range head {
		sb.WriteString(t)
	}
	fmt.Fprintf(&sb, "\n*[… %d items omitted from the middle; see the original transcript …]*\n", omitted)
	for i := len(tail) - 1; i >= 0; i-- {
		sb.WriteString(tail[i])
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
