# Projects (`owl project`)

```
owl · projects · acme/app                                              updated just now
14 projects · 6 in progress · 1 planned · 7 backlog · 15 issues yours
──────────────────────────────────────────────────────────────────────────────────────
In progress
▸ !!  Sequential Capture redesign   ▓▓▓▓▓▓░░░░  62%  12/127  11 ms  in 12d   Capture + Refine  Stefan
      Emission Categories — Plumbing ▓▓▓▓▓▓▓░░░ 71%   0/6     4 ms           Backstage         Emilio
  !   Bardo Backstage (BACKEND)     ▓░░░░░░░░░   8%   0/917         3d late  Backstage         Stefan  Paused
Planned
      Endpoint Validation w. LLM errors ▓▓▓▓▓░░░ 50%  2/4                    Other             Emilio
Backlog
      Sven v2 — Deterministic harness ▓▓▓░░░░░░ 28%   0/18    6 ms           Other             Stefan
```

The projects you work in, in three sections by status, newest change
first. A row shows the priority, the name, Linear's own progress as ten
cells, **your open issues over every issue the project holds**, the
milestone count, the target date, the initiative above it, the lead,
and the state name only where the section does not already say it — a
status named Paused but typed `started` sits under In progress and says
so.

Priority uses the issue list's own `!!!`/`!!`/`!`/`-`, so the two lists
read alike. The **target date** is read against today — `in 12d`,
`today`, `3d late` — and a date already past is the one thing on the
row that is not dim, because it is the one thing that is not merely
information. The **initiative** is the layer above the project, which
is what makes a long list scannable: half a dozen names covering
everything, rather than fourteen unrelated ones.

Most of these are blank on most rows, and that is the honest rendering
— an empty priority means nobody set one. Four extra columns are also
wider than the row a popup gets, so rather than wrapping, the row
**sheds them as the window narrows**: the lead first, then the
initiative, then the date, and the priority last.

Two fields that look made for this list are not on it, because the data
is not there to justify a column: Linear's project `health`
(onTrack/atRisk/offTrack) and `lastUpdate` were each set on **one
project in twenty-five** in the workspace this was built against. They
are a `projectFields` entry away if your workspace fills them in.

A project in one of `project.dim_statuses` renders **dim throughout** —
name, bar and counts — while staying in the section its status type
puts it in. It is present and not moving, which is a different thing
from being elsewhere. The default is `[Paused]`, and it is a config key
rather than a rule in the code because nothing in the data marks such a
status: Linear has no `paused` status *type*, so a workspace's Paused
is typed `started`, identical to In Progress. Naming it here is the
only honest way to tell them apart, and it keeps working when you
rename it or add another.

A fourth section, **Completed · 7d**, holds what you finished in the
last week — a week rather than the issue list's day, because projects
finish on a different clock and one closed on Monday is still news on
Friday.

Which projects are yours is a union: the ones you lead, the ones you
belong to, and the ones you have an open issue in. That last clause is
not decoration — filtering by membership alone drops the project most
of the work is in.

owl works that third clause out itself rather than asking Linear,
because Linear cannot answer it. Its project filter
`issues: { some: { assignee: { isMe }, state: { type: { nin: … } } } }`
does not conjoin per issue: it matches when *some* issue is yours and
*some* issue is open, which in a project of 920 is always true, and
`and:` inside `some` behaves the same. So owl asks Linear only for
lead-or-member, and derives the rest from the issues it has already
fetched — one 90ms lookup per project that is missing.

`/` filters by name, `o` opens the project in Linear, `y` copies its
name. Off a terminal, `owl project` prints a table.

**`→` on a project row drills into it**, and `←` comes back to the row
you left. The drilled list is the project's own issues, everyone's,
grouped by milestone in the project's order with the unmilestoned last.
The project column goes — every row would name the project you are
standing in — and the assignee column fills in for the issues that are
not yours. Enter still opens a feature workspace, because what the key
does follows the row and not the list.

`owl issue --project <id|name>` is the same list off a terminal —
**everyone's, not your slice** — grouped by milestone in the project's
own order, with the unmilestoned last and the assignee column blank
where the issue is yours, so the gaps are your queue. It takes Linear's
slug or a fragment of the name, the same as `owl project open`.

```
$ owl issue --project sequential
Sequential Capture redesign · 62% · 52 open, 12 yours

M3.5 — Downstream compatibility & activation prerequisites (pre-cutover gate)
  BAR-4782  !    1d  In Review            Define resolves issuer-scoped business rules…
  BAR-4785  !!   1d  Backlog              Flag activities whose PII masking failed…
M5 — Output evaluation
  BAR-2718  -    9w  Duplicate   Emilio   Confirm currency/unit/region/issuer parity
No milestone
  BAR-4160  !!   1d  In Review            Per-tenant captureEngine override…
```

Enter opens the project's **conversation** — the one above the issues.
It is a workspace like the others: a worktree under `worktrees_dir`
named `proj-<slug>`, a window in `project.session`, Claude started in
it on `/owl:project {name}`. Two things differ, and both follow from a
project having no branch of its own:

- The worktree is **detached at the remote's default branch**. git
  refuses to check `main` out twice, and a project's agent has nothing
  to commit: it reads, plans, dispatches with `owl issue start`, and
  reviews what comes back. The `/owl:project` skill says so in as many
  words.
