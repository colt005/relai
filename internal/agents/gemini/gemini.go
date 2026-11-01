// Package gemini reads Gemini CLI sessions from ~/.gemini/tmp.
//
// Format notes (undocumented; cross-checked against the jazzyalex/agent-sessions
// fixtures, MIT, and the Gemini CLI session-management docs):
//   - tmp/<project>/chats/session-<timestamp>-<id>.json (older CLI): one JSON
//     document {sessionId, projectHash, startTime, lastUpdated, kind, summary?,
//     messages[]}, rewritten on every update.
//   - tmp/<project>/chats/session-<timestamp>-<id>.jsonl (newer CLI): a header line
//     with the same metadata, then one message per line. {"$set": {...}} lines
//     update header fields. A message id seen again replaces the earlier record.
//   - Messages: type "user" (content is a string or [{text}]), "gemini" (content,
//     toolCalls[{name, args, result[].functionResponse.response.output}]); other
//     types ("info", "error") are UI notices and are skipped.
//   - The cwd is not stored, only projectHash (sha256 of the project root). It is
//     recovered from <project>/.project_root when present, or by hashing the
//     directory relai runs in and its parents.
//   - Subagent sessions have kind "subagent" and are hidden.
package gemini

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/colt005/relai/internal/jsonl"
	s "github.com/colt005/relai/internal/session"
)

type Adapter struct{ Root string }

func New() *Adapter {
	home, _ := os.UserHomeDir()
	return &Adapter{Root: filepath.Join(home, ".gemini", "tmp")}
}

func (a *Adapter) Name() string  { return "gemini" }
func (a *Adapter) Label() string { return "Gemini CLI" }

func (a *Adapter) Files() ([]string, error) {
	var out []string
	for _, ext := range []string{"json", "jsonl"} {
		m, err := filepath.Glob(filepath.Join(a.Root, "*", "chats", "session-*."+ext))
		if err != nil {
			return nil, err
		}
		out = append(out, m...)
	}
	return out, nil
}

// file is a parsed session file in either layout.
type file struct {
	head map[string]any
	msgs []map[string]any
}

// load parses a session file; records > 0 keeps only the first records records.
func load(path string, records int) (file, error) {
	var f file
	if strings.HasSuffix(path, ".jsonl") {
		f.head = map[string]any{}
		index := map[string]int{}
		err := jsonl.EachFileN(path, records, func(o map[string]any) bool {
			if set := s.Obj(o, "$set"); set != nil {
				for k, v := range set {
					f.head[k] = v
				}
				return true
			}
			if s.Str(o, "type") == "" {
				for k, v := range o { // header line
					f.head[k] = v
				}
				return true
			}
			if id := s.Str(o, "id"); id != "" {
				if i, ok := index[id]; ok {
					f.msgs[i] = o
					return true
				}
				index[id] = len(f.msgs)
			}
			f.msgs = append(f.msgs, o)
			return true
		})
		return f, err
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return f, err
	}
	if err := json.Unmarshal(b, &f.head); err != nil {
		return f, err
	}
	for _, key := range []string{"messages", "history"} { // "history" is seen in drifted files
		list, _ := f.head[key].([]any)
		for _, m := range list {
			if mm, ok := m.(map[string]any); ok {
				f.msgs = append(f.msgs, mm)
			}
		}
	}
	if records > 0 && len(f.msgs) > records {
		f.msgs = f.msgs[:records]
	}
	return f, nil
}

// msgType normalizes the role field across layouts.
func msgType(m map[string]any) string {
	if t := s.Str(m, "type"); t != "" {
		return t
	}
	switch s.Str(m, "role") {
	case "model":
		return "gemini"
	case "":
		return ""
	default:
		return s.Str(m, "role")
	}
}

// text returns the plain text of a message's content or parts.
func text(m map[string]any) string {
	if c, ok := m["content"]; ok && c != nil {
		if t := strings.TrimSpace(s.Flatten(c)); t != "" {
			return t
		}
	}
	if parts, ok := m["parts"].([]any); ok {
		return strings.TrimSpace(s.Flatten(parts))
	}
	return ""
}

