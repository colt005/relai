package redact

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMasks(t *testing.T) {
	r, _ := New("")
	cases := map[string]string{
		"OPENAI_API_KEY=sk-proj-abcdefghijklmnopqrstuvwxyz0123456789":          "[REDACTED:",
		"key sk-ant-api03-" + strings.Repeat("a", 90):                          "[REDACTED:anthropic-key]",
		"AKIAIOSFODNN7EXAMPLE":                                                 "[REDACTED:aws-access-key]",
		"token ghp_" + strings.Repeat("A", 36):                                 "[REDACTED:github-token]",
		"Authorization: Bearer abcdef0123456789abcdef":                         "Bearer [REDACTED:bearer-token]",
		"postgres://admin:hunter22@db.internal:5432/app":                       "postgres://admin:[REDACTED:url-credentials]@db",
		`"client_secret": "9f8e7d6c5b4a3210"`:                                  `"client_secret": "[REDACTED:secret-assignment]"`,
		"DB_PASSWORD=correcthorsebattery":                                      "DB_PASSWORD=[REDACTED:secret-assignment]",
		"-----BEGIN RSA PRIVATE KEY-----\nMIIE\n-----END RSA PRIVATE KEY-----": "[REDACTED:private-key]",
		"xoxb-123456789012-abcdefghij":                                         "[REDACTED:slack-token]",
	}
	for in, want := range cases {
		if got := r.String(in); !strings.Contains(got, want) {
			t.Errorf("%q -> %q, want it to contain %q", in, got, want)
		}
	}
}

func TestLeavesHarmlessTextAlone(t *testing.T) {
	r, _ := New("")
	for _, in := range []string{
		"PWD=/Users/me/project",
		"max_tokens: 100000",
		"API_KEY=${OPENAI_API_KEY}",
		"password: <your-password>",
		"token = process.env.GITHUB_TOKEN",
		"SECRET_KEY=changeme123",
		"see https://github.com/foo/bar",
		"the task is done",
	} {
		if got := r.String(in); got != in {
			t.Errorf("%q was changed to %q", in, got)
		}
	}
}

func TestCustomPatternsAndCounts(t *testing.T) {
	f := filepath.Join(t.TempDir(), "redact.txt")
	os.WriteFile(f, []byte("# internal ids\nACME-[0-9]{6}\n"), 0o600)
	r, err := New(f)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.String("ticket ACME-123456 and AKIAIOSFODNN7EXAMPLE"); got != "ticket [REDACTED:custom] and [REDACTED:aws-access-key]" {
		t.Errorf("got %q", got)
	}
	if r.Total() != 2 {
		t.Errorf("total = %d", r.Total())
	}
}

func TestNilRedactorIsNoop(t *testing.T) {
	var r *Redactor
	if r.String("AKIAIOSFODNN7EXAMPLE") != "AKIAIOSFODNN7EXAMPLE" || r.Total() != 0 {
		t.Error("nil redactor should pass text through")
	}
}

func TestNoDoubleCount(t *testing.T) {
	r, _ := New("")
	got := r.String("OPENAI_API_KEY=sk-proj-abcdefghijklmnopqrstuvwxyz0123456789")
	if got != "OPENAI_API_KEY=[REDACTED:openai-key]" || r.Total() != 1 {
		t.Errorf("got %q, total %d", got, r.Total())
	}
}

func TestMaskingKeepsTheLineCount(t *testing.T) {
	r, _ := New("")
	in := "before\n-----BEGIN RSA PRIVATE KEY-----\nMIIEow\nabcdef\n-----END RSA PRIVATE KEY-----\nafter\n"
	out := r.String(in)
	if strings.Count(out, "\n") != strings.Count(in, "\n") {
		t.Errorf("line count changed: %d -> %d\n%q", strings.Count(in, "\n"), strings.Count(out, "\n"), out)
	}
	if strings.Contains(out, "MIIEow") || !strings.Contains(out, "before") || !strings.Contains(out, "after") {
		t.Errorf("unexpected output %q", out)
	}
}