- owl **names the conversation**. It generates a session id on first
  open, keeps it in `$XDG_STATE_HOME/owl/projects.json`, and passes
  `--session-id` then `--resume` — so a project is one conversation
  that resumes as itself, rather than whatever `-c` finds last in that
  worktree. Session ids are machine-local, which is why they live
  beside the Linear token and not in the repo. It passes the project's
  title as `--name` too, on every open, so Claude's prompt box, its
  `/resume` picker and the terminal title read as the project does in
  Linear; a `/rename` inside the conversation lasts until the next open.

`owl project open|start|close <id>` does the same from a terminal,
where `<id>` is Linear's slug, the slug in the workspace's name, or a
fragment of the name — `owl project open sequential`. A fragment
matching two projects is an error naming both rather than a guess.
`close` removes the worktree and the window and keeps the session id,
so the next open resumes the conversation.

A project renamed in Linear keeps its workspace. owl records the name
of the first open beside the session id and looks for that workspace
before one under the new name, because Claude keeps a conversation by
its directory and a new worktree would start it over. The new name
shows in the conversation's title instead
([design/projects.md](design/projects.md#when-a-project-is-renamed-in-linear)).

## Which repositories

`owl pr` holds the repository you are standing in. Say `pr.owners` and
it holds every repository those accounts own instead:

```yaml
pr:
  owners: ["@me", norrbrunn]
```

`user:` and `org:` are synonyms to GitHub's search, so one list covers
people and organisations alike, and `"@me"` keeps your own username out
of the file (quoted, because YAML reserves a leading `@`). `owl pr
--here` narrows to this repository whatever the config says.

It is an allowlist on purpose. A search with no repository qualifier
spans every repository the account can see, so a machine that should
only show personal work would show the company's too — declare the
owners per context and the two can never mix.

A row says which repository it is from, since a pull request number
means nothing without one: #3 is a different pull request in each. The
column appears only where the list spans several — with one, every row
would carry the same answer — and names the repository without its
owner, which the title bar has already given. Opening a row from
another repository is refused rather than guessed at: `open` fetches
`pull/<N>/head` from the repository owl is in.

Linear is reached with a personal API key (Settings → Security &
access), which the config holds as a **reference**, never as a value:

```yaml
linear:
  token: op://Work/Linear API key/credential
  account: work.1password.com   # when more than one account is signed in
  team: BAR
```

`team` is where `owl hoot` files an issue, and — unless `teams` says
otherwise — what makes a key in a branch name mean something. Leave
both unset and `bar-4157-<slug>` is just a branch: a PR of yours opens
a workspace of its own rather than the feature's.

`teams` is that second job on its own, for a workspace where one team
is not the whole story:

```yaml
linear:
  token: op://Work/Linear API key/credential
  team: DEV             # where `owl hoot` files
  teams: [DEV, LIFE]    # which keys in a branch are yours to follow
```

With `team: DEV` alone, a `LIFE-3` branch reads as no issue at all —
no chip on the row, and no way from the pull request back to the
feature it belongs to. The issue list shows LIFE-3 regardless, since
that query is not filtered by team, so the issue is in front of you
and the link to it is quietly missing. The gate stays an allowlist
rather than a rule that rejects things shaped like version numbers: a
key from a team owl has not been told about is missing, never wrong.

## Several workspaces

Linear keeps workspaces apart on purpose — separate accounts, separate
keys, and no query, view or plan that crosses between them. That is
right for a company and a person who are legally two things, and wrong
for the one desk they are both worked from. Written as a list,
`linear:` is every workspace owl reads:

```yaml
linear:
  - name: stefanahman
    token: op://Developer/linear-stefanahman-owl/credential
    teams: [DEV, LIFE]
  - name: norrbrunn
    token: op://Developer/linear-norrbrunn-owl/credential
    teams: [NOR]
```

The lists merge, newest change first, and every row remembers where it
came from: an issue says so already in `DEV-12`, and a project — which
carries a UUID and a name — gets a column that appears only when there
is more than one workspace to tell apart. `owl issue open DEV-12` is
routed by the team its key names; a project, having no key, is asked
of each workspace in turn.

A list asks for a name per workspace and refuses two that claim the
same team key: one names the file a token caches into
(`linear-<name>.token`), the other answers which workspace `DEV-12` is
in. A lone workspace needs neither, is asked for neither, and keeps
caching into `linear.token` rather than being sent back to 1Password
for a key it already has.

A workspace that cannot be read does not blank the list. What answered
is shown and what failed is said where the multiplexer shows messages;
only a set where every workspace failed is an error, because an empty
list and no error would read as "nothing assigned to you", which is a
different thing entirely.

An `op://` reference is read from 1Password the first time it is
needed — owl says why before the prompt appears — and kept in
`~/.local/state/owl/linear.token`, mode 600, the way `gh` keeps a
token on a machine without a keyring: one approval per machine, none
per start. A key Linear refuses is forgotten and read again, once;
delete the file to force that by hand. A cache file others can read is
refused. The other forms: `file://~/.config/owl/linear.token` reads a
file of yours (mode 600, or it is refused), `$VAR` reads the
environment, and anything else is taken as the key itself — the one
form that puts a secret in the config.
