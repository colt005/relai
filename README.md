# relai

**Relay, for AI.** Move a coding session from one agent to another without re-explaining anything.

Start a task in one coding agent, continue it in another, and come back again. relai finds your
sessions, condenses them into a briefing the next agent can read, masks any secrets along
the way, and opens the new agent right where you left off.

Works with **Claude Code**, **Codex**, **Gemini CLI** and **Cursor** (Cursor Agent CLI), with more agents on the way. Runs entirely on your Mac.

---

## Install

```sh
brew install colt005/tap/relai
relai install-skills
```

The second command teaches your agents (currently Claude Code and Codex) to use relai themselves. Restart them
afterwards.

Building from source instead? You'll need Go 1.27 or newer:

```sh
go install github.com/colt005/relai/cmd/relai@latest
```

## Quick start

```sh
relai pick
```

Browse every session from both agents with a live preview. Press **Enter** to resume one,
or **Ctrl-O** to continue it in the other agent. (Install `fzf` for the best experience:
`brew install fzf`.)

Or skip the picker and hand off your latest session in the current folder:

```sh
relai handoff latest -a claude --here --to codex
```

## From inside your agent

After `relai install-skills`, just ask in plain words:

> hand this off to codex

> continue from my last claude session in this repo

When handing off, the current agent writes a briefing from everything it knows right now:
the goal, what's done, what's broken, and what's next. relai then opens the other agent
with that briefing and a condensed transcript.

## Getting the full detail

A handoff file is condensed: long messages and tool output are clipped, and a very long
session loses its middle. Where something was cut, it carries a marker like
`[full L1840–1902]`. The header shows the command that makes the complete transcript:

```sh
relai full claude:3f2a9c --snapshot 812   # prints the path of the full transcript
relai full claude:3f2a9c --stdout         # or prints it
relai clean                               # deletes every cached transcript
```

The copy is made on demand, with secrets masked, and `--snapshot` pins it to the point
the handoff described, so it matches the handoff even if the session has grown since.
There is one file per session and snapshot in `~/.relai/cache/full` (private to you),
reused until it hasn't been used for 7 days.

## Menu bar app

**RelaiBar** puts your sessions one click away. Search, resume, or continue any session in
another agent from the menu bar.

It isn't part of the Homebrew install yet (a packaged release is planned), so build it from
a clone of this repo:

```sh
macos/RelaiBar/build-app.sh --install
```

Requires macOS 13 or newer and the Xcode Command Line Tools (`xcode-select --install`).
See [macos/RelaiBar](macos/RelaiBar/README.md) for details.

## Commands

| Command | What it does |
|---|---|
| `relai pick` | Browse and act on sessions interactively |
| `relai list` | List recent sessions from both agents |
| `relai show <session>` | Print a condensed transcript |
| `relai resume <session>` | Reopen a session in its own agent |
| `relai handoff <session> --to <agent>` | Continue a session in another agent |
| `relai launch --to <agent> --file <brief.md>` | Start an agent from any briefing file |
| `relai install-skills` | Install or update the agent skills |
| `relai doctor` | Check that everything is set up correctly |

A `<session>` can be `latest`, a session ID, the first few characters of one, or
`agent:id` (for example `codex:3f2a`).

Useful options:

- `-a claude`, `-a codex`, `-a gemini` or `-a cursor` limits to one agent, and `--here` to sessions started in the
  current folder.
- `--new-window` opens the agent in a new Terminal window instead of the current one.
- `--summarize` has an agent write a structured briefing before handing off.
- `--no-launch` writes the handoff file without starting anything.

## Privacy

Session transcripts often contain secrets that passed through a command's output: API
keys, tokens, passwords. Since a handoff sends your work to a different agent, relai masks
them first.

- Common formats are detected automatically: Anthropic, OpenAI, AWS, GitHub, GitLab,
  Slack, Google, Stripe, npm, and Hugging Face keys, JWTs, private keys, passwords in
  URLs, and secret-looking `KEY=value` pairs.
- Add your own patterns to `~/.relai/redact.txt`, one regular expression per line.
- Handoff files are saved in `~/.relai/handoffs/`, readable only by you.
- relai never uploads anything, never collects telemetry, and never modifies your agents'
  session files.

Masking is a safety net, not a guarantee. Review a handoff before sharing it further.

## Configuration

| Variable | Purpose | Default |
|---|---|---|
| `RELAI_HOME` | Where relai keeps handoffs and its index | `~/.relai` |
| `RELAI_TERMINAL` | `Terminal`, `iTerm` or `cmux` for `--new-window` | `Terminal` |
| `CLAUDE_CONFIG_DIR` | Claude Code's data folder, if moved | `~/.claude` |
| `CODEX_HOME` | Codex's data folder, if moved | `~/.codex` |

## Scripting

`relai list --json` returns a versioned list of sessions with everything a script or app
needs, including the command to resume each one. New fields may be added within a
version; existing ones won't change.

## Troubleshooting

Run `relai doctor` first. It shows which agents relai can find, how many sessions it can
read, whether the skills are installed and current, and any files it couldn't parse.

Agents change their session formats from time to time. If sessions go missing or titles
look wrong after an agent update, please
[open an issue](https://github.com/colt005/relai/issues) with the `relai doctor` output
and your agent's version.

## Contributing

Each agent is a small adapter in `internal/agents/`. To add one, implement the
`session.Adapter` interface, register it in `internal/agents/agents.go`, and add sample
sessions under `testdata/` with tests for titles, folders, and transcripts.

```sh
go test ./...
```

## License

See [LICENSE](LICENSE). Third-party notices are in
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).