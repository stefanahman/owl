# Multiplexers

Reviews and features live in a terminal multiplexer: one window per
workspace, the agent typed into it, its state read back for the `©`
badge. `mux: auto` picks herdr when owl runs inside it (`HERDR_ENV=1`),
cmux inside cmux (`CMUX_WORKSPACE_ID`) and tmux otherwise; `mux: tmux`,
`herdr` or `cmux` in the config forces one, `owl --mux cmux` forces one
for a run. The drivers are [mux](https://github.com/stefanahman/mux)'s;
owl is the review side of them.

| | tmux | herdr | cmux |
|---|---|---|---|
| a workspace | a window of `tmux.session` (reviews) or `issue.session` (features), cwd the worktree | a workspace labelled after the worktree, cwd the worktree | a workspace named after the worktree, cwd the worktree |
| grouping | none: the session is the grouping | none | off unless `groups.enabled`; then a workspace group per scope, named `reviews`, `features` or `projects` — the same words as the tmux sessions — coloured and iconed from `groups` in the config |
| the agent's state | [claude-status](https://github.com/stefanahman/claude-status), from Claude Code's hooks | herdr's own detection, from the screen: a few seconds behind, so a badge can trail by one refresh | claude-status's sidebar pill when the plugin runs there; without it cmux's own Claude Code hooks, whose `needsInput` also covers Claude's 60-second idle reminder |
| `done` | finished, not yet looked at; focusing the window acknowledges it | the same, from herdr | the pill's `done` (or cmux's `idle`) while cmux's notification about the turn is unread; Enter in owl marks it read |
| a prompt while Claude waits | refused from the state option | refused by herdr's `agent.prompt` itself; an agent herdr hasn't detected gets the text typed | refused from the pill, or from cmux's hook state without it; an agent neither saw gets the text typed |
| Enter arrives | `select-window`, then your hook | `workspace focus`; every attached client follows | `workspace select`, and `focus-window` when owl runs outside cmux |
| the TUI after an open (`on_open: auto`) | quits — the popup closes, the open finishes behind it | stays in its workspace | stays in its workspace |
| a failure after the TUI is gone | tmux's status line, eight seconds | a herdr notification | a cmux notification, on owl's own workspace |
| `hooks.after_open` sees | `OWL_SESSION` = the session, `OWL_WINDOW` = the window | `OWL_SESSION` = the herdr session, `OWL_WINDOW` = the label | `OWL_WINDOW` = the name; cmux has no session |

Every hook also gets `OWL_PR` or `OWL_ISSUE` (and `OWL_BRANCH`),
`OWL_WORKTREE`, `OWL_REPO` and `OWL_MUX`, the last for hooks that only
make sense with one multiplexer. `hooks.after_open` takes a mapping
for that: `{tmux: spaces focus reviews}` runs under tmux and nothing
elsewhere. `--config FILE` reads another config; `OWL_CONFIG` does the
same for hooks that cannot pass flags.

## tmux

Any version runs `open` and `close`; the popup needs ≥ 3.2. The
reviews session is created on first use and kept alive by
`tmux.keepalive_window` when no review is open. Bind the list to a
popup:

```tmux
bind r display-popup -E -w 88% -h 84% owl pr
```

