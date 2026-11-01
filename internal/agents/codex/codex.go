// Package codex reads Codex CLI sessions ("rollouts") from ~/.codex/sessions.
//
// Format notes (cross-checked against openai/codex codex-rs/rollout and
// codex-rs/protocol, Apache-2.0, and jazzyalex/agent-sessions fixtures, MIT):
//   - sessions/YYYY/MM/DD/rollout-<timestamp>-<uuid>.jsonl, optionally compressed
//     to .jsonl.zst. Both siblings can exist briefly while compressing.
//   - Each line: {"timestamp", "type", "payload"}. Types used here: session_meta
//     (id, cwd, git.branch, source), turn_context (cwd), response_item (model I/O),
//     event_msg (UI events: user_message, agent_message, thread_name_updated,
//     thread_goal_updated, …). Unknown types are ignored.
//   - Pre-Sept-2025 rollouts: first line is a bare {"id","timestamp",…} header and
//     the remaining lines are response items without an envelope.
//   - source is "cli" | "vscode" | "exec" | "mcp" | {"subagent": …} | custom.
//   - Injected context arrives as user messages wrapped in tags such as
//     <environment_context> and <user_instructions>; newer versions move it into
//     world_state records instead.
package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/colt005/relai/internal/jsonl"
	s "github.com/colt005/relai/internal/session"
)

type Adapter struct{ Root string }

func New() *Adapter {
	base := os.Getenv("CODEX_HOME")
	if base == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".codex")
	}
	return &Adapter{Root: filepath.Join(base, "sessions")}
}

func (a *Adapter) Name() string  { return "codex" }
func (a *Adapter) Label() string { return "Codex" }

var uuidRe = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

func (a *Adapter) Files() ([]string, error) {
	// Keyed by canonical .jsonl path so a plain file and its .zst sibling count once.
	found := map[string]string{}
	err := filepath.WalkDir(a.Root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			if p == a.Root {
				return filepath.SkipAll
			}
			return nil
		}
		name := d.Name()
		if d.IsDir() || !strings.HasPrefix(name, "rollout-") {
			return nil
		}
		if !strings.HasSuffix(name, ".jsonl") && !strings.HasSuffix(name, ".jsonl.zst") {
			return nil
		}
		key := strings.TrimSuffix(p, ".zst")
		if prev, ok := found[key]; !ok || strings.HasSuffix(prev, ".zst") {
			found[key] = p // prefer the plain file: it's the one still being written
		}
		return nil
	})
	out := make([]string, 0, len(found))
	for _, p := range found {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, err
}

var injectedPrefixes = []string{
	"<environment_context>", "<user_instructions>", "# AGENTS.md", "<permissions",
	"<turn_aborted>", "<collaboration_mode>", "<skills_instructions>", "<developer_instructions>",
}

// wholeTagBlock matches a message that is entirely one <snake_case> … </snake_case>
// block, which is how Codex injects context. Catches tags this list doesn't know yet.
var wholeTagBlock = regexp.MustCompile(`(?s)^<([a-z_]+)[^>]*>.*</([a-z_]+)>$`)

