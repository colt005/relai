// Package fullcache holds the complete, redacted transcripts that `relai full`
// creates on demand. A handoff pins a snapshot of its session, and each snapshot
// gets one file, so a copy never goes stale: it is reused until it ages out.
package fullcache

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

// MaxAge is how long an unused copy is kept.
const MaxAge = 7 * 24 * time.Hour

// Dir is where copies live under the relai home.
func Dir(home string) string { return filepath.Join(home, "cache", "full") }

var unsafe = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// Path is the file for one snapshot of one session.
func Path(home, agent, id string, snapshot int) string {
	name := fmt.Sprintf("%s-%s-%d.md", unsafe.ReplaceAllString(agent, "_"), unsafe.ReplaceAllString(id, "_"), snapshot)
	return filepath.Join(Dir(home), name)
}

// Exists reports whether a copy is already cached.
func Exists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && !st.IsDir()
}

// Write stores data atomically, readable only by the user.
func Write(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // no-op once renamed
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// Touch marks a copy as used now, so it isn't pruned while handoffs still use it.
func Touch(path string) {
	now := time.Now()
	_ = os.Chtimes(path, now, now)
}

// Prune removes copies unused for longer than maxAge and returns how many.
func Prune(home string, maxAge time.Duration) int {
	entries, _ := os.ReadDir(Dir(home))
	removed := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err == nil && time.Since(info.ModTime()) > maxAge {
			if os.Remove(filepath.Join(Dir(home), e.Name())) == nil {
				removed++
			}
		}
	}
	return removed
}

// Clean removes every cached copy and returns how many.
func Clean(home string) (int, error) {
	entries, err := os.ReadDir(Dir(home))
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, e := range entries {
		if os.Remove(filepath.Join(Dir(home), e.Name())) == nil {
			removed++
		}
	}
	return removed, nil
}
