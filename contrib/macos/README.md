# macOS glue: one Ghostty window on its own space

Optional. pr-owl itself only needs git, gh and tmux; this directory is
for the setup where every review lives in a single [Ghostty](https://ghostty.org)
window pinned to a [yabai](https://github.com/koekeishiya/yabai) space,
and one hotkey brings it up from anywhere.

```
contrib/macos/
  bin/pr-reviews-focus     find-or-spawn the Ghostty window attached to the review session, focus it
  bin/ws-review            hotkey entry point: session + window + pr-owl popup
  yabai/pr-reviews.yabairc the rule that pins the window to a space
```

Requirements: Ghostty, yabai, jq, tmux ≥ 3.3 (`display-popup -T`).

## Install

```sh
make install-macos BIN=~/.local/bin   # also builds and installs pr-owl into BIN
```

Then in `~/.config/pr-owl/config.yaml` (`pr-owl config init` writes the
template):

```yaml
default_repo: ~/src/your-repo            # what the popup shows when launched from the desktop
hooks:
  after_open: ~/.local/bin/pr-reviews-focus
```

`after_open` runs after every `pr-owl open` (Enter and `f` in the TUI),
so opening a review always ends with the review window in front. Use an
absolute path — the popup's PATH is tmux's, not your shell's.

## Pieces

**`pr-reviews-focus [--cwd DIR]`** finds the Ghostty window whose title
starts with the session name; if there is none it spawns one attached
to the session (`--cwd` is its working directory, default
`$PR_OWL_WORKTREE`), moves it to space `$PR_REVIEWS_SPACE` (9) and
focuses it. Prints `focused <id>` or `spawned <id>`.

**`ws-review`** is what a hotkey runs when Ghostty is *not* in front:
ensure the tmux session exists, `pr-reviews-focus`, then
`tmux display-popup … pr-owl` inside it. It finds `pr-owl` next to
itself, so a hotkey daemon's minimal PATH is fine.

**`yabai/pr-reviews.yabairc`** is the one rule needed; source it from
your yabairc.

## Hotkey recipes

The same key should reach the popup from both sides of the screen:

- Outside Ghostty — [skhd](https://github.com/koekeishiya/skhd):

  ```
  rctrl + ralt + rcmd - r : $HOME/.local/bin/ws-review
  ```

- Inside Ghostty — bind the popup in tmux and have the hotkey send the
  prefix. In `tmux.conf`:

  ```tmux
  bind r display-popup -E -w 88% -h 84% -T ' pr-owl ' pr-owl
  ```

  and a [Karabiner-Elements](https://karabiner-elements.pqrs.org) rule
  that maps Hyper+R to `prefix` + `r` only when Ghostty is frontmost
  (adjust `spacebar`+`left_control` to your prefix):

  ```json
  {
    "description": "Hyper + R → tmux prefix + r (Ghostty only — pr-owl popup)",
    "manipulators": [{
      "type": "basic",
      "from": { "key_code": "r", "modifiers": { "mandatory": ["right_control", "right_option", "right_command"] } },
      "to": [{ "key_code": "spacebar", "modifiers": ["left_control"] }, { "key_code": "r" }],
      "conditions": [{ "type": "frontmost_application_if", "bundle_identifiers": ["^com\\.mitchellh\\.ghostty$"] }]
    }]
  }
  ```

Navigate between open reviews inside the window with tmux's own keys
(`prefix n`/`p`, `prefix w`).
