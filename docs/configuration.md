# Configuration

`~/.config/owl/config.yaml` (`$XDG_CONFIG_HOME` and `$OWL_CONFIG`
respected). `owl config init` writes this, with longer comments; every key is
optional, and these are the defaults.

Several configs, picked with `--config` or `$OWL_CONFIG` — a work one and a
personal one, say, each with its own repositories and Linear workspaces —
keep their lists apart: each caches in `~/.cache/owl/<file name>/`, named
after links are followed, so neither shows nor `owl pr --check` announces
the other's rows. `config.yaml` caches in `~/.cache/owl/` itself.

```yaml
mux: auto                        # tmux, herdr, cmux, or auto: herdr or cmux when owl runs inside one, else tmux

tmux:
  session: reviews               # one window per review lives here
  keepalive_window: scratch      # keeps the session alive with no reviews open

herdr:
  socket: ""                     # default: $HERDR_SOCKET_PATH, else ~/.config/herdr/herdr.sock

remote: origin                   # the GitHub remote: PRs are listed for it and fetched from it
worktrees_dir: .worktrees.local  # relative to the repo root
default_repo: ""                 # used when owl starts outside a git repo
repos: {}                        # where each team's issues open, by team key: {VI: ~/src/voyage}; a team not named opens where owl runs

merged_window: 3d                # how far back the Merged sections reach: 3d, 12h, 90m — said back in the section header
done_window: 3d                  # the same for the issue list's Done section

agent:
  cmd: claude --permission-mode auto   # Claude Code, with your flags (e.g. --model claude-opus-5); -c is appended when the worktree has a prior conversation
  prompt: "/owl:review {pr}"           # first prompt of a fresh review
  stack_prompt: "/owl:review {pr} stack"   # first prompt of a fresh stack review; {pr} the tip, {prs} every layer
  link_local:                          # symlinked from the repo into each new worktree (keep them gitignored there)
    - .claude/settings.local.json
    - .claude/*.local.md
    - .claude/skills/*.local

issue:
  session: features              # tmux: one window per feature lives here; herdr and cmux need no container
  prompt: "/owl:feature {key}"   # first prompt of a fresh feature; {key} is the issue's key
  claim: true                    # opening an issue makes it yours in Linear: started, and assigned to you when nobody holds it
  cmd: ""                        # the agent command a feature starts with, in place of agent.cmd; "" is agent.cmd

mine:                            # a PR of your own, from the second pane of `owl pr`
  prompt: "Please read up on…"   # first prompt of a *fresh* conversation on one; {pr}, {branch}, {key}

project:
  session: projects              # tmux: one window per project — the conversation above the issues
  prompt: "/owl:project {name}"  # first prompt of a fresh project; {name} is the project's name in Linear
  dim_statuses: [Paused]         # statuses that mean present but not moving; their rows render dim
  cmd: ""                        # the agent command a project starts with, in place of agent.cmd; "" is agent.cmd

groups:                          # cmux workspace groups, one per scope; tmux and herdr ignore them
  enabled: false                 # folds the sidebar, costs its single recency order — see docs/multiplexers.md
  reviews:  { color: "#00afff", icon: eye }
  features: { color: "#00d75f", icon: hammer }
  projects: { color: "#af87ff", icon: square.stack.3d.up }

open_cmd: ""                     # opens URLs; default: open (macOS) or xdg-open
on_open: auto                    # the TUI once an open starts: auto (quit under tmux, stay under herdr and cmux), quit, stay, or switch (move your client to the reviews)

hooks:
  after_open: ""                 # runs after every open with OWL_PR/OWL_ISSUE, _SESSION, _WINDOW, _WORKTREE, _REPO, _MUX set
                                 # or one per multiplexer: {tmux: spaces focus reviews}

theme:                           # the three Claude-state colours (ANSI 0-255 or #rrggbb)
  working: "#dbbc7f"
  blocked: "214"
  done: "42"

keys:                            # rebind any action: a key name or a list
  open: enter
  start: s
  quit: [q, ctrl+c]

# pr:                            # unset: the PR list holds the repo you are standing in
#   owners: ["@me", acme]        # the owners it spans instead; owl pr --here narrows to this repo
#   teams: [acme/web, acme/api]  # the teams whose review requests you follow, as org/team; written with none, no team's; unset, every team you are in

linear:                          # the issue tracker behind `owl issue`
  token: ""                      # op://<vault>/<item>/<field>, file://<path>, $VAR, or the key; Read for the lists, Write for issue.claim and `owl hoot`
  account: ""                    # the 1Password account the item is in, when several are signed in
  team: ""                       # the team's key (ENG in ENG-123): where `owl hoot` files issues, and — unless teams says otherwise — which keys in a branch name count as issues
  # teams: [DEV, LIFE]           # every team you file work under; defaults to the single team above

bindings:                        # your own keys on a row: a prompt for its agent, or a URL to open
  pr:
    - key: f                     # shipped; listing it again replaces it
      name: check feedback
      prompt: "Please carefully check the feedback since your last review …"
      when: conversation
  issue:
    - key: f                     # shipped; the review you were given, not the one you are giving
      name: check the review
      prompt: "Please go through the review on {key}'s pull request carefully … Agreeing with all of it is the failure mode, not the goal."
      when: conversation
```

The default `agent.cmd` runs Claude with `--permission-mode auto`
inside a checkout the PR's author controls, and `link_local` never
replaces a file the PR ships under the same name — so a PR's own
`.claude/` is what the agent starts with. Read that part of the diff
first when it matters.

Two hooks, two layers: `on_open` is what the TUI itself does once an
open starts; `hooks.after_open` is a shell command run after *every*
`open` (TUI or CLI), where window-manager glue goes.
