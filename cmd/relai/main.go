// relai browses, resumes, and hands off sessions between CLI coding agents.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/colt005/relai/internal/agents"
	"github.com/colt005/relai/internal/fullcache"
	"github.com/colt005/relai/internal/index"
	"github.com/colt005/relai/internal/launch"
	"github.com/colt005/relai/internal/redact"
	"github.com/colt005/relai/internal/render"
	"github.com/colt005/relai/internal/session"
	"github.com/colt005/relai/internal/skills"
)

var version = "" // set by goreleaser; empty in other builds

// versionString reports the release version. Release builds get it from goreleaser;
// `go install …@vX.Y.Z` builds carry it in the module info; local builds report
// "dev" plus the commit when it's known.
func versionString() string {
	if version != "" {
		return version
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	if v := bi.Main.Version; v != "" && v != "(devel)" {
		return strings.TrimPrefix(v, "v")
	}
	var rev, dirty string
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "-dirty"
			}
		}
	}
	if rev == "" {
		return "dev"
	}
	return "dev+" + rev[:min(len(rev), 7)] + dirty
}

// JSONSchemaVersion is the contract for `--json` output consumed by UIs.
const JSONSchemaVersion = 1

const usage = `relai - browse, resume, and hand off sessions between coding agents

Usage:
  relai list    [-a AGENT] [--here] [-n 30] [--all] [--json]
  relai show    <ref> [--max-chars N] [--no-tool-output] [--no-redact]
  relai full    <ref> [--snapshot N] [--stdout]
  relai clean
  relai resume  <ref> [--new-window]
  relai handoff <ref> --to AGENT [--note FILE] [--summarize] [--prompt TEXT]
                [--cd DIR] [--no-launch] [--new-window] [--no-redact]
  relai launch  --to AGENT --file BRIEF.md [--cd DIR] [--new-window]
  relai pick    [-a AGENT] [--here] [--all] [--new-window]
  relai install-skills
  relai doctor
  relai version        (also --version)

<ref> is a session id or unique prefix, "agent:id", or "latest".
Commands taking <ref> also accept -a AGENT and --here to narrow it.
Agents: claude, codex, gemini, cursor.

Environment: RELAI_HOME (default ~/.relai), RELAI_TERMINAL (Terminal|iTerm|cmux),
CLAUDE_CONFIG_DIR, CODEX_HOME. Extra redaction patterns: $RELAI_HOME/redact.txt.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmds := map[string]func([]string) error{
		"list": cmdList, "ls": cmdList, "show": cmdShow, "full": cmdFull, "clean": cmdClean, "resume": cmdResume,
		"handoff": cmdHandoff, "launch": cmdLaunch, "pick": cmdPick,
		"install-skills": cmdInstallSkills, "doctor": cmdDoctor,
		"version":   func([]string) error { fmt.Println("relai", versionString()); return nil },
		"--version": func([]string) error { fmt.Println("relai", versionString()); return nil },
		"-v":        func([]string) error { fmt.Println("relai", versionString()); return nil },
		"help":      func([]string) error { fmt.Print(usage); return nil },
		"-h":        func([]string) error { fmt.Print(usage); return nil },
		"--help":    func([]string) error { fmt.Print(usage); return nil },
	}
	fn, ok := cmds[os.Args[1]]
	if !ok {
		fmt.Fprintf(os.Stderr, "relai: unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err := fn(os.Args[2:]); err != nil {
		fmt.Fprintln(os.Stderr, "relai:", err)
		os.Exit(1)
	}
}

// ---------------------------------------------------------------- shared

func relaiHome() string {
	if d := os.Getenv("RELAI_HOME"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".relai")
}

// parseInterspersed lets flags appear before or after positional args.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

type filter struct {
	agent string
	here  bool
	all   bool
}

func (f *filter) register(fs *flag.FlagSet) {
	fs.StringVar(&f.agent, "a", "", "only this agent")
	fs.StringVar(&f.agent, "agent", "", "only this agent")
	fs.BoolVar(&f.here, "here", false, "only sessions started in the current directory")
	fs.BoolVar(&f.all, "all", false, "include hidden sessions (subagents, -p/exec runs)")
}

func newRedactor(disabled bool) (*redact.Redactor, error) {
	if disabled {
		return nil, nil
	}
	return redact.New(filepath.Join(relaiHome(), "redact.txt"))
}

// loadSessions refreshes the index and returns sessions matching f, newest first.
func loadSessions(f filter) ([]session.Session, index.Stats, error) {
	ads := agents.All()
	if f.agent != "" {
		if _, ok := ads[f.agent]; !ok {
			return nil, index.Stats{}, fmt.Errorf("unknown agent %q (have: %s)", f.agent, strings.Join(agents.Names(), ", "))
		}
	}
	red, err := newRedactor(false)
	if err != nil {
		return nil, index.Stats{}, err
	}
	ix := index.Load(filepath.Join(relaiHome(), "index.json"))
	all, st, err := ix.Refresh(ads, red)
	if err != nil {
		fmt.Fprintln(os.Stderr, "relai: warning: could not save index:", err)
	}
	cwd, _ := os.Getwd()
	cwd = realpath(cwd)
	var out []session.Session
	seen := map[string]bool{}
	for _, s := range all {
		// The same session can exist twice (e.g. a plain rollout and a compressed
		// copy). all is newest-first, so the first one wins.
		if k := s.Agent + ":" + s.ID; seen[k] {
			continue
		} else {
			seen[k] = true
		}
		if f.agent != "" && s.Agent != f.agent {
			continue
		}
		if s.Hidden && !f.all {
			continue
		}
		if f.here && realpath(s.CWD) != cwd {
			continue
		}
		out = append(out, s)
	}
	return out, st, nil
}

func realpath(p string) string {
	if p == "" {
		return ""
	}
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return filepath.Clean(p)
}

func resolve(ref string, f filter) (session.Session, error) {
	if ref == "latest" || ref == "last" {
		ss, _, err := loadSessions(f)
		if err != nil {
			return session.Session{}, err
		}
		if len(ss) == 0 {
			return session.Session{}, errors.New("no sessions found")
		}
		return ss[0], nil
	}
	if a, id, ok := strings.Cut(ref, ":"); ok {
		if _, known := agents.All()[a]; known {
			f.agent, ref = a, id
		}
	}
	f.all = true // explicit ids may point at hidden sessions
	ss, _, err := loadSessions(f)
	if err != nil {
		return session.Session{}, err
	}
	var hits []session.Session
	for _, s := range ss {
		if s.ID == ref {
			return s, nil
		}
		if strings.HasPrefix(s.ID, ref) {
			hits = append(hits, s)
		}
	}
	switch len(hits) {
	case 0:
		return session.Session{}, fmt.Errorf("no session matching %q", ref)
	case 1:
		return hits[0], nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%q is ambiguous:", ref)
	for _, h := range hits[:min(len(hits), 10)] {
		fmt.Fprintf(&b, "\n  %s:%s  %s", h.Agent, h.ID, h.Title)
	}
	return session.Session{}, errors.New(b.String())
}

// loadTranscript parses a session. When the adapter can snapshot, it covers the first
// snapshot records (0 = everything so far) and returns that count; adapters that
// can't snapshot return 0.
func loadTranscript(ad session.Adapter, path string, snapshot int) (int, session.Transcript, error) {
	sn, ok := ad.(session.Snapshotter)
	if !ok {
		tr, err := ad.Transcript(path)
		return 0, tr, err
	}
	n, err := sn.Records(path)
	if err != nil {
		return 0, session.Transcript{}, err
	}
	if snapshot > 0 {
		if snapshot > n {
			return 0, session.Transcript{}, fmt.Errorf("the session now has %d records, fewer than the snapshot's %d; "+
				"it was rewritten since the handoff", n, snapshot)
		}
		n = snapshot
	}
	tr, err := sn.TranscriptUpTo(path, n)
	return n, tr, err
}

// fullCommand is what an agent runs to get the complete transcript of a snapshot.
func fullCommand(s session.Session, snapshot int) string {
	cmd := fmt.Sprintf("relai full %s:%s", s.Agent, s.ID)
	if snapshot > 0 {
		cmd += fmt.Sprintf(" --snapshot %d", snapshot)
	}
	return cmd
}

// renderSession renders the condensed transcript and returns the snapshot it covers.
func renderSession(s session.Session, maxChars int, toolOutput bool, red *redact.Redactor) (string, int, error) {
	ad := agents.All()[s.Agent]
	snap, tr, err := loadTranscript(ad, s.Path, 0)
	if err != nil {
		return "", 0, err
	}
	doc := render.Transcript(s, tr, render.Options{
		MaxChars: maxChars, ToolOutput: toolOutput, AgentLabel: ad.Label(),
		ResumeCmd: launch.Join(ad.ResumeCmd(s)),
		Snapshot:  snap, FullCmd: fullCommand(s, snap),
	})
	return red.String(doc), snap, nil
}

// ---------------------------------------------------------------- list

type jsonSession struct {
	session.Session
	ResumeCmd string `json:"resume_cmd"`
}

func cmdList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	var f filter
	f.register(fs)
	n := fs.Int("n", 30, "max sessions (0 = all)")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	ss, _, err := loadSessions(f)
	if err != nil {
		return err
	}
	if *n > 0 && len(ss) > *n {
		ss = ss[:*n]
	}
	if *asJSON {
		out := struct {
			Schema   int           `json:"schema"`
			Sessions []jsonSession `json:"sessions"`
		}{Schema: JSONSchemaVersion, Sessions: []jsonSession{}}
		ads := agents.All()
		for _, s := range ss {
			out.Sessions = append(out.Sessions, jsonSession{s, launch.Join(ads[s.Agent].ResumeCmd(s))})
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	for _, s := range ss {
		fmt.Println(listLine(s))
	}
	return nil
}

func listLine(s session.Session) string {
	proj := "?"
	if s.CWD != "" {
		proj = filepath.Base(s.CWD)
	}
	return fmt.Sprintf("%-6s  %-8s  %8s  %-22s  %s", s.Agent, short(s.ID), render.Relative(s.Updated),
		truncate(proj, 22), truncate(s.Title, 80))
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// ---------------------------------------------------------------- show / resume

func cmdShow(args []string) error {
	fs := flag.NewFlagSet("show", flag.ExitOnError)
	var f filter
	f.register(fs)
	maxChars := fs.Int("max-chars", 60000, "transcript budget")
	noTools := fs.Bool("no-tool-output", false, "omit tool results")
	noRedact := fs.Bool("no-redact", false, "don't mask secrets")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: relai show <ref>")
	}
	s, err := resolve(pos[0], f)
	if err != nil {
		return err
	}
	red, err := newRedactor(*noRedact)
	if err != nil {
		return err
	}
	doc, _, err := renderSession(s, *maxChars, !*noTools, red)
	if err != nil {
		return err
	}
	fmt.Print(doc)
	return nil
}

// cmdFull writes the complete transcript of a session, secrets masked, to a cached
// file and prints its path. The cache holds one file per (session, snapshot), made
// on demand: a handoff pins the snapshot it described, so the copy matches the
// handoff even if the session has grown since.
func cmdFull(args []string) error {
	fs := flag.NewFlagSet("full", flag.ExitOnError)
	var f filter
	f.register(fs)
	snapshot := fs.Int("snapshot", 0, "source records to include (from a handoff file; default: all so far)")
	toStdout := fs.Bool("stdout", false, "print the transcript instead of its path")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: relai full <ref> [--snapshot N] [--stdout]")
	}
	s, err := resolve(pos[0], f)
	if err != nil {
		return err
	}
	ad := agents.All()[s.Agent]
	home := relaiHome()

	snap := *snapshot
	if sn, ok := ad.(session.Snapshotter); ok && snap == 0 {
		if snap, err = sn.Records(s.Path); err != nil {
			return err
		}
	}
	path := fullcache.Path(home, s.Agent, s.ID, snap)
	if !fullcache.Exists(path) {
		snap, tr, err := loadTranscript(ad, s.Path, snap)
		if err != nil {
			return err
		}
		red, err := newRedactor(false)
		if err != nil {
			return err
		}
		doc := red.String(render.Full(s, tr, render.Options{
			AgentLabel: ad.Label(), ResumeCmd: launch.Join(ad.ResumeCmd(s)), Snapshot: snap,
		}))
		if err := fullcache.Write(path, []byte(doc)); err != nil {
			return err
		}
	}
	fullcache.Touch(path)
	fullcache.Prune(home, fullcache.MaxAge)
	if *toStdout {
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fmt.Print(string(b))
		return nil
	}
	fmt.Println(path)
	return nil
}

func cmdClean(args []string) error {
	n, err := fullcache.Clean(relaiHome())
	if err != nil {
		return err
	}
	fmt.Printf("removed %d cached transcript(s)\n", n)
	return nil
}

func cmdResume(args []string) error {
	fs := flag.NewFlagSet("resume", flag.ExitOnError)
	var f filter
	f.register(fs)
	newWin := fs.Bool("new-window", false, "open in a new terminal window (macOS)")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: relai resume <ref>")
	}
	s, err := resolve(pos[0], f)
	if err != nil {
		return err
	}
	return launch.Run(agents.All()[s.Agent].ResumeCmd(s), s.CWD, *newWin)
}

// ---------------------------------------------------------------- handoff

const summaryPrompt = `You are writing a handoff briefing so a different coding agent can continue this work.
The condensed transcript of a %s session in %s is provided on stdin. Write a concise markdown briefing
with these sections: Goal; What was done (key changes, files touched); Current state (what works,
what's broken, what was in progress when the session ended); Decisions & constraints (things the
user asked for or ruled out); Next steps; Gotchas. Be specific: file paths, commands, error messages.
Only use facts from the transcript. Output only the briefing.`

func handoffPrompt(srcLabel, file, fullCmd, extra string) string {
	p := fmt.Sprintf("You're continuing work handed off from a previous %s session. Read the handoff file first: %s. ", srcLabel, file)
	if fullCmd != "" {
		p += fmt.Sprintf("If something in it is unclear or you need exact details, run `%s`: it prints the path of "+
			"the complete session transcript with secrets masked (made on demand). It is large, so search it "+
			"instead of reading it whole; the [full L…] markers in the handoff file give the relevant line ranges. ", fullCmd)
	}
	p += "Then tell me in a few lines where things stand and what you'd do next, " +
		"and wait for my go-ahead before changing anything."
	if extra != "" {
		p += " " + extra
	}
	return p
}

type handoffOpts struct {
	to, note, prompt, cd, summarizeWith string
	summarize, noLaunch, newWindow      bool
	noRedact                            bool
	maxChars                            int
}

func cmdHandoff(args []string) error {
	fs := flag.NewFlagSet("handoff", flag.ExitOnError)
	var f filter
	f.register(fs)
	var o handoffOpts
	fs.StringVar(&o.to, "to", "", "target agent (required)")
	fs.StringVar(&o.note, "note", "", "markdown briefing to put at the top")
	fs.BoolVar(&o.summarize, "summarize", false, "have an agent write a briefing first")
	fs.StringVar(&o.summarizeWith, "summarize-with", "", "agent to summarize with (default: target)")
	fs.StringVar(&o.prompt, "prompt", "", "extra instructions for the receiving agent")
	fs.StringVar(&o.cd, "cd", "", "directory to start the target in (default: session's)")
	fs.BoolVar(&o.noLaunch, "no-launch", false, "only write the handoff file and print its path")
	fs.BoolVar(&o.newWindow, "new-window", false, "open in a new terminal window (macOS)")
	fs.BoolVar(&o.noRedact, "no-redact", false, "don't mask secrets")
	fs.IntVar(&o.maxChars, "max-chars", 60000, "transcript budget")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 || o.to == "" {
		return errors.New("usage: relai handoff <ref> --to AGENT")
	}
	s, err := resolve(pos[0], f)
	if err != nil {
		return err
	}
	return doHandoff(s, o)
}

func doHandoff(s session.Session, o handoffOpts) error {
	ads := agents.All()
	target, ok := ads[o.to]
	if !ok {
		return fmt.Errorf("unknown agent %q", o.to)
	}
	src := ads[s.Agent]
	red, err := newRedactor(o.noRedact)
	if err != nil {
		return err
	}
	doc, snap, err := renderSession(s, o.maxChars, true, red)
	if err != nil {
		return err
	}
	var briefings []string
	if o.note != "" {
		b, err := os.ReadFile(expand(o.note))
		if err != nil {
			return err
		}
		briefings = append(briefings, "## Briefing from the outgoing agent\n\n"+red.String(string(b)))
	}
	if o.summarize {
		using := o.summarizeWith
		if using == "" {
			using = o.to
		}
		fmt.Fprintf(os.Stderr, "summarizing with %s…\n", using)
		brief, err := summarize(ads[using], s, src.Label(), doc)
		if err != nil {
			fmt.Fprintln(os.Stderr, "relai: warning: summarize failed:", err)
		} else if brief != "" {
			briefings = append(briefings, "## Briefing (auto-generated)\n\n"+red.String(brief))
		}
	}
	doc = render.WithBriefings(doc, briefings...)

	dir := filepath.Join(relaiHome(), "handoffs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	out := filepath.Join(dir, fmt.Sprintf("%s-%s-%s.md", s.Agent, short(s.ID), time.Now().Format("20060102-150405")))
	if err := os.WriteFile(out, []byte(doc), 0o600); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "handoff written:", out)
	if n := red.Total(); n > 0 {
		fmt.Fprintf(os.Stderr, "masked %d secret(s): %s\n", n, summarizeCounts(red.Count))
	}
	if o.noLaunch {
		fmt.Println(out)
		return nil
	}
	dirTo := o.cd
	if dirTo == "" {
		dirTo = s.CWD
	}
	return launch.Run(target.LaunchCmd(handoffPrompt(src.Label(), out, fullCommand(s, snap), o.prompt)), dirTo, o.newWindow)
}

func summarize(ad session.Adapter, s session.Session, srcLabel, transcript string) (string, error) {
	tmp, err := os.CreateTemp("", "relai-brief-*.md")
	if err != nil {
		return "", err
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	argv := ad.SummarizeCmd(fmt.Sprintf(summaryPrompt, srcLabel, s.CWD), tmp.Name())
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin = strings.NewReader(transcript)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if st, err := os.Stat(s.CWD); err == nil && st.IsDir() {
		cmd.Dir = s.CWD
	}
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%v: %s", err, truncate(strings.TrimSpace(stderr.String()), 400))
	}
	if b, _ := os.ReadFile(tmp.Name()); len(bytes.TrimSpace(b)) > 0 {
		return strings.TrimSpace(string(b)), nil
	}
	return strings.TrimSpace(stdout.String()), nil
}

func summarizeCounts(c map[string]int) string {
	var parts []string
	for k, v := range c {
		parts = append(parts, fmt.Sprintf("%s×%d", k, v))
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

func expand(p string) string {
	if strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	return p
}

// ---------------------------------------------------------------- launch

func cmdLaunch(args []string) error {
	fs := flag.NewFlagSet("launch", flag.ExitOnError)
	to := fs.String("to", "", "target agent")
	file := fs.String("file", "", "handoff or briefing file")
	cd := fs.String("cd", "", "directory to start in")
	prompt := fs.String("prompt", "", "extra instructions")
	newWin := fs.Bool("new-window", false, "open in a new terminal window (macOS)")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	ad, ok := agents.All()[*to]
	if !ok || *file == "" {
		return errors.New("usage: relai launch --to AGENT --file FILE")
	}
	abs, err := filepath.Abs(expand(*file))
	if err != nil {
		return err
	}
	if _, err := os.Stat(abs); err != nil {
		return err
	}
	dir := *cd
	if dir == "" {
		dir, _ = os.Getwd()
	}
	return launch.Run(ad.LaunchCmd(handoffPrompt("agent", abs, "", *prompt)), dir, *newWin)
}

// ---------------------------------------------------------------- pick

func cmdPick(args []string) error {
	fs := flag.NewFlagSet("pick", flag.ExitOnError)
	var f filter
	f.register(fs)
	newWin := fs.Bool("new-window", false, "open in a new terminal window (macOS)")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	ss, _, err := loadSessions(f)
	if err != nil {
		return err
	}
	if len(ss) == 0 {
		return errors.New("no sessions found")
	}
	byKey := map[string]session.Session{}
	for _, s := range ss {
		byKey[s.Agent+":"+s.ID] = s
	}
	other := map[string]string{"claude": "codex", "codex": "claude", "gemini": "claude", "cursor": "claude"}

	var key string
	var chosen session.Session
	if _, err := exec.LookPath("fzf"); err == nil {
		self, _ := os.Executable()
		var lines []string
		for _, s := range ss {
			lines = append(lines, s.Agent+":"+s.ID+"\t"+listLine(s))
		}
		cmd := exec.Command("fzf", "--delimiter", "\t", "--with-nth", "2", "--no-sort",
			"--expect", "enter,ctrl-o,ctrl-y",
			"--header", "enter: resume · ctrl-o: hand off to the other agent · ctrl-y: write handoff file only",
			"--preview", launch.Quote(self)+" show {1} --max-chars 8000",
			"--preview-window", "right,55%,wrap")
		cmd.Stdin = strings.NewReader(strings.Join(lines, "\n"))
		cmd.Stderr = os.Stderr
		out, err := cmd.Output()
		if err != nil {
			return nil // cancelled
		}
		res := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(res) < 2 {
			return nil
		}
		key = res[0]
		chosen = byKey[strings.SplitN(res[1], "\t", 2)[0]]
	} else {
		for i, s := range ss[:min(len(ss), 40)] {
			fmt.Printf("%3d. %s\n", i+1, listLine(s))
		}
		var in string
		fmt.Print("pick #: ")
		fmt.Scanln(&in)
		i, err := strconv.Atoi(in)
		if err != nil || i < 1 || i > min(len(ss), 40) {
			return nil
		}
		chosen = ss[i-1]
		fmt.Print("[r]esume, [h]and off to the other agent, write handoff [f]ile: ")
		fmt.Scanln(&in)
		key = map[string]string{"r": "enter", "h": "ctrl-o", "f": "ctrl-y"}[strings.TrimSpace(in)]
	}
	switch key {
	case "enter":
		return launch.Run(agents.All()[chosen.Agent].ResumeCmd(chosen), chosen.CWD, *newWin)
	case "ctrl-o", "ctrl-y":
		return doHandoff(chosen, handoffOpts{to: other[chosen.Agent], noLaunch: key == "ctrl-y",
			newWindow: *newWin, maxChars: 60000})
	}
	return nil
}

// ---------------------------------------------------------------- setup

func cmdInstallSkills(args []string) error {
	written, err := skills.Install()
	for _, w := range written {
		fmt.Println("installed", w)
	}
	if err == nil {
		fmt.Println("restart your agents to pick up the skill.")
	}
	return err
}

func cmdDoctor(args []string) error {
	fmt.Println("relai", versionString())
	fmt.Println("home:", relaiHome())
	for _, bin := range []string{"claude", "codex", "gemini", "cursor-agent", "cmux", "fzf"} {
		p, err := exec.LookPath(bin)
		if err != nil && bin == "cmux" {
			p, err = launch.CmuxBin() // also finds it inside the app bundle
		}
		if err == nil {
			fmt.Printf("  %-12s %s\n", bin, p)
		} else {
			fmt.Printf("  %-12s not found on PATH\n", bin)
		}
	}
	for _, t := range skills.Targets() {
		p := filepath.Join(t, "relai", "SKILL.md")
		b, err := os.ReadFile(p)
		switch {
		case err != nil:
			fmt.Printf("  skill   missing: %s (run: relai install-skills)\n", p)
		case !bytes.Equal(b, skills.SkillMD):
			fmt.Printf("  skill   outdated: %s (run: relai install-skills)\n", p)
		default:
			fmt.Printf("  skill   ok: %s\n", p)
		}
	}
	start := time.Now()
	all, st, err := loadSessions(filter{all: true})
	if err != nil {
		return err
	}
	hidden := map[string]int{}
	for _, s := range all {
		if s.Hidden {
			hidden[s.Agent]++
		}
	}
	for _, name := range agents.Names() {
		fmt.Printf("%-7s %5d session files, %d hidden (subagent / non-interactive)\n", name, st.Files[name], hidden[name])
	}
	fmt.Printf("index: %d file(s) re-parsed in %s\n", st.Parsed, time.Since(start).Round(time.Millisecond))
	if len(st.Failures) > 0 {
		fmt.Printf("%d file(s) could not be read:\n", len(st.Failures))
		i := 0
		for p, e := range st.Failures {
			if i++; i > 10 {
				break
			}
			fmt.Printf("  %s: %s\n", p, e)
		}
	}
	return nil
}
