# Contributing

```sh
make test               # go test . and the e2e suite — the open/close tests need tmux
make lint               # gofmt, go vet
make update-snapshots   # after a deliberate UI change: the screen snapshots
OWL_SMOKE=1 go test -run TestFetchSmoke .   # against your real gh, from a repo checkout
```

Three test layers, all hermetic — throwaway git origin, fake `gh`, a
private tmux server, empty `CLAUDE_CONFIG_DIR`; nothing touches your
sessions or config:

- `ui_test.go` drives the Bubble Tea model with teatest and asserts on
  what the frames say.
- `workspace_test.go` runs `open`/`close` for real against tmux and git.
- `e2e/` runs the built binary in a virtual terminal (`x/vttest`) and
  snapshots the screen as JSON — plus a PNG next to it, so a UI change
  shows up as a picture in the PR.

To install a build from a checkout: `make install BIN=~/.local/bin`.
