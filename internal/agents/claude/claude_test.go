package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	s "github.com/colt005/relai/internal/session"
)

func place(t *testing.T, root, fixture, name string) string {
	t.Helper()
	b, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "-tmp-myapp")
	os.MkdirAll(dir, 0o755)
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMetaAndTranscript(t *testing.T) {
	root := t.TempDir()
	a := &Adapter{Root: root}
	p := place(t, root, "../../../testdata/claude/basic.jsonl", "11111111-2222-3333-4444-555555555555.jsonl")

	m, err := a.Meta(p)
	if err != nil {
		t.Fatal(err)
	}
	if m.Title != "login limiter" {
		t.Errorf("custom-title should win, got %q", m.Title)
	}
	if m.CWD != "/tmp/myapp" || m.Branch != "main" || m.Source != "cli" || m.Hidden {
		t.Errorf("bad meta: %+v", m)
	}

	tr, err := a.Transcript(p)
	if err != nil {
		t.Fatal(err)
	}
	var users, tools []string
	var sawSummary bool
	for _, msg := range tr.Msgs {
		switch msg.Role {
		case s.User:
			users = append(users, msg.Text)
		case s.ToolCall:
			tools = append(tools, msg.Text)
		case s.Summary:
			sawSummary = true
		}
	}
	want := []string{"Add rate limiting to the /login endpoint", "/review src"}
	if strings.Join(users, "|") != strings.Join(want, "|") {
		t.Errorf("user turns = %q, want %q", users, want)
	}
	if strings.Join(tools, "|") != "Read(/tmp/myapp/src/auth.ts)|Bash(npm test)" {
		t.Errorf("tool calls = %q", tools)
	}
	if !sawSummary {
		t.Error("compaction summary missing")
	}
}

// Titles appended after a long session must be found via the tail scan.
func TestTitleInTailOfLargeFile(t *testing.T) {
	root := t.TempDir()
	a := &Adapter{Root: root}
	var b strings.Builder
	b.WriteString(`{"type":"user","cwd":"/x","timestamp":"2026-01-01T00:00:00Z","message":{"role":"user","content":"first prompt"}}` + "\n")
	filler := `{"type":"assistant","timestamp":"2026-01-01T00:00:01Z","message":{"role":"assistant","content":"` + strings.Repeat("x", 4000) + `"}}` + "\n"
	for i := 0; i < 800; i++ { // ~3 MB, well past the head window
		b.WriteString(filler)
	}
	b.WriteString(`{"type":"custom-title","customTitle":"renamed late"}` + "\n")
	p := filepath.Join(root, "p", "33333333-0000-4000-8000-000000000000.jsonl")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte(b.String()), 0o644)

	m, err := a.Meta(p)
	if err != nil {
		t.Fatal(err)
	}
	if m.Title != "renamed late" {
		t.Errorf("title = %q", m.Title)
	}
}

func TestSDKSessionsHidden(t *testing.T) {
	root := t.TempDir()
	a := &Adapter{Root: root}
	p := place(t, root, "../../../testdata/claude/sdk.jsonl", "22222222-2222-3333-4444-555555555555.jsonl")
	m, _ := a.Meta(p)
	if !m.Hidden {
		t.Error("sdk-cli session should be hidden")
	}
}

func TestFilesSkipsSubagents(t *testing.T) {
	root := t.TempDir()
	a := &Adapter{Root: root}
	place(t, root, "../../../testdata/claude/basic.jsonl", "11111111-2222-3333-4444-555555555555.jsonl")
	place(t, root, "../../../testdata/claude/basic.jsonl", "agent-123.jsonl")
	sub := filepath.Join(root, "-tmp-myapp", "11111111-2222-3333-4444-555555555555", "subagents")
	os.MkdirAll(sub, 0o755)
	os.WriteFile(filepath.Join(sub, "agent-x.jsonl"), []byte("{}\n"), 0o644)
	files, _ := a.Files()
	if len(files) != 1 {
		t.Errorf("files = %v", files)
	}
}

// Real-world fixtures from agent-sessions: must parse without errors and yield titles.
func TestThirdPartyFixtures(t *testing.T) {
	root := t.TempDir()
	a := &Adapter{Root: root}
	for i, f := range []string{"small", "large", "schema_drift"} {
		p := place(t, root, "../../../testdata/third_party/agent-sessions/claude/"+f+".jsonl",
			"aaaaaaaa-0000-4000-8000-00000000000"+string(rune('0'+i))+".jsonl")
		m, err := a.Meta(p)
		if err != nil || m.Title == "" {
			t.Errorf("%s: meta=%+v err=%v", f, m, err)
		}
		tr, err := a.Transcript(p)
		if err != nil || len(tr.Msgs) == 0 {
			t.Errorf("%s: %d msgs, err=%v", f, len(tr.Msgs), err)
		}
	}
}
