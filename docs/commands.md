# Commands

```
owl [--config FILE] [--mux tmux|herdr|cmux] [<noun> [command]]   the options apply to every command, and to what the TUI runs
owl                                   this introduction and the commands
owl pr                                the PR list
owl pr open <N> [--prompt TEXT] [--stack]  open (or focus) PR N's workspace; with --prompt, hand the prompt to the agent; with --stack, the workspace of the stack's line through N, or for a line no longer open the workspace that reviewed it, while it is there
owl pr start <N> [--prompt TEXT] [--stack] the same without going there: no window selection, no after_open
owl pr close [--force] [--stack] [<N>]     remove the worktree, branch and window, or with --stack the stack workspace whose line holds N, or the one named `stack-…`; N is inferred from inside a workspace
owl pr stack <N> [--json]             the stack PR N is a layer of: the line from the trunk through N, the tips where it forks, and the notes directory
owl issue                             the open issues assigned to you; a table when stdout is not a terminal
owl issue open <KEY> [--prompt TEXT] [--base BRANCH]  open (or focus) the feature workspace of issue KEY, and claim the issue
owl issue start <KEY> [--prompt TEXT] [--base BRANCH] the same without going there
owl issue close [--force] [<KEY>]     remove the feature's worktree, local branch and window
owl issue new <title…>                file an issue in linear.team, assigned to you
owl project                           the projects you work in; a table when stdout is not a terminal
owl project open <id> [--prompt TEXT] open (or focus) the project's conversation; <id> is Linear's slug, its workspace's slug, or a fragment of the name
owl project start <id> [--prompt TEXT] the same without going there
owl project close [--force] <id>      remove the project's worktree and window; the session id is kept
owl hoot <title…>                     the same, from the owl
owl config init | path
owl --version
```

The noun is the scope — `pr`, `issue` or `project` — so a verb never
has to guess from the shape of an id which kind of thing it acts on.
`close` exits 2 when there was nothing to remove.
