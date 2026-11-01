// Package claude reads Claude Code sessions from ~/.claude/projects.
//
// Format notes (undocumented; cross-checked against jazzyalex/agent-sessions, MIT):
//   - One JSONL file per session: projects/<encoded-cwd>/<session-uuid>.jsonl
//   - Subagent transcripts live in <session-uuid>/subagents/*.jsonl (older versions:
//     agent-*.jsonl next to the session). They are not listed.
//   - Records of type "user"/"assistant" carry cwd, gitBranch, timestamp, sessionId,
//     entrypoint and message.{role,content}. isSidechain/isMeta records are not user turns.
//   - Titles: "custom-title" (customTitle, from /rename) wins, then "ai-title" (aiTitle),
//     then "agent-name" (agentName), then legacy "summary", then the first real prompt.
//     These records can be appended anywhere, including at the end of the file.
//   - Slash commands appear as <command-name>/x</command-name><command-args>…</command-args>.
//   - "api-request*" records are request logs and are ignored.
package claude

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/colt005/relai/internal/jsonl"
	s "github.com/colt005/relai/internal/session"
)

type Adapter struct{ Root string }

func New() *Adapter {
	if d := os.Getenv("CLAUDE_CONFIG_DIR"); d != "" {
		return &Adapter{Root: filepath.Join(d, "projects")}
	}
	home, _ := os.UserHomeDir()
	return &Adapter{Root: filepath.Join(home, ".claude", "projects")}
}

func (a *Adapter) Name() string  { return "claude" }
func (a *Adapter) Label() string { return "Claude Code" }

func (a *Adapter) Files() ([]string, error) {
	matches, err := filepath.Glob(filepath.Join(a.Root, "*", "*.jsonl"))
	if err != nil {
		return nil, err
	}
	out := matches[:0]
	for _, m := range matches {
		if !strings.HasPrefix(filepath.Base(m), "agent-") {
			out = append(out, m)
		}
	}
	return out, nil
}

var (
	cmdNameRe = regexp.MustCompile(`(?s)<command-name>(.*?)</command-name>`)
	cmdArgsRe = regexp.MustCompile(`(?s)<command-args>(.*?)</command-args>`)
	noise     = []string{"<local-command", "Caveat:", "<system-reminder>", "[Request interrupted"}
)