func isInjected(text string) bool {
	t := strings.TrimSpace(text)
	for _, p := range injectedPrefixes {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	if m := wholeTagBlock.FindStringSubmatch(t); m != nil && m[1] == m[2] {
		return true
	}
	return false
}

// item returns the response item carried by a line, handling both envelopes.
func item(o map[string]any) map[string]any {
	if s.Str(o, "type") == "response_item" {
		return s.Obj(o, "payload")
	}
	if _, enveloped := o["payload"]; !enveloped {
		switch s.Str(o, "type") {
		case "message", "function_call", "function_call_output", "custom_tool_call",
			"custom_tool_call_output", "local_shell_call":
			return o // legacy rollout
		}
	}
	return nil
}

func contentText(it map[string]any) string {
	parts, _ := it["content"].([]any)
	var b strings.Builder
	for _, p := range parts {
		pm, ok := p.(map[string]any)
		if !ok {
			continue
		}
		switch s.Str(pm, "type") {
		case "input_text", "output_text", "text":
			b.WriteString(s.Str(pm, "text"))
			b.WriteByte('\n')
		case "input_image":
			b.WriteString("[image]\n")
		}
	}
	return strings.TrimSpace(b.String())
}

func decodeMaybeJSON(v any) any {
	if str, ok := v.(string); ok {
		var out any
		if json.Unmarshal([]byte(str), &out) == nil {
			return out
		}
	}
	return v
}

func toolOutput(v any) string {
	v = decodeMaybeJSON(v)
	if m, ok := v.(map[string]any); ok {
		for _, k := range []string{"output", "stdout", "text"} {
			if str := s.Str(m, k); str != "" {
				return str
			}
		}
		if c, ok := m["content"]; ok {
			return s.Flatten(c)
		}
	}
	return s.Flatten(v)
}

// msgs converts one line into transcript messages (response items only).
func msgs(o map[string]any) []s.Msg {
	it := item(o)
	if it == nil {
		return nil
	}
	ts := s.ParseTime(o["timestamp"])
	switch s.Str(it, "type") {
	case "message":
		role := s.Str(it, "role")
		txt := contentText(it)
		if txt == "" || (role != "user" && role != "assistant") || (role == "user" && isInjected(txt)) {
			return nil
		}
		return []s.Msg{{Role: s.Role(role), Text: txt, TS: ts}}
	case "function_call", "custom_tool_call", "tool_call":
		name := s.Str(it, "name")
		if name == "" {
			name = s.Str(s.Obj(it, "function"), "name")
		}
		args := it["arguments"]
		if args == nil {
			args = it["input"]
		}
		return []s.Msg{{Role: s.ToolCall, Text: s.FmtTool(name, decodeMaybeJSON(args)), TS: ts}}
	case "local_shell_call":
		return []s.Msg{{Role: s.ToolCall, Text: s.FmtTool("shell", s.Obj(it, "action")), TS: ts}}
	case "web_search_call":
		return []s.Msg{{Role: s.ToolCall, Text: s.FmtTool("web_search", s.Obj(it, "action")), TS: ts}}
	case "function_call_output", "custom_tool_call_output":
		return []s.Msg{{Role: s.ToolResult, Text: toolOutput(it["output"]), TS: ts}}
	case "tool_result":
		return []s.Msg{{Role: s.ToolResult, Text: toolOutput(it["result"]), TS: ts}}
	}
	return nil
}

func sourceString(v any) (string, bool) {
	switch src := v.(type) {
	case string:
		return src, src == "exec"
	case map[string]any:
		for k := range src {
			return k, true // {"subagent": …} and other structured sources
		}
	}
	return "", false
}

func (a *Adapter) Meta(path string) (s.Session, error) {
	st, err := os.Stat(path)
	if err != nil {
		return s.Session{}, err
	}
	sess := s.Session{Agent: "codex", Path: path, Updated: st.ModTime(), Size: st.Size()}
	if ids := uuidRe.FindAllString(filepath.Base(path), -1); len(ids) > 0 {
		sess.ID = ids[len(ids)-1]
	}
	var threadName, firstPrompt, eventPrompt string
	n, gotMeta := 0, false
	tail := func(o map[string]any) {
		p := s.Obj(o, "payload")
		if s.Str(o, "type") == "event_msg" && s.Str(p, "type") == "thread_name_updated" {
			if t := s.Str(p, "thread_name"); t != "" {
				threadName = t
			}
		}
	}
	head := func(o map[string]any) bool {
		n++
		p := s.Obj(o, "payload")
		switch s.Str(o, "type") {
		case "session_meta":
			// The first session_meta is this file's own; later ones come from
			// forked or replayed history.
			if gotMeta {
				break
			}
			gotMeta = true
			if id := s.Str(p, "id"); id != "" {
				sess.ID = id
			}
			if c := s.Str(p, "cwd"); c != "" {
				sess.CWD = c
			}
			sess.Started = s.ParseTime(p["timestamp"])
			sess.Branch = s.Str(s.Obj(p, "git"), "branch")
			sess.Source, sess.Hidden = sourceString(p["source"])
		case "turn_context":
			if sess.CWD == "" {
				sess.CWD = s.Str(p, "cwd")
			}
		case "event_msg":
			if s.Str(p, "type") == "user_message" && eventPrompt == "" {
				eventPrompt = s.Str(p, "message")
			}
		case "":
			if n == 1 && o["id"] != nil { // legacy header
				sess.ID = s.Str(o, "id")
				sess.Started = s.ParseTime(o["timestamp"])
			}
		}
		if firstPrompt == "" {
			for _, m := range msgs(o) {
				if m.Role == s.User {
					firstPrompt = m.Text
					break
				}
			}
		}
		if sess.CWD == "" { // legacy: cwd only appears inside <environment_context>
			if it := item(o); it != nil {
				if m := cwdTagRe.FindStringSubmatch(contentText(it)); m != nil {
					sess.CWD = strings.TrimSpace(m[1])
				}
			}
		}
		tail(o)
		return !gotMetaOrLegacy(gotMeta, sess) || sess.CWD == "" || (firstPrompt == "" && eventPrompt == "")
	}
	if err := jsonl.Scan(path, 400, head, [][]byte{[]byte(`"thread_name_updated"`)}, tail); err != nil {
		return sess, err
	}
	for _, t := range []string{threadName, firstPrompt, eventPrompt} {
		if t != "" {
			sess.Title = s.OneLine(t, 120)
			break
		}
	}
	if sess.ID == "" {
		sess.ID = strings.TrimSuffix(strings.TrimSuffix(filepath.Base(path), ".zst"), ".jsonl")
	}
	return sess, nil
}

var cwdTagRe = regexp.MustCompile(`<cwd>(.*?)</cwd>`)

func (a *Adapter) Transcript(path string) (s.Transcript, error) {
	var tr s.Transcript
	var events []s.Msg
	err := jsonl.EachFile(path, func(o map[string]any) bool {
		tr.Msgs = append(tr.Msgs, msgs(o)...)
		if s.Str(o, "type") == "event_msg" {
			p := s.Obj(o, "payload")
			ts := s.ParseTime(o["timestamp"])
			switch s.Str(p, "type") {
			case "user_message":
				events = append(events, s.Msg{Role: s.User, Text: s.Str(p, "message"), TS: ts})
			case "agent_message":
				events = append(events, s.Msg{Role: s.Assistant, Text: s.Str(p, "message"), TS: ts})
			case "thread_goal_updated":
				if g := s.Str(s.Obj(p, "goal"), "objective"); g != "" {
					tr.Goal = g
				}
			}
		}
		return true
	})
	// Some rollouts only carry the UI event stream; fall back to it.
	if len(tr.Msgs) == 0 {
		tr.Msgs = events
	}
	return tr, err
}

func (a *Adapter) ResumeCmd(sess s.Session) []string { return []string{"codex", "resume", sess.ID} }
func (a *Adapter) LaunchCmd(prompt string) []string  { return []string{"codex", prompt} }
func (a *Adapter) SummarizeCmd(prompt, outFile string) []string {
	// --ephemeral keeps the summarizer run out of the user's session list.
	return []string{"codex", "exec", "--ephemeral", "--skip-git-repo-check",
		"--output-last-message", outFile, prompt}
}

// gotMetaOrLegacy reports whether the session header has been seen.
func gotMetaOrLegacy(gotMeta bool, sess s.Session) bool { return gotMeta || !sess.Started.IsZero() }
