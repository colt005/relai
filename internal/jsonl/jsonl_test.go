package jsonl

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCountAndLimitAgree(t *testing.T) {
	p := write(t, "{\"a\":1}\n{broken\n{\"a\":2}\n{\"a\":3}\n")
	n, err := CountFile(p)
	if err != nil || n != 4 {
		t.Fatalf("count %d, err %v", n, err)
	}
	var seen []int
	EachFileN(p, 2, func(o map[string]any) bool {
		v, _ := o["a"].(interface{ Int64() (int64, error) }).Int64()
		seen = append(seen, int(v))
		return true
	})
	// A line that looks like a record but doesn't decode still counts as one, so the
	// first two records are {"a":1} and {broken; only the first is delivered.
	if len(seen) != 1 || seen[0] != 1 {
		t.Errorf("limit 2 delivered %v", seen)
	}
}

func TestUnfinishedLastLineIsNotCountedUntilComplete(t *testing.T) {
	p := write(t, "{\"a\":1}\n{\"a\":2")
	if n, _ := CountFile(p); n != 1 {
		t.Errorf("torn last line counted: %d", n)
	}
	os.WriteFile(p, []byte("{\"a\":1}\n{\"a\":2}"), 0o644) // complete, only the newline missing
	if n, _ := CountFile(p); n != 2 {
		t.Errorf("complete unterminated line not counted: %d", n)
	}
}
