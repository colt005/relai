package cursor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	s "github.com/colt005/relai/internal/session"
)

const fixtures = "../../../testdata/third_party/agent-sessions/cursor/"

const id = "11111111-2222-3333-4444-555555555555"

func place(t *testing.T, root, fixture string) string {
	t.Helper()
	b, err := os.ReadFile(fixtures + fixture)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "Users-me-my-app", "agent-transcripts", id)
	os.MkdirAll(filepath.Join(dir, "subagents"), 0o755)
	os.WriteFile(filepath.Join(dir, "subagents", "child.jsonl"), b, 0o644)
	p := filepath.Join(dir, id+".jsonl")
	os.WriteFile(p, b, 0o644)
	return p
}

func TestSession(t *testing.T) {
	root := t.TempDir()
	a := &Adapter{Root: root}
	p := place(t, root, "test-session.jsonl")
	files, _ := a.Files()
	if len(files) != 1 || files[0] != p {
		t.Fatalf("subagent transcripts must not be listed: %v", files)
	}
	m, err := a.Meta(p)
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != id || m.Title != "run ls command" || m.Agent != "cursor" {
		t.Errorf("meta: %+v", m)
	}
	tr, _ := a.Transcript(p)
	if tr.Msgs[0].Role != s.User || tr.Msgs[0].Text != "run ls command" {
		t.Errorf("first message: %+v", tr.Msgs[0])
	}
	var calls int
	for _, x := range tr.Msgs {
		if x.Role == s.ToolCall {
			calls++
			if !strings.HasPrefix(x.Text, "Shell(") {
				t.Errorf("tool call: %q", x.Text)
			}
		}
	}
	if calls != 2 {
		t.Errorf("want 2 tool calls, got %d", calls)
	}
}

func TestSchemaDrift(t *testing.T) {
	root := t.TempDir()
	a := &Adapter{Root: root}
	p := place(t, root, "schema_drift.jsonl")
	tr, _ := a.Transcript(p)
	var got []string
	for _, x := range tr.Msgs {
		got = append(got, string(x.Role))
	}
	want := "user tool_call assistant tool_result assistant"
	if strings.Join(got, " ") != want {
		t.Errorf("roles %q, want %q", strings.Join(got, " "), want)
	}
}

func TestDecodeProjectDir(t *testing.T) {
	dirs := map[string]bool{"/Users": true, "/Users/me": true, "/Users/me/my-app": true}
	exists := func(p string) bool { return dirs[p] }
	if got := decodeProjectDir("Users-me-my-app", exists); got != "/Users/me/my-app" {
		t.Errorf("got %q", got)
	}
	if got := decodeProjectDir("Users-me-gone-dir", exists); got != "/Users/me/gone-dir" {
		t.Errorf("got %q", got)
	}
}