func (a *Adapter) Meta(path string) (s.Session, error) {
	st, err := os.Stat(path)
	if err != nil {
		return s.Session{}, err
	}
	f, err := load(path, 0)
	if err != nil {
		return s.Session{}, err
	}
	base := filepath.Base(path)
	sess := s.Session{
		Agent: "gemini", ID: strings.TrimSuffix(strings.TrimSuffix(base, ".jsonl"), ".json"),
		Path: path, Updated: st.ModTime(), Size: st.Size(), Source: s.Str(f.head, "kind"),
	}
	if id := s.Str(f.head, "sessionId"); id != "" {
		sess.ID = id
	}
	if t := s.ParseTime(f.head["startTime"]); !t.IsZero() {
		sess.Started = t
	} else if t := s.ParseTime(f.head["start_time"]); !t.IsZero() {
		sess.Started = t
	}
	if t := s.ParseTime(f.head["lastUpdated"]); !t.IsZero() {
		sess.Updated = t
	}
	sess.Hidden = sess.Source == "subagent"
	sess.CWD = resolveCWD(filepath.Dir(filepath.Dir(path)), s.Str(f.head, "projectHash"))

	title := s.Str(f.head, "summary")
	if title == "" {
		for _, m := range f.msgs {
			if msgType(m) == "user" {
				if t := text(m); t != "" {
					title = t
					break
				}
			}
		}
	}
	sess.Title = s.OneLine(title, 120)
	return sess, nil
}

// resolveCWD maps a project hash back to a directory.
func resolveCWD(projectDir, hash string) string {
	if b, err := os.ReadFile(filepath.Join(projectDir, ".project_root")); err == nil {
		if p := strings.TrimSpace(string(b)); p != "" {
			return p
		}
	}
	if hash == "" {
		return ""
	}
	wd, _ := os.Getwd()
	for d := wd; d != ""; d = filepath.Dir(d) {
		sum := sha256.Sum256([]byte(d))
		if hex.EncodeToString(sum[:]) == hash {
			return d
		}
		if d == filepath.Dir(d) {
			break
		}
	}
	return ""
}

func (a *Adapter) Transcript(path string) (s.Transcript, error) { return a.TranscriptUpTo(path, 0) }

// Records and TranscriptUpTo make Adapter an s.Snapshotter. A .jsonl session counts
// source lines; a legacy .json session counts messages.
func (a *Adapter) Records(path string) (int, error) {
	if strings.HasSuffix(path, ".jsonl") {
		return jsonl.CountFile(path)
	}
	f, err := load(path, 0)
	return len(f.msgs), err
}

func (a *Adapter) TranscriptUpTo(path string, records int) (s.Transcript, error) {
	var tr s.Transcript
	f, err := load(path, records)
	if err != nil {
		return tr, err
	}
	add := func(r s.Role, t string, m map[string]any) {
		if strings.TrimSpace(t) != "" {
			ts := s.ParseTime(m["timestamp"])
			if ts.IsZero() {
				ts = s.ParseTime(m["ts"])
			}
			tr.Msgs = append(tr.Msgs, s.Msg{Role: r, Text: t, TS: ts})
		}
	}
	for _, m := range f.msgs {
		switch msgType(m) {
		case "user":
			add(s.User, text(m), m)
		case "gemini":
			add(s.Assistant, text(m), m)
			calls, _ := m["toolCalls"].([]any)
			for _, c := range calls {
				cm, ok := c.(map[string]any)
				if !ok {
					continue
				}
				tr.Msgs = append(tr.Msgs, s.Msg{Role: s.ToolCall, TS: s.ParseTime(m["timestamp"]),
					Text: s.FmtTool(s.Str(cm, "name"), cm["args"]), Full: s.FmtToolInput(cm["args"])})
				if out := toolOutput(cm); out != "" {
					if s.Str(cm, "status") == "error" {
						out = "ERROR: " + out
					}
					add(s.ToolResult, out, m)
				}
			}
		}
	}
	return tr, nil
}

func toolOutput(call map[string]any) string {
	results, _ := call["result"].([]any)
	for _, r := range results {
		rm, _ := r.(map[string]any)
		resp := s.Obj(s.Obj(rm, "functionResponse"), "response")
		for _, k := range []string{"output", "error"} {
			if v := s.Str(resp, k); v != "" {
				return v
			}
		}
	}
	return s.Str(call, "resultDisplay")
}

func (a *Adapter) ResumeCmd(sess s.Session) []string {
	return []string{"gemini", "--resume", sess.ID}
}
func (a *Adapter) LaunchCmd(prompt string) []string { return []string{"gemini", "-i", prompt} }
func (a *Adapter) SummarizeCmd(prompt, _ string) []string {
	return []string{"gemini", "-p", prompt}
}
