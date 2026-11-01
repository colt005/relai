// Package cursor reads Cursor Agent CLI sessions from ~/.cursor/projects.
//
// Format notes (undocumented; cross-checked against jazzyalex/agent-sessions, MIT):
//   - projects/<encoded-cwd>/agent-transcripts/<uuid>/<uuid>.jsonl, one record per
//     line: {"role": "user"|"assistant", "message": {"content": [blocks]}}.
//     Blocks: text, tool_use / tool_call / tool-use (name, input), tool-result,
//     thinking. Lines like {"type": "turn_ended"} carry no content.
//   - User prompts are wrapped in <user_query>…</user_query>.
//   - Subagent transcripts live in <uuid>/subagents/*.jsonl and are not listed.
//   - The project directory name is the cwd with "/" replaced by "-", which is
//     ambiguous for paths containing hyphens; it is decoded by probing the disk.
//   - Not read: ~/.cursor/chats/*/*/store.db (SQLite), which needs a SQLite driver.
package cursor

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/colt005/relai/internal/jsonl"
	s "github.com/colt005/relai/internal/session"
)

type Adapter struct{ Root string }

func New() *Adapter {
	home, _ := os.UserHomeDir()
	return &Adapter{Root: filepath.Join(home, ".cursor", "projects")}
}

func (a *Adapter) Name() string  { return "cursor" }
func (a *Adapter) Label() string { return "Cursor" }

func (a *Adapter) Files() ([]string, error) {
	return filepath.Glob(filepath.Join(a.Root, "*", "agent-transcripts", "*", "*.jsonl"))
}

var queryRe = regexp.MustCompile(`(?s)<user_query>\s*(.*?)\s*</user_query>`)

func userText(t string) string {
	if m := queryRe.FindStringSubmatch(t); m != nil {
		return strings.TrimSpace(m[1])
	}
	return strings.TrimSpace(t)
}

func blocks(o map[string]any) []map[string]any {
	var out []map[string]any
	switch c := s.Obj(o, "message")["content"].(type) {
	case string:
		out = append(out, map[string]any{"type": "text", "text": c})
	case []any:
		for _, b := range c {
			if bm, ok := b.(map[string]any); ok {
				out = append(out, bm)
			}
		}
	}
	return out
}

func isToolUse(t string) bool    { return t == "tool_use" || t == "tool_call" || t == "tool-use" }
func isToolResult(t string) bool { return t == "tool_result" || t == "tool-result" }

func (a *Adapter) Meta(path string) (s.Session, error) {
	st, err := os.Stat(path)
	if err != nil {
		return s.Session{}, err
	}
	sess := s.Session{
		Agent: "cursor", ID: strings.TrimSuffix(filepath.Base(path), ".jsonl"),
		Path: path, Updated: st.ModTime(), Started: st.ModTime(), Size: st.Size(), Source: "cli",
		CWD: decodeProjectDir(filepath.Base(filepath.Dir(filepath.Dir(path))), isDir),
	}
	err = jsonl.EachFile(path, func(o map[string]any) bool {
		if s.Str(o, "role") != "user" {
			return true
		}
		for _, b := range blocks(o) {
			if s.Str(b, "type") == "text" {
				if t := userText(s.Str(b, "text")); t != "" {
					sess.Title = s.OneLine(t, 120)
					return false
				}
			}
		}
		return true
	})
	return sess, err
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// decodeProjectDir reverses Cursor's "/" -> "-" encoding. Hyphens inside real
// directory names make that ambiguous, so it walks left to right and treats a
// hyphen as a separator only when the directory so far exists.
func decodeProjectDir(name string, exists func(string) bool) string {
	segs := strings.Split(name, "-")
	if name == "" || len(segs) == 0 {
		return ""
	}
	prefix, cur := "", segs[0]
	for _, seg := range segs[1:] {
		if exists(prefix + "/" + cur) {
			prefix, cur = prefix+"/"+cur, seg
		} else {
			cur += "-" + seg
		}
	}
	return prefix + "/" + cur
}

func (a *Adapter) Transcript(path string) (s.Transcript, error) {
	var tr s.Transcript
	add := func(r s.Role, text string) {
		if strings.TrimSpace(text) != "" {
			tr.Msgs = append(tr.Msgs, s.Msg{Role: r, Text: text})
		}
	}
	err := jsonl.EachFile(path, func(o map[string]any) bool {
		role := s.Str(o, "role")
		if role != "user" && role != "assistant" {
			return true
		}
		for _, b := range blocks(o) {
			t := s.Str(b, "type")
			switch {
			case isToolUse(t):
				add(s.ToolCall, s.FmtTool(s.Str(b, "name"), b["input"]))
			case isToolResult(t):
				add(s.ToolResult, s.Flatten(b["content"]))
			case t == "thinking":
			case role == "user":
				add(s.User, userText(s.Str(b, "text")))
			default: // text, and unknown blocks that carry visible text
				add(s.Assistant, s.Str(b, "text"))
			}
		}
		return true
	})
	return tr, err
}

func (a *Adapter) ResumeCmd(sess s.Session) []string {
	return []string{"cursor-agent", "--resume", sess.ID}
}
func (a *Adapter) LaunchCmd(prompt string) []string { return []string{"cursor-agent", prompt} }
func (a *Adapter) SummarizeCmd(prompt, _ string) []string {
	return []string{"cursor-agent", "-p", prompt}
}
