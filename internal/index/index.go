// Package index keeps a cache of session metadata so listing stays fast with
// thousands of sessions. A file is re-parsed only when its mtime or size changes,
// and changed files are parsed in parallel.
//
// The cache is a single JSON file (no SQLite) so relai stays a pure-Go static
// binary. At ~300 bytes per session, 10k sessions is a ~3 MB file, which loads in
// milliseconds.
package index

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"

	"github.com/colt005/relai/internal/redact"
	"github.com/colt005/relai/internal/session"
)

// SchemaVersion invalidates every cached entry when bumped. Bump it whenever a
// parser change would produce different metadata for the same file.
const SchemaVersion = 1

type entry struct {
	MTime   int64           `json:"mtime"`
	Size    int64           `json:"size"`
	Session session.Session `json:"session"`
	Err     string          `json:"err,omitempty"`
}

type data struct {
	Version int              `json:"version"`
	Entries map[string]entry `json:"entries"`
}

type Index struct {
	path string
	d    data
}

// Stats describes the last refresh, for `relai doctor`.
type Stats struct {
	Files    map[string]int    // per agent
	Parsed   int               // files (re)parsed this run
	Failures map[string]string // path -> error
}

func Load(path string) *Index {
	ix := &Index{path: path}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &ix.d)
	}
	if ix.d.Version != SchemaVersion || ix.d.Entries == nil {
		ix.d = data{Version: SchemaVersion, Entries: map[string]entry{}}
	}
	return ix
}

// Refresh brings the index up to date with what's on disk and returns all sessions,
// newest first. Titles are redacted before being cached.
func (ix *Index) Refresh(adapters map[string]session.Adapter, red *redact.Redactor) ([]session.Session, Stats, error) {
	st := Stats{Files: map[string]int{}, Failures: map[string]string{}}
	type job struct {
		path        string
		ad          session.Adapter
		mtime, size int64
	}
	var jobs []job
	seen := map[string]bool{}
	dirty := false

	for _, ad := range adapters {
		files, err := ad.Files()
		if err != nil && len(files) == 0 {
			continue // agent not installed / no sessions dir
		}
		for _, f := range files {
			fi, err := os.Stat(f)
			if err != nil {
				continue
			}
			seen[f] = true
			st.Files[ad.Name()]++
			e, ok := ix.d.Entries[f]
			if ok && e.MTime == fi.ModTime().UnixNano() && e.Size == fi.Size() {
				if e.Err != "" {
					st.Failures[f] = e.Err
				}
				continue
			}
			jobs = append(jobs, job{f, ad, fi.ModTime().UnixNano(), fi.Size()})
		}
	}

	if len(jobs) > 0 {
		dirty = true
		st.Parsed = len(jobs)
		var mu sync.Mutex
		ch := make(chan job)
		var wg sync.WaitGroup
		for w := 0; w < runtime.NumCPU(); w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := range ch {
					s, err := j.ad.Meta(j.path)
					e := entry{MTime: j.mtime, Size: j.size, Session: s}
					if err != nil {
						e.Err = err.Error()
					}
					mu.Lock()
					e.Session.Title = red.String(e.Session.Title)
					ix.d.Entries[j.path] = e
					if e.Err != "" {
						st.Failures[j.path] = e.Err
					}
					mu.Unlock()
				}
			}()
		}
		for _, j := range jobs {
			ch <- j
		}
		close(ch)
		wg.Wait()
	}

	for p, e := range ix.d.Entries {
		if _, managed := adapters[e.Session.Agent]; (managed || e.Session.Agent == "") && !seen[p] {
			delete(ix.d.Entries, p)
			dirty = true
		}
	}

	out := make([]session.Session, 0, len(ix.d.Entries))
	for _, e := range ix.d.Entries {
		if _, ok := adapters[e.Session.Agent]; ok && e.Session.ID != "" {
			out = append(out, e.Session)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Updated.After(out[j].Updated) })

	if dirty {
		if err := ix.save(); err != nil {
			return out, st, err
		}
	}
	return out, st, nil
}

func (ix *Index) save() error {
	if err := os.MkdirAll(filepath.Dir(ix.path), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(ix.d)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(ix.path), ".index-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	tmp.Close()
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), ix.path) // atomic: concurrent relai processes never see a torn file
}
