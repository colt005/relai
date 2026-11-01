package index

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/colt005/relai/internal/redact"
	"github.com/colt005/relai/internal/session"
)

// fake adapter counting Meta calls
type fake struct {
	dir   string
	calls atomic.Int64
}

func (f *fake) Name() string  { return "fake" }
func (f *fake) Label() string { return "Fake" }
func (f *fake) Files() ([]string, error) {
	return filepath.Glob(filepath.Join(f.dir, "*.jsonl"))
}
func (f *fake) Meta(p string) (session.Session, error) {
	f.calls.Add(1)
	st, _ := os.Stat(p)
	b, _ := os.ReadFile(p)
	return session.Session{Agent: "fake", ID: filepath.Base(p), Path: p, Title: string(b), Updated: st.ModTime()}, nil
}
func (f *fake) Transcript(string) (session.Transcript, error) { return session.Transcript{}, nil }
func (f *fake) ResumeCmd(session.Session) []string            { return nil }
func (f *fake) LaunchCmd(string) []string                     { return nil }
func (f *fake) SummarizeCmd(string, string) []string          { return nil }

func TestIncrementalRefresh(t *testing.T) {
	dir := t.TempDir()
	idx := filepath.Join(t.TempDir(), "index.json")
	f := &fake{dir: dir}
	ads := map[string]session.Adapter{"fake": f}
	red, _ := redact.New("")

	write := func(name, body string, mt time.Time) {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(body), 0o644)
		os.Chtimes(p, mt, mt)
	}
	base := time.Now().Add(-time.Hour)
	write("a.jsonl", "alpha", base)
	write("b.jsonl", "beta AKIAIOSFODNN7EXAMPLE", base.Add(time.Minute))

	ss, st, err := Load(idx).Refresh(ads, red)
	if err != nil || len(ss) != 2 || st.Parsed != 2 {
		t.Fatalf("first refresh: %d sessions, parsed %d, err %v", len(ss), st.Parsed, err)
	}
	if ss[0].ID != "b.jsonl" {
		t.Error("sessions should be newest first")
	}
	if ss[0].Title != "beta [REDACTED:aws-access-key]" {
		t.Errorf("titles should be redacted before caching: %q", ss[0].Title)
	}

	// Fresh process, nothing changed: no parsing.
	f.calls.Store(0)
	_, st, _ = Load(idx).Refresh(ads, red)
	if f.calls.Load() != 0 || st.Parsed != 0 {
		t.Errorf("unchanged files were re-parsed (%d calls)", f.calls.Load())
	}

	// One file changes, one is deleted.
	write("a.jsonl", "alpha v2", base.Add(2*time.Minute))
	os.Remove(filepath.Join(dir, "b.jsonl"))
	f.calls.Store(0)
	ss, _, _ = Load(idx).Refresh(ads, red)
	if f.calls.Load() != 1 || len(ss) != 1 || ss[0].Title != "alpha v2" {
		t.Errorf("after change: calls=%d sessions=%+v", f.calls.Load(), ss)
	}
}

func TestSchemaBumpInvalidates(t *testing.T) {
	idx := filepath.Join(t.TempDir(), "index.json")
	os.WriteFile(idx, []byte(`{"version":0,"entries":{"x":{"mtime":1,"size":1}}}`), 0o600)
	if ix := Load(idx); len(ix.d.Entries) != 0 {
		t.Error("old schema entries should be dropped")
	}
}
