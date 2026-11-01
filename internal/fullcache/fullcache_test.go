package fullcache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPathIsSafeAndPerSnapshot(t *testing.T) {
	p := Path("/h", "claude", "../../etc/passwd", 12)
	if filepath.Dir(p) != filepath.Join("/h", "cache", "full") || strings.Contains(filepath.Base(p), "/") {
		t.Errorf("path escapes the cache: %s", p)
	}
	if Path("/h", "claude", "a", 1) == Path("/h", "claude", "a", 2) {
		t.Error("each snapshot needs its own file")
	}
}

func TestWritePrivateAndReusable(t *testing.T) {
	home := t.TempDir()
	p := Path(home, "claude", "abc", 7)
	if Exists(p) {
		t.Fatal("should not exist yet")
	}
	if err := Write(p, []byte("masked")); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(p)
	dst, _ := os.Stat(filepath.Dir(p))
	if st.Mode().Perm() != 0o600 || dst.Mode().Perm() != 0o700 {
		t.Errorf("modes: file %v, dir %v", st.Mode().Perm(), dst.Mode().Perm())
	}
	if !Exists(p) {
		t.Error("should exist after Write")
	}
}

func TestPruneKeepsRecentAndTouchedCopies(t *testing.T) {
	home := t.TempDir()
	old, used, fresh := Path(home, "a", "old", 1), Path(home, "a", "used", 1), Path(home, "a", "fresh", 1)
	for _, p := range []string{old, used, fresh} {
		Write(p, []byte("x"))
	}
	past := time.Now().Add(-10 * 24 * time.Hour)
	os.Chtimes(old, past, past)
	os.Chtimes(used, past, past)
	Touch(used) // a handoff asked for it again
	if n := Prune(home, MaxAge); n != 1 {
		t.Errorf("pruned %d, want 1", n)
	}
	if Exists(old) || !Exists(used) || !Exists(fresh) {
		t.Error("only the untouched old copy should go")
	}
	if n, _ := Clean(home); n != 2 {
		t.Errorf("Clean removed %d, want 2", n)
	}
}