// userText returns what the person actually typed in a user record, or "".
func userText(o map[string]any) string {
	if s.Bool(o, "isMeta") || s.Bool(o, "isSidechain") || s.Bool(o, "isCompactSummary") {
		return ""
	}
	var parts []string
	switch c := s.Obj(o, "message")["content"].(type) {
	case string:
		parts = []string{c}
	case []any:
		for _, b := range c {
			if bm, ok := b.(map[string]any); ok {
				switch s.Str(bm, "type") {
				case "text":
					parts = append(parts, s.Str(bm, "text"))
				case "image":
					parts = append(parts, "[image]")
				}
			}
		}
	}
	var out []string
	for _, p := range parts {
		t := strings.TrimSpace(p)
		if m := cmdNameRe.FindStringSubmatch(t); m != nil {
			cmd := strings.TrimSpace(m[1])
			if a := cmdArgsRe.FindStringSubmatch(t); a != nil && strings.TrimSpace(a[1]) != "" {
				cmd += " " + strings.TrimSpace(a[1])
			}
			out = append(out, cmd)
			continue
		}
		skip := t == ""
		for _, n := range noise {
			if strings.HasPrefix(t, n) {
				skip = true
			}
		}
		if !skip {
			out = append(out, t)
		}
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// `-title"` matches both "custom-title" and "ai-title" in one pass.
var titleMarkers = [][]byte{[]byte(`-title"`), []byte(`"agent-name"`)}

func isSDK(entrypoint string) bool { return strings.HasPrefix(entrypoint, "sdk") }

func (a *Adapter) Meta(path string) (s.Session, error) {
	st, err := os.Stat(path)
	if err != nil {
		return s.Session{}, err
	}
	sess := s.Session{
		Agent: "claude", ID: strings.TrimSuffix(filepath.Base(path), ".jsonl"),
		Path: path, Updated: st.ModTime(), Size: st.Size(),
	}
	var custom, ai, agentName, summary, firstPrompt string

	titles := func(o map[string]any) {
		switch s.Str(o, "type") {
		case "custom-title":
			if t := s.Str(o, "customTitle"); t != "" {
				custom = t // latest rename wins
			}
		case "ai-title":
			if t := s.Str(o, "aiTitle"); t != "" && ai == "" {
				ai = t
			}
		case "agent-name":
			if t := s.Str(o, "agentName"); t != "" {
				agentName = t
			}
		}
	}
	head := func(o map[string]any) bool {
		titles(o)
		t := s.Str(o, "type")
		if strings.HasPrefix(t, "api-request") {
			return true
		}
		if t == "summary" && summary == "" {
			summary = s.Str(o, "summary")
		}
		if t == "user" || t == "assistant" {
			if sess.CWD == "" {
				sess.CWD = s.Str(o, "cwd")
			}
			if sess.Branch == "" {
				sess.Branch = s.Str(o, "gitBranch")
			}
			if sess.Source == "" {
				sess.Source = s.Str(o, "entrypoint")
			}
			if sess.Started.IsZero() {
				sess.Started = s.ParseTime(o["timestamp"])
			}
			if id := s.Str(o, "sessionId"); id != "" && sess.ID == "" {
				sess.ID = id
			}
			if t == "user" && firstPrompt == "" {
				firstPrompt = userText(o)
			}
		}
		// Stop decoding once the basics are known; titles are found by marker.
		return sess.CWD == "" || sess.Started.IsZero() || firstPrompt == ""
	}
	if err := jsonl.Scan(path, 400, head, titleMarkers, titles); err != nil {
		return sess, err
	}
	for _, t := range []string{custom, ai, agentName, summary, firstPrompt} {
		if t != "" {
			sess.Title = s.OneLine(t, 120)
			break
		}
	}
	sess.Hidden = isSDK(sess.Source)
	return sess, nil
}

func (a *Adapter) Transcript(path string) (s.Transcript, error) {
	var tr s.Transcript
	add := func(r s.Role, text string, ts time.Time) {
		if strings.TrimSpace(text) != "" {
			tr.Msgs = append(tr.Msgs, s.Msg{Role: r, Text: text, TS: ts})
		}
	}
	err := jsonl.EachFile(path, func(o map[string]any) bool {
		t := s.Str(o, "type")
		if (t != "user" && t != "assistant") || s.Bool(o, "isSidechain") {
			return true
		}
		ts := s.ParseTime(o["timestamp"])
		content := s.Obj(o, "message")["content"]
		if t == "user" {
			if s.Bool(o, "isCompactSummary") {
				add(s.Summary, s.Flatten(content), ts)
				return true
			}
			if s.Bool(o, "isMeta") {
				return true
			}
			if blocks, ok := content.([]any); ok {
				for _, b := range blocks {
					if bm, ok := b.(map[string]any); ok && s.Str(bm, "type") == "tool_result" {
						out := s.Flatten(bm["content"])
						if s.Bool(bm, "is_error") {
							out = "ERROR: " + out
						}
						add(s.ToolResult, out, ts)
					}
				}
			}
			add(s.User, userText(o), ts)
			return true
		}
		if str, ok := content.(string); ok {
			add(s.Assistant, str, ts)
			return true
		}
		blocks, _ := content.([]any)
		for _, b := range blocks {
			bm, ok := b.(map[string]any)
			if !ok {
				continue
			}
			switch s.Str(bm, "type") {
			case "text":
				add(s.Assistant, s.Str(bm, "text"), ts)
			case "tool_use":
				add(s.ToolCall, s.FmtTool(s.Str(bm, "name"), bm["input"]), ts)
			}
		}
		return true
	})
	return tr, err
}

func (a *Adapter) ResumeCmd(sess s.Session) []string { return []string{"claude", "--resume", sess.ID} }
func (a *Adapter) LaunchCmd(prompt string) []string  { return []string{"claude", prompt} }
func (a *Adapter) SummarizeCmd(prompt, _ string) []string {
	return []string{"claude", "-p", prompt}
}
