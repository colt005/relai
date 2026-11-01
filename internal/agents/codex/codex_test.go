package codex

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	s "github.com/colt005/relai/internal/session"
	"github.com/klauspost/compress/zstd"
)

func place(t *testing.T, root, fixture, name string) string {
	t.Helper()
	b, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "2026", "10", "30")
	os.MkdirAll(dir, 0o755)
	p := filepath.Join(dir, name)
	if strings.HasSuffix(name, ".zst") && !strings.HasSuffix(fixture, ".zst") {
		var buf bytes.Buffer
		w, _ := zstd.NewWriter(&buf)
		w.Write(b)
		w.Close()
		b = buf.Bytes()
	}
	os.WriteFile(p, b, 0o644)
	return p
}

const modernName = "rollout-2026-10-30T12-00-00-aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee.jsonl"

func TestModern(t *testing.T) {
	root := t.TempDir()
	a := &Adapter{Root: root}
	p := place(t, root, "../../../testdata/codex/modern.jsonl", modernName)
	m, err := a.Meta(p)
	if err != nil {
		t.Fatal(err)
	}
	if m.ID != "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee" || m.CWD != "/tmp/myapp" {
		t.Errorf("first session_meta should win: %+v", m)
	}
	if m.Title != "flaky ws test" || m.Branch != "feat/ws" || m.Hidden {
		t.Errorf("bad meta: %+v", m)
	}
	tr, _ := a.Transcript(p)
	var got []string
	for _, msg := range tr.Msgs {
		got = append(got, string(msg.Role)+":"+msg.Text)
	}
	want := []string{
		"user:Fix the flaky websocket test",
		"tool_call:shell(npm test -- ws.spec.ts)",
		"tool_result:FAIL ws.spec.ts timeout after 5000ms",
		"tool_call:apply_patch(*** Begin Patch *** Update File: ws.spec.ts *** End Patch)",
		"assistant:The test races server startup; I'll await the listen callback.",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("transcript:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if tr.Goal != "Make the websocket suite reliable" {
		t.Errorf("goal = %q", tr.Goal)
	}
}

func TestLegacy(t *testing.T) {
	root := t.TempDir()
	a := &Adapter{Root: root}
	p := place(t, root, "../../../testdata/codex/legacy.jsonl", "rollout-2025-08-01T09-00-00-0199aaaa-0000-7000-8000-000000000000.jsonl")
	m, _ := a.Meta(p)
	if m.ID != "0199aaaa-0000-7000-8000-000000000000" || m.CWD != "/Users/me/old" || m.Title != "Rename the config module" {
		t.Errorf("legacy meta: %+v", m)
	}
	tr, _ := a.Transcript(p)
	if len(tr.Msgs) != 3 || tr.Msgs[1].Text != "shell(git mv config.py settings.py)" {
		t.Errorf("legacy transcript: %+v", tr.Msgs)
	}
}

func TestHiddenSources(t *testing.T) {
	root := t.TempDir()
	a := &Adapter{Root: root}
	for _, f := range []string{"exec", "subagent"} {
		p := place(t, root, "../../../testdata/codex/"+f+".jsonl", "rollout-2026-10-30T13-00-00-"+f+".jsonl")
		if m, _ := a.Meta(p); !m.Hidden {
			t.Errorf("%s session should be hidden: %+v", f, m)
		}
	}
}

func TestZstAndDedupe(t *testing.T) {
	root := t.TempDir()
	a := &Adapter{Root: root}
	place(t, root, "../../../testdata/codex/modern.jsonl", modernName)
	zp := place(t, root, "../../../testdata/codex/modern.jsonl", modernName+".zst")
	files, _ := a.Files()
	if len(files) != 1 || strings.HasSuffix(files[0], ".zst") {
		t.Errorf("plain file should shadow its .zst sibling: %v", files)
	}
	m, err := a.Meta(zp)
	if err != nil || m.Title != "flaky ws test" {
		t.Errorf("zst meta: %+v %v", m, err)
	}
	tr, _ := a.Transcript(zp)
	if len(tr.Msgs) != 5 {
		t.Errorf("zst transcript has %d msgs", len(tr.Msgs))
	}
}

func TestInjectedDetection(t *testing.T) {
	for in, want := range map[string]bool{
		"<environment_context>\n<cwd>/x</cwd>\n</environment_context>": true,
		"<brand_new_tag>\nstuff\n</brand_new_tag>":                     true,
		"<b>bold</b> is html":                                          false,
		"please fix <div> rendering":                                   false,
	} {
		if got := isInjected(in); got != want {
			t.Errorf("isInjected(%q) = %v", in, got)
		}
	}
}

func TestThirdPartyFixtures(t *testing.T) {
	root := t.TempDir()
	a := &Adapter{Root: root}
	for _, f := range []string{"small.jsonl", "large.jsonl", "legacy_050.jsonl", "small.jsonl.zst"} {
		p := place(t, root, "../../../testdata/third_party/agent-sessions/codex/"+f, "rollout-x-"+f)
		m, err := a.Meta(p)
		if err != nil || m.ID == "" {
			t.Errorf("%s: meta=%+v err=%v", f, m, err)
		}
		if _, err := a.Transcript(p); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
}

var _ = s.User
