// The file `owl config init` writes: every key with its default and a
// line saying what it is for. A page of prose rather than code, kept
// out of config.go so the types and the parsing read as one thing —
// TestTemplateMatchesDefaults is what holds the two in step.
package main

// configTemplate is what `owl config init` writes. It is the
// documentation for every key, and TestTemplateMatchesDefaults keeps it
// equal to defaultConfig().
const configTemplate = `# owl configuration. Every key is optional; these are the defaults.

mux: auto                        # the multiplexer reviews run in: tmux, herdr, cmux, or auto (herdr or cmux when owl runs inside one, else tmux)

tmux:
  session: reviews               # session that holds one window per review
  keepalive_window: scratch      # window that keeps the session alive with no reviews open

herdr:
  socket: ""                     # herdr's socket; default: $HERDR_SOCKET_PATH, else ~/.config/herdr/herdr.sock

remote: origin                   # git remote of the GitHub repo: PRs are listed for it and fetched from it
worktrees_dir: .worktrees.local  # where review worktrees go, relative to the repo root (added to .git/info/exclude)
default_repo: ""                 # repo to use when owl is started outside a git repo; ~ is expanded

agent:
  cmd: claude --permission-mode auto     # Claude Code, with your flags (e.g. --model claude-opus-5); owl appends -c when the worktree has a prior conversation (found in ~/.claude/projects)
  prompt: "/owl:review {pr}"    # first prompt of a fresh review; {pr} is the PR number
  link_local:                            # globs relative to the repo root, symlinked into each new worktree
    - .claude/settings.local.json
    - .claude/*.local.md
    - .claude/skills/*.local

issue:
  session: features              # tmux: one window per feature lives here; herdr and cmux need no container
  prompt: "/owl:feature {key}"   # first prompt of a fresh feature; {key} is the issue's key (BAR-123)

mine:                            # a PR of your own, opened from the second pane of ` + "`owl pr`" + `
  prompt: "Please read up on where this work stands before changing anything. The branch is {branch} and its pull request is #{pr}: read the commits against the base, the PR's checks, its review comments and whether it merges cleanly, and anything uncommitted in the worktree. Then tell me what is done, what is left, and what is stopping it from landing — and wait for me. Do not review this PR: it is mine, not one I was asked to look at."    # first prompt of a *fresh* conversation on a PR of your own; {pr}, {branch}, and {key} (empty when the branch carries no issue). A workspace that already holds a conversation resumes it instead and is sent no prompt at all, so this fires when you arrive somewhere for the first time

project:
  session: projects              # tmux: one window per project lives here — the conversation above the issues
  prompt: "/owl:project {name}"  # first prompt of a fresh project; {name} is the project's name in Linear
  dim_statuses: [Paused]         # statuses that mean present but not moving: their rows render dim. Linear types Paused as started, the same as In Progress, so only this tells them apart

groups:                          # cmux workspace groups, one per scope, named after the scope itself; tmux and herdr have no such thing and ignore this
  enabled: false                 # off by default: grouping folds the sidebar but costs its single recency order, since cmux sorts groups by their own latest notification and members below their anchor
  reviews:                       # the group is made from the first workspace that needs it, and cmux removes it when the last member closes
    color: "#00afff"             # "#RRGGBB"; empty leaves cmux's default
    icon: eye                    # an SF Symbol name; empty for none. cmux does not check that the symbol exists
  features:
    color: "#00d75f"
    icon: hammer
  projects:
    color: "#af87ff"
    icon: square.stack.3d.up

open_cmd: ""                     # opens URLs; default: open (macOS) or xdg-open
on_open: auto                    # the TUI once an open starts: auto (quit under tmux, where a popup closes at once and open finishes behind it; stay under herdr and cmux, where the list keeps its own workspace), quit, stay (keep the list), switch (move your client to the reviews, for owl in a tmux window)

hooks:
  attention: ""                  # runs when owl pr --check finds work has arrived, with OWL_ARRIVED, OWL_ARRIVED_PRS, OWL_WAITING, OWL_SUMMARY, OWL_REPO, OWL_MUX set; a mapping gives one per multiplexer, as after_open does
                                 # empty: owl uses the multiplexer's own notification instead
  after_open: ""                 # command run after an open with OWL_PR (a review) or OWL_ISSUE and OWL_BRANCH (a feature), OWL_WINDOW, OWL_WORKTREE, OWL_REPO, OWL_MUX set, and OWL_SESSION under tmux and herdr; ~ is expanded.
                                 # A mapping gives one per multiplexer, e.g. {tmux: spaces focus reviews}: none under herdr and cmux, where the window is already in front

# How far back the "what just finished" sections reach: the PR list's
# Merged section and the merged half of the mine pane, and the issue
# list's Done section. Written as 3d, 12h or 90m, and said back in the
# section header. Three days so what landed on Friday is still there on
# Monday.
merged_window: 3d
done_window: 3d

theme:                           # lipgloss colours: ANSI 0-255 or #rrggbb
  working: "#dbbc7f"
  blocked: "214"
  done: "42"

keys:                            # one key name or a list; names as bubbletea spells them (enter, esc, pgup, ctrl+u, ...)
  up: [up, k]
  down: [down, j]
  top: [g, home]
  bottom: [G, end]
  page_up: [pgup, ctrl+u]
  page_down: [pgdown, ctrl+d]
  open: enter
  start: s
  browser: o
  yank: y
  next: n
  pane: tab                       # move between the panes of a list; the inactive one dims
  drill: right                    # on a project row: its issues, grouped by milestone
  back: left                      # back out of a drilled list
  cleanup: c
  search: /
  cancel: esc
  refresh: r
  help: "?"
  quit: [q, ctrl+c]

# Which repositories the PR list holds. Unset, it is the one you are
# standing in, so the list is scoped by your working directory.
# pr:
#   owners: ["@me", acme]        # your own namespace and the orgs you opt into, which GitHub ORs; owl pr --here narrows to this repo whatever this says. An allowlist on purpose: a search with no repository qualifier spans every repo your account can see

linear:                          # the issue tracker behind ` + "`owl issue`" + `
  token: ""                      # a personal API key (Linear: Settings → Security & access) as a reference: op://<vault>/<item>/<field> is read from 1Password once and kept in ~/.local/state/owl/linear.token, mode 600; file://<path> reads a file of yours (mode 600); $VAR reads the environment; anything else is the key itself
  account: ""                    # the 1Password account the item is in (its sign-in address), when more than one is signed in
  team: ""                       # the team's key (BAR in BAR-123): where ` + "`owl hoot`" + ` files issues, and — unless teams says otherwise — which keys in a branch name count as issues
  # teams: [DEV, LIFE]           # every team you file work under, when one team is not the whole story: which keys in a branch name are yours to follow. Defaults to the single team above; with neither set, a PR of yours never resolves to its feature's workspace

bindings:                        # your own keys on a row: a prompt handed to the agent, or a URL opened; ` + "`?`" + ` lists them by name
  pr:                            # on a PR; placeholders {pr}, {repo}, {branch}, {url}, and {id} — the first match of ` + "`pattern`" + ` in the title, body and branch (no match → the key does nothing)
    - key: f                     # shipped; an entry of yours with the same key replaces it, other keys add to it
      name: check feedback
      prompt: "Please carefully check the feedback since your last review — take your time. First pass: check whether each prior finding is resolved (file:line evidence). Second pass: critique your own conclusions and drop weak claims. Output: RESOLVED / STILL BROKEN / NEW CONCERNS / new verdict."
      when: conversation         # only where a workspace or a prior conversation exists; without it, a fresh workspace starts with the prompt
    # - key: d
    #   name: Dependabot
    #   prompt: "/owl:dependabot {pr}"
    # - key: l
    #   name: Linear
    #   pattern: 'PROJ-\d+'
    #   url: https://linear.app/<org>/issue/{id}
  mine:                          # on one of your own PRs, in the mine pane; same placeholders as pr
    - key: f                     # shipped; the review you were given, checked rather than complied with
      name: check the review
      prompt: "Please go through the review on pull request {pr} carefully — take your time, and treat every comment as a claim to check rather than an instruction to follow. First pass: for each comment find the code it is about and decide whether it is right, citing file:line. A reviewer can be wrong about what the code does, or right about the code and wrong about this repository's conventions — check both. Second pass: critique your own verdicts and drop any you cannot evidence. Then change only what survives that, and answer the rest with what you found. Report each comment as AGREED (and what you changed) / DISAGREED (and the evidence) / UNCLEAR (and the question you need answered). Agreeing with all of it is the failure mode, not the goal."
      when: conversation
  issue:                         # on an issue; placeholders {key}, {repo}, {branch}, {url}, {id} (pattern over title and branch)
    - key: f                     # shipped; the other side of the PR list's f — the review you were given, not the one you are giving
      name: check the review
      prompt: "Please go through the review on {key}'s pull request carefully — take your time, and treat every comment as a claim to check rather than an instruction to follow. First pass: for each comment find the code it is about and decide whether it is right, citing file:line. A reviewer can be wrong about what the code does, or right about the code and wrong about this repository's conventions — check both. Second pass: critique your own verdicts and drop any you cannot evidence. Then change only what survives that, and answer the rest with what you found. Report each comment as AGREED (and what you changed) / DISAGREED (and the evidence) / UNCLEAR (and the question you need answered). Agreeing with all of it is the failure mode, not the goal."
      when: conversation
    # - key: p
    #   name: Continue
    #   prompt: "Continue {key} where the last session left off"
    #   when: conversation
`
