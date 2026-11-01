package gemini

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	s "github.com/colt005/relai/internal/session"
)

const fixtures = "../../../testdata/third_party/agent-sessions/gemini/"

func place(t *testing.T, root, fixture, name string) string {
	t.Helper()
	b, err := os.ReadFile(fixtures + fixture)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "proj", "chats")
	os.MkdirAll(dir, 0o755)
	p := filepath.Join(dir, name)
	os.WriteFile(p, b, 0o644)
	return p
}

func TestLegacyJSON(t *testing.T) {
	root := t.TempDir()
	a := &Adapter{Root: root}
	p := place(t, root, "small.json", "session-2026-01-16T18-34-3739ef95.json")
	files, _ := a.Files()
	if len(files) != 1 || files[0] != p {
		t.Fatalf("files: %v", files)
	}
	m, err := a.Meta(p)
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "session-2026-01-16T18-34-3739ef95" || m.Title != "Hello" || m.Hidden || m.Started.IsZero() {
		t.Errorf("meta: %+v", m)
	}
	tr, _ := a.Transcript(p)
	if len(tr.Msgs) != 2 || tr.Msgs[0].Role != s.User || tr.Msgs[1].Text != "Hi!" {
		t.Errorf("transcript: %+v", tr.Msgs)
	}
}

func TestJSONLWithToolCalls(t *testing.T) {
	root := t.TempDir()
	a := &Adapter{Root: root}
	p := place(t, root, "jsonl_v040.jsonl", "session-2026-04-29T21-39-gemini.jsonl")
	m, err := a.Meta(p)
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "gemini_stage0_jsonl" || m.Title != "Say hello and list files." {
		t.Errorf("meta: %+v", m)
	}
	if !m.Updated.Equal(s.ParseTime("2026-04-29T21:39:22.319Z")) && m.Updated.IsZero() {
		t.Errorf("updated: %v", m.Updated)
	}
	tr, _ := a.Transcript(p)
	var roles []s.Role
	for _, x := range tr.Msgs {
		roles = append(roles, x.Role)
	}
	want := []s.Role{s.User, s.ToolCall, s.ToolResult, s.Assistant}
	if len(roles) != len(want) {
		t.Fatalf("roles %v", roles)
	}
	for i := range want {
		if roles[i] != want[i] {
			t.Fatalf("roles %v, want %v", roles, want)
		}
	}
	if !strings.Contains(tr.Msgs[1].Text, "ls -F") || !strings.Contains(tr.Msgs[2].Text, "README.md") {
		t.Errorf("tool rendering: %q / %q", tr.Msgs[1].Text, tr.Msgs[2].Text)
	}
}

func TestSchemaDrift(t *testing.T) {
	root := t.TempDir()
	a := &Adapter{Root: root}
	p := place(t, root, "schema_drift.json", "session-drift.json")
	m, err := a.Meta(p)
	if err != nil || m.Title != "Redacted session summary" {
		t.Fatalf("meta: %+v err %v", m, err)
	}
	tr, _ := a.Transcript(p)
	if len(tr.Msgs) != 2 || tr.Msgs[1].Role != s.Assistant {
		t.Errorf("transcript: %+v", tr.Msgs)
	}
}

func TestRewrittenMessageReplaces(t *testing.T) {
	root := t.TempDir()
	a := &Adapter{Root: root}
	dir := filepath.Join(root, "proj", "chats")
	os.MkdirAll(dir, 0o755)
	p := filepath.Join(dir, "session-x.jsonl")
	os.WriteFile(p, []byte(`{"sessionId":"x","kind":"main"}
{"id":"1","type":"user","content":"first"}
{"id":"1","type":"user","content":"edited"}
`), 0o644)
	tr, _ := a.Transcript(p)
	if len(tr.Msgs) != 1 || tr.Msgs[0].Text != "edited" {
		t.Errorf("transcript: %+v", tr.Msgs)
	}
}

func TestProjectRootResolvesCWD(t *testing.T) {
	root := t.TempDir()
	a := &Adapter{Root: root}
	p := place(t, root, "small.json", "session-a.json")
	os.WriteFile(filepath.Join(root, "proj", ".project_root"), []byte("/work/app\n"), 0o644)
	m, _ := a.Meta(p)
	if m.CWD != "/work/app" {
		t.Errorf("cwd %q", m.CWD)
	}
}
