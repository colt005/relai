# RelaiBar

A menu bar app for relai: browse your coding-agent sessions, resume them, or hand them
off to another agent in one click.

## Build and install

Needs macOS 13+, the relai CLI, and the Xcode Command Line Tools
(`xcode-select --install`). Full Xcode isn't required.

    ./build-app.sh --install

That builds `RelaiBar.app`, copies it to /Applications, and launches it. Look for the
⇄ icon in the menu bar. Turn on **Launch at login** in its settings.

For development, `swift run` starts it straight from the package.

## Using it

- Click the icon to see recent sessions from every agent (Claude Code, Codex, Gemini CLI,
  Cursor). Type to search by title, project, or path. The agent dropdown next to the
  search box narrows the list to one agent; it only lists agents that have sessions.
- Select a session (hover it, or use ↑/↓) to see its buttons: **Resume** continues it in
  the same agent, **Hand off** continues it in another agent, and ⋯ has the rest
  (continue in a fresh session, full preview, copy the resume command, open the project).
  Every icon button has a tooltip.
- Keyboard: ↑/↓ select, ↩ resume, ⌘↩ hand off to the last agent you used, ⌘P peek at the
  selected session, esc close the peek or clear the search. The search box is focused whenever the popover opens.
- Rest the pointer on a session for 1.5 seconds and a preview pops out beside the
  window, showing where the session left off. It can be turned off in settings.
- Sessions open in a new Terminal window, or iTerm2 or cmux if you pick one in settings.
  cmux only takes commands from other apps if Settings > Automation allows it. The first
  time, macOS asks permission for RelaiBar to control Terminal: allow it.

## How it works

RelaiBar is a thin UI over the relai CLI. It reads `relai list --json` and runs
`relai resume` / `relai handoff` for actions, so parsing, redaction, and the session
index all live in one place. Because apps launched from Finder don't get your shell's
PATH, RelaiBar asks your login shell for it once and passes it to relai, so relai can
find the agent commands.

## Troubleshooting

- **"Couldn't find the relai command"**: set the full path in settings (`which relai` in
  Terminal shows it).
- **Nothing opens when resuming**: check System Settings → Privacy & Security →
  Automation, and allow RelaiBar to control Terminal (or iTerm).
- **Sessions missing or wrong**: run `relai doctor` in Terminal; RelaiBar shows exactly
  what the CLI sees.
