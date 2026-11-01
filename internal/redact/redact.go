// Package redact masks secrets before transcripts are written to handoff files or
// shown on screen. Session transcripts routinely contain API keys and tokens that
// passed through tool output (cat .env, printenv, curl -H …), and a handoff copies
// them to a new file and into a different vendor's model.
//
// Patterns follow the shape of common gitleaks rules (github.com/gitleaks/gitleaks,
// MIT). This is a safety net, not a guarantee: it errs toward catching well-known
// token formats and obvious KEY=value assignments.
package redact

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
)

type rule struct {
	name string
	re   *regexp.Regexp
	// keep is the number of leading submatches preserved verbatim (e.g. "API_KEY=").
	keep int
}

var builtin = []rule{
	{"private-key", regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----[\s\S]*?-----END [A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----`), 0},
	{"anthropic-key", regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_\-]{20,}`), 0},
	{"openai-key", regexp.MustCompile(`\bsk-(?:proj-|svcacct-|admin-)?[A-Za-z0-9_\-]{20,}`), 0},
	{"aws-access-key", regexp.MustCompile(`\b(?:A3T[A-Z0-9]|AKIA|ASIA|ABIA|ACCA)[A-Z2-7]{16}\b`), 0},
	{"github-token", regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{60,})`), 0},
	{"gitlab-token", regexp.MustCompile(`\bglpat-[A-Za-z0-9_\-]{20,}`), 0},
	{"slack-token", regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9\-]{10,}`), 0},
	{"slack-webhook", regexp.MustCompile(`https://hooks\.slack\.com/(?:services|workflows)/[A-Za-z0-9+/_\-]{20,}`), 0},
	{"google-api-key", regexp.MustCompile(`\bAIza[0-9A-Za-z_\-]{35}`), 0},
	{"stripe-key", regexp.MustCompile(`\b(?:sk|rk)_(?:live|test)_[0-9A-Za-z]{20,}`), 0},
	{"npm-token", regexp.MustCompile(`\bnpm_[A-Za-z0-9]{36}\b`), 0},
	{"huggingface-token", regexp.MustCompile(`\bhf_[A-Za-z0-9]{30,}`), 0},
	{"jwt", regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{10,}\.eyJ[A-Za-z0-9_\-]{10,}\.[A-Za-z0-9_\-]{10,}`), 0},
	{"bearer-token", regexp.MustCompile(`(?i)(\bbearer\s+)[A-Za-z0-9._~+/\-]{16,}=*`), 1},
	{"url-credentials", regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.\-]*://[^/\s:@]+:)[^@\s/]{3,}(@)`), 2},
	// KEY=value / "key": "value" where the key name looks secret.
	{"secret-assignment", regexp.MustCompile(
		`(?i)(\b[A-Z0-9_.\-]*(?:SECRET|TOKEN|PASSWORD|PASSWD|API[_\-]?KEY|ACCESS[_\-]?KEY|PRIVATE[_\-]?KEY|CLIENT[_\-]?SECRET|CREDENTIALS?)[A-Z0-9_.\-]*["']?\s*[:=]\s*["']?)([^\s"'\x60,;}{]{8,})`), 1},
}

// Values that look like placeholders or references rather than real secrets.
var placeholder = regexp.MustCompile(`(?i)^(?:\[REDACTED|\$|<|\{|%|process\.env|os\.environ|env\(|getenv|secrets\.|x{4,}|\*{3,}|your[_\-]|example|changeme|redacted|placeholder|null|none|true|false|\d+$)`)

type Redactor struct {
	rules []rule
	Count map[string]int
}

// New returns a redactor with the built-in rules plus any extra patterns found in
// extraFile (one Go regexp per line, # comments allowed). A missing file is fine.
func New(extraFile string) (*Redactor, error) {
	r := &Redactor{rules: append([]rule{}, builtin...), Count: map[string]int{}}
	if extraFile == "" {
		return r, nil
	}
	f, err := os.Open(extraFile)
	if os.IsNotExist(err) {
		return r, nil
	} else if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for i := 1; sc.Scan(); i++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		re, err := regexp.Compile(line)
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", extraFile, i, err)
		}
		r.rules = append(r.rules, rule{name: "custom", re: re})
	}
	return r, sc.Err()
}

// String masks every match in text.
func (r *Redactor) String(text string) string {
	if r == nil {
		return text
	}
	for _, ru := range r.rules {
		ru := ru
		text = ru.re.ReplaceAllStringFunc(text, func(m string) string {
			sub := ru.re.FindStringSubmatch(m)
			if ru.name == "secret-assignment" && len(sub) > 2 && placeholder.MatchString(sub[2]) {
				return m
			}
			r.Count[ru.name]++
			mask := fmt.Sprintf("[REDACTED:%s]", ru.name)
			switch {
			case ru.keep == 1 && len(sub) > 1:
				return sub[1] + mask
			case ru.keep == 2 && len(sub) > 2:
				return sub[1] + mask + sub[2]
			}
			return mask
		})
	}
	return text
}

// Total returns how many secrets were masked so far.
func (r *Redactor) Total() int {
	if r == nil {
		return 0
	}
	n := 0
	for _, c := range r.Count {
		n += c
	}
	return n
}