tmux runs that with the server's PATH — give the absolute path if
`owl` isn't on it. The agent's state comes from
[claude-status](https://github.com/stefanahman/claude-status); without
it the badge only says "a window exists".

## herdr

Run `owl` in a pane of the session your workspaces should join. From
outside, `herdr.socket` names the server (default `$HERDR_SOCKET_PATH`,
else `~/.config/herdr/herdr.sock`), and `OWL_SESSION` is the session's
name as read from that path. herdr ≥ 0.9.

## cmux

Run `owl` in a cmux terminal: cmux's socket admits only processes
started inside it, unless cmux itself was started with
`CMUX_SOCKET_MODE=allowAll`. The state needs cmux's Claude Code
integration (`automation.claudeCodeIntegration` in
`~/.config/cmux/cmux.json`, on by default) or, better, claude-status,
whose pill does not mistake an idle Claude for a blocked one. cmux ≥
0.64, macOS.

## cmux workspace groups

**Off unless you ask for it — `groups.enabled: true`.** Grouping buys a
folded sidebar and costs its single recency order, and that is the
whole of the trade. cmux sorts groups by their own most recent
notification and sorts members below their anchor by theirs, so a
review that pinged two minutes ago sits under a features group that
pinged one minute ago, beneath an anchor that never pings at all. With
a handful of workspaces the folding wins. Past that, the sidebar stops
being readable top to bottom, which is what it was for.

One switch rather than one per scope: a single group is enough to pull
its members out of the common order, so the trade does not divide. The
useful shape, if you want both, is to group the furniture — the
long-lived workspaces you keep — by hand, and leave the work
ungrouped.

With it on, each scope's workspaces land in a group of their own, named
after the scope: `reviews`, `features`, `projects`. No new vocabulary —
those are the tmux session names and the names of Stefan's spaces, so
renaming one means renaming all of them.

The group is meant to be made from the first workspace that needs it,
which becomes its anchor, so that cmux drops the group when its last
member closes. owl therefore touches groups on exactly one path —
`windows.Open`, the branch that runs when the window does not exist yet
— and never on close. Grouping on every open would look harmless and
would not be: it would drag a workspace back that you had pulled out of
its group by hand.

Getting there takes a repair, and owl 0.11.0 shipped without it.
cmux's `workspace-group create --from <ws>` does not anchor the group
on the workspace it is given — `--from` takes a list and means
*capture these*, as its own help says of the opposite case: omitting it
"creates an anchor-only group without capturing live workspaces". So
cmux invents an anchor titled after the group and adds it alongside,
the group is born with two members, and it outlives its last review
because the invented one is still in it. That is the dedicated-anchor
shape this design rejected.

mux ≥ 0.6.1 repairs it on the create path: it re-reads the groups and,
when the anchor is one cmux invented rather than the workspace owl
passed, moves the anchor across and closes the invented one. Closing a
workspace is the one irreversible thing here, so it is guarded to the
exact shape a fresh create leaves — a group of two whose anchor is not
ours — and a group holding anything else keeps its phantom header
rather than risk a workspace of yours. A refused move is not an error:
the workspace is grouped either way.

Two consequences. Anything opened under owl 0.11.0 left a phantom
workspace per group, named `reviews`, `features` or `projects`; the fix
stops new ones and does not retire those, because closing a workspace
is a person's call. And the repair infers "cmux invented this anchor"
from the shape of the group. cmux's rpc payload on main carries
`anchor_workspace_is_generated`, which would make it exact; 0.64.22
does not expose it, so upgrading cmux would let both sides simplify.

Two more things to know when it misbehaves. A group is identified by
its name, because the id the API carries is not reachable from cmux's
CLI: rename the group in the sidebar and owl stops finding it, and the
next open makes a second group under the old name. And the style is
stored without validation — cmux keeps whatever string it is given, so
a misspelt `icon` draws nothing rather than failing. (The three
shipped symbols, `eye`, `hammer` and `square.stack.3d.up`, do resolve.)

## A clean environment

The multiplexer has to come from a clean environment. A tmux server, a
herdr server or the cmux app started from inside a Claude Code session
keeps its `CLAUDECODE` marker, and every agent started in it is a child
session that saves no transcript; cmux started from a shell inside tmux
keeps `TMUX`, and its shell integration then hands `CMUX_SURFACE_ID` to
tmux before every command, so the hooks the states come from never
engage. owl reads the server's or app's environment before it lists
anything (mux's `Ping`) and refuses with the fix in the message:
restart it from a hotkey or a plain shell.

## Not every agent tool is a backend

The interface asks for a window to type into, a state to read back, a
way to focus that window and a way to notify. [Paseo](https://paseo.sh)
has terminals but exposes none of the last three for them, and its
agents are headless sessions with no terminal to type into. It would
fit as a different kind of backend, one handed the agent and the prompt
rather than a shell line; that change waits for a backend that needs
it.
