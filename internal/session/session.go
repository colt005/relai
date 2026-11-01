// Package session defines the agent-independent model relai works with.
package session

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Session is the metadata relai shows in lists and stores in the index.
type Session struct {
	Agent   string    `json:"agent"`
	ID      string    `json:"id"`
	Path    string    `json:"path"`
	CWD     string    `json:"cwd"`
	Title   string    `json:"title"`
	Branch  string    `json:"branch,omitempty"`
	Started time.Time `json:"started"`
	Updated time.Time `json:"updated"`
	// Source is the agent-specific origin (Claude "entrypoint", Codex "source").
	Source string `json:"source,omitempty"`
	// Hidden marks sessions that aren't interactive user sessions:
	// subagents, `claude -p` / `codex exec` runs and similar.
	Hidden bool  `json:"hidden,omitempty"`
	Size   int64 `json:"size"`
}

// Role of a transcript entry.
type Role string

const (
	User       Role = "user"
	Assistant  Role = "assistant"
	ToolCall   Role = "tool_call"
	ToolResult Role = "tool_result"
	Summary    Role = "summary" // compaction summaries and similar
)

type Msg struct {
	Role Role
	Text string
	TS   time.Time
	// Full is the unabridged form of a tool call's input, when Text clips it.
	Full string
}

type Transcript struct {
	Msgs []Msg
	// Goal is an explicit goal recorded by the agent, if any (Codex thread goals).
	Goal string
}

// Adapter is implemented once per agent. Adding an agent means writing an adapter
// and registering it in internal/agents.
type Adapter interface {
	Name() string  // short id: "claude", "codex"
	Label() string // display name
	// Files returns every session file on disk.
	Files() ([]string, error)
	// Meta reads cheap metadata from one file.
	Meta(path string) (Session, error)
	// Transcript parses a full session.
	Transcript(path string) (Transcript, error)
	ResumeCmd(s Session) []string
	// LaunchCmd starts a new interactive session with an initial prompt.
	LaunchCmd(prompt string) []string
	// SummarizeCmd runs a one-shot, non-interactive prompt with the transcript on
	// stdin. If outFile is non-empty the agent writes its answer there; otherwise
	// the answer is read from stdout.
	SummarizeCmd(prompt, outFile string) []string
}

// Snapshotter is implemented by adapters that can render a session as it was after
// its first N source records. A handoff records N, so the full transcript made later
// matches the handoff even if the session has since grown.
type Snapshotter interface {
	// Records returns how many source records the session has now.
	Records(path string) (int, error)
	// TranscriptUpTo parses only the first records records (<= 0 means all).
	TranscriptUpTo(path string, records int) (Transcript, error)
}

// ---------------------------------------------------------------- helpers

var tsRe = regexp.MustCompile(`^(\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d)(\.\d+)?(Z|[+-]\d\d:?\d\d)?`)

// ParseTime accepts RFC3339 strings with any fractional precision, or epoch seconds/ms.
func ParseTime(v any) time.Time {
	switch t := v.(type) {
	case string:
		m := tsRe.FindStringSubmatch(t)
		if m == nil {
			return time.Time{}
		}
		tz := m[3]
		if tz == "" {
			tz = "Z"
		} else if tz != "Z" && !strings.Contains(tz, ":") {
			tz = tz[:3] + ":" + tz[3:]
		}
		p, err := time.Parse(time.RFC3339Nano, m[1]+m[2]+tz)
		if err != nil {
			return time.Time{}
		}
		return p
	case float64:
		if t > 1e12 {
			return time.UnixMilli(int64(t))
		}
		return time.Unix(int64(t), 0)
	case json.Number:
		f, _ := t.Float64()
		return ParseTime(f)
	}
	return time.Time{}
}

var wsRe = regexp.MustCompile(`\s+`)

// Clip truncates s to n runes, noting how much was cut.
func Clip(s string, n int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimRight(string(r[:n]), " \n") + fmt.Sprintf(" …[+%d chars]", len(r)-n)
}

// OneLine collapses whitespace and clips.
func OneLine(s string, n int) string { return Clip(wsRe.ReplaceAllString(s, " "), n) }

// FmtTool renders a tool call compactly, preferring the most informative argument.
func FmtTool(name string, input any) string {
	if name == "" {
		name = "tool"
	}
	switch in := input.(type) {
	case map[string]any:
		for _, k := range []string{"command", "cmd"} {
			if cmd := commandString(in[k]); cmd != "" {
				return fmt.Sprintf("%s(%s)", name, OneLine(cmd, 200))
			}
		}
		for _, k := range []string{"file_path", "path", "pattern", "url", "query", "description", "prompt"} {
			if s, ok := in[k].(string); ok && s != "" {
				return fmt.Sprintf("%s(%s)", name, OneLine(s, 200))
			}
		}
		b, _ := json.Marshal(in)
		return fmt.Sprintf("%s(%s)", name, OneLine(string(b), 200))
	case string:
		return fmt.Sprintf("%s(%s)", name, OneLine(in, 200))
	}
	return name + "()"
}

// FmtToolInput returns a tool call's input as indented JSON, unclipped.
func FmtToolInput(input any) string {
	b, err := json.MarshalIndent(input, "", "  ")
	if err != nil {
		return fmt.Sprint(input)
	}
	return string(b)
}

func commandString(v any) string {
	switch c := v.(type) {
	case string:
		return c
	case []any:
		parts := make([]string, 0, len(c))
		for _, p := range c {
			parts = append(parts, fmt.Sprint(p))
		}
		// ["bash", "-lc", "<script>"] -> "<script>"
		if len(parts) >= 3 && (parts[1] == "-lc" || parts[1] == "-c") {
			return parts[len(parts)-1]
		}
		return strings.Join(parts, " ")
	}
	return ""
}

// Str returns m[k] as a string, or "".
func Str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}

// Obj returns m[k] as an object, or nil.
func Obj(m map[string]any, k string) map[string]any {
	o, _ := m[k].(map[string]any)
	return o
}

// Bool returns m[k] as a bool.
func Bool(m map[string]any, k string) bool {
	b, _ := m[k].(bool)
	return b
}

// Flatten turns string / list-of-text-blocks content into plain text.
func Flatten(v any) string {
	switch c := v.(type) {
	case string:
		return c
	case []any:
		var b strings.Builder
		for _, it := range c {
			switch x := it.(type) {
			case map[string]any:
				if t := Str(x, "text"); t != "" {
					b.WriteString(t)
					b.WriteByte('\n')
				} else if strings.Contains(Str(x, "type"), "image") {
					b.WriteString("[image]\n")
				}
			case string:
				b.WriteString(x)
				b.WriteByte('\n')
			}
		}
		return strings.TrimSpace(b.String())
	case nil:
		return ""
	}
	b, _ := json.Marshal(v)
	return string(b)
}
