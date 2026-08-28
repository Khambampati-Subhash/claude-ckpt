# ckpt

**Branch a Claude Code conversation like a git branch.**

Go back to any earlier message in a session, split off from it, and carry on
down a different path — without touching the original. Run several of those
paths at once, keep the one that worked, throw the rest away.

```
$ ckpt list 1a2b3c4d
session 1a2b3c4d  Refactor the auth module

   aa11bb22  user      Refactor the auth module to use middleware
   cc33dd44  assistant I'll start by mapping the current request path...
   ee55ff66  user      Actually — should we use middleware or a decorator?
   7788aa99  assistant Middleware is the better fit here because...

Fork with:  ckpt fork 1a2b3c4d@<checkpoint>

$ ckpt fork 1a2b3c4d@ee55ff66 -n 3
forked 1a2b3c4d @ee55ff66 -> 5e6f7a8b-1111-4111-8111-111111111111 (30 messages)
forked 1a2b3c4d @ee55ff66 -> 9c1d2e3f-2222-4222-8222-222222222222 (30 messages)
forked 1a2b3c4d @ee55ff66 -> 4b5a6978-3333-4333-8333-333333333333 (30 messages)
```

Three sessions, same history behind them, free to diverge.

---

## Why

### The problem

A Claude Code session is a single line. When you reach a fork in the road —
middleware or decorator, rewrite or patch, this library or that one — you pick
one and find out. If it turns out wrong, you either argue the model back out of
it or start over.

`--resume` returns you to the *end* of a session. `/rewind` takes you back to an
earlier point, but it is **linear**: the path you abandon is gone. There is no
way to hold a point and take several paths from it.

### Why it's possible

Claude Code already stores every session as a DAG. Look at any transcript in
`~/.claude/projects/`:

```json
{"type":"assistant","uuid":"cc33dd44-…","parentUuid":"aa11bb22-…","message":{…}}
```

`uuid` and `parentUuid`. That is a commit graph — the same structure git uses.
Real transcripts already contain branch points where one message has two
children.

**The data model branches. The CLI only ever renders one line through it.**
`ckpt` exposes the graph that is already there.

| git | Claude Code transcript |
|---|---|
| commit | message |
| commit SHA | `uuid` |
| parent commit | `parentUuid` |
| `git checkout -b` at an old commit | fork at a checkpoint |
| branch | a session ending at some message |

### Why it's cheap

Forking is not the same as running three separate sessions. Three separate
sessions mean three cold contexts and three times the input cost. A fork
**shares its parent's prefix**, and prompt caching makes that nearly free:

- cache **read** ≈ 0.1× base input price
- cache **write** ≈ 1.25× (5-minute TTL)

Forking three ways therefore costs roughly `1.25× + 3 × 0.1× ≈ 1.55×` of the
shared history — about half the price of one extra serial attempt. That is what
makes "try three approaches" a reasonable thing to do rather than an
extravagance.

This only holds if the replayed prefix is **byte-identical** to the parent's,
which is why `ckpt` preserves every inherited record exactly and rewrites only
the session ID.

---

## Install

Requires Go 1.25+ and Claude Code.

```sh
go install github.com/khambampati-subhash/claude-ckpt/cmd/ckpt@latest
```

Or from a clone:

```sh
git clone https://github.com/khambampati-subhash/claude-ckpt
cd claude-ckpt
go install ./cmd/ckpt
```

The binary is named `ckpt` and lands in `$(go env GOPATH)/bin`. **That directory
must be on your `PATH`** or the command will not be found:

```sh
export PATH="$PATH:$(go env GOPATH)/bin"   # add to ~/.zshrc to persist
ckpt help
```

No dependencies beyond the Go standard library. Single static binary.

---

## Quick start

### 1. Go to a project you have used Claude Code in

`ckpt` reads sessions for the directory you are standing in, exactly like Claude
Code does.

```sh
cd ~/code/my-project
ckpt list
```

```
1a2b3c4d  Refactor the auth module                     142 messages
2c3d4e5f  Fix segment compaction bug                    88 messages
```

If you see `no Claude Code sessions found`, you are in a directory Claude Code
has never run in. That is the expected message, not a failure.

### 2. Find a checkpoint

```sh
ckpt list 1a2b3c4d
```

Every line is a checkpoint you can fork from. A `├─` marks a message that
already has more than one child. Look for a **user** line where you made a
decision worth revisiting:

```
   ee55ff66  user      Actually — should we use middleware or a decorator?
```

### 3. Fork it

```sh
ckpt fork 1a2b3c4d@ee55ff66
```

IDs abbreviate to any unique prefix, like git SHAs. Ambiguous prefixes are
rejected rather than guessed.

### 4. Resume the fork

```sh
claude --resume <the-new-id>
```

You are back at that moment with everything before it and nothing after it.
Give a different instruction and the conversation takes a different path.

The original session is untouched. You can keep using it in another terminal at
the same time.

---

## Commands

```
ckpt list                          List sessions for the current directory
ckpt list <session>                Show the checkpoint graph for a session
ckpt graph                         Show how sessions fork from one another
ckpt graph --html [path]           Write that graph as a standalone HTML page
ckpt fork <session>@<checkpoint>   Fork a session at a checkpoint
ckpt fork <session>@<checkpoint> -n 3
                                   Create three independent forks
ckpt run <session>@<checkpoint> -n 3
                                   Fork N ways, each in its own git worktree
ckpt promote <session> [--cleanup] Merge a winning branch back
```

### `ckpt graph`

The `git log --graph --all` view — every session in the project and where each
one split off:

```
● 1a2b3c4d  Refactor the auth module                    386 msgs
├── ● 5e6f7a8b  … [fork @ff44aa55]                       51 msgs   ⑂ ff44aa55  +0 new
│       ↳ from: Should we use middleware or a decorator?
├── ● 9c1d2e3f  … [fork @ee55ff66]                       30 msgs   ⑂ ee55ff66  +12 new
│       ↳ from: Can we cache the token instead?
└── ● 4b5a6978  … [fork @dd22ee33]                       48 msgs   ⑂ dd22ee33  +3 new

6 session(s), 4 fork(s)
```

### `ckpt graph --html`

```sh
ckpt graph --html && open ckpt-graph.html
```

The fork tree on the left, the selected session's messages on the right. Click a
message to read it in full; each shows the `ckpt fork` command that would branch
from it, and each session shows its `claude --resume` line ready to copy.

A **single self-contained file** — no CDN, no fonts, no server — that follows
your system light/dark setting. It is written `0600` and embeds excerpts of your
conversations, so treat it like the transcripts it came from. `ckpt-graph.html`
is gitignored for that reason.

---

## Running forks in parallel

A conversation fork is only half the isolation. Three sessions resumed from the
same checkpoint will all edit the same files and overwrite each other, so each
one needs its own worktree. `ckpt run` does both halves:

```sh
ckpt run 1a2b3c4d@ee55ff66 -n 3
```

```
branch 1/3  5e6f7a8b  ../myrepo-ckpt-5e6f7a8b  (30 messages)
branch 2/3  9c1d2e3f  ../myrepo-ckpt-9c1d2e3f  (30 messages)
branch 3/3  4b5a6978  ../myrepo-ckpt-4b5a6978  (30 messages)

Launch them — the sleep matters, see below:

(cd ../myrepo-ckpt-5e6f7a8b && claude --resume 5e6f7a8b-…) &
sleep 2
(cd ../myrepo-ckpt-9c1d2e3f && claude --resume 9c1d2e3f-…) &
(cd ../myrepo-ckpt-4b5a6978 && claude --resume 4b5a6978-…) &
```

Each branch gets a fork, a worktree, and a `ckpt/<id>` branch off your current
HEAD. Paste the block to start them.

**The `sleep 2` is not incidental.** A prompt cache entry only becomes readable
once the first response starts streaming. Launch all three at once and every one
of them misses the shared prefix, so you pay full input price three times. Start
one, let it begin, then start the rest.

### Promoting the winner

```sh
ckpt promote 9c1d2e3f --cleanup
```

Merges that branch into your current branch, then removes the sibling worktrees
and deletes their branches. Add `--force` if a losing branch has uncommitted
changes you are willing to discard. Without `--cleanup` the siblings are left
alone, so you can cherry-pick from them first.

Merge the **code**, not the conversations — two divergent histories cannot be
spliced into a coherent one. The losing conversation forks stay on disk; delete
them from `~/.claude/projects/` if you want them gone.

### Where this works, and where it doesn't

Parallel exploration pays off when success is **mechanically checkable** — the
tests pass, the code compiles, the query returns the right rows. Then picking a
winner is automatic.

For open-ended work ("write a better design doc") you are left reading three
outputs yourself, and three mediocre options are worse than one. Scope it
accordingly.

---

## What is and isn't isolated

**Isolated — the conversation.** Forks are separate files. Writing to one never
touches another, and `ckpt` never modifies an existing session: it opens parents
read-only and creates new files with `O_EXCL`.

**Not isolated — everything else.** Files on disk, databases, branches you
pushed, messages already sent. Claude Code has no idea another session exists.

```
conversations  →  isolated ✅   (separate .jsonl files)
files on disk  →  SHARED   ❌   (same working directory)
```

So: pair every fork with a worktree, and only fork work whose side effects are
reversible. An agent that posts to Slack on branch 2 has done something no
`git checkout` undoes.

**One thing to avoid:** do not resume the *same* session ID in two terminals.
Both processes append to the same file. Forking exists precisely so you don't
have to.

---

## How it works

### Forking

1. Resolve the checkpoint uuid.
2. Walk `parentUuid` from that message back to the root — the ancestor chain.
3. Write those records to a new `<sessionId>.jsonl`, rewriting only `sessionId`.

Every other field is preserved byte for byte, **including each message's
original `uuid`**. That keeps a fork traceable to its parent and keeps the
replayed prefix identical, so the prompt cache still hits.

### Fork lineage

Which session a fork came from **cannot be recovered from transcripts alone.** A
fork inherits its parent's message uuids — but so does every sibling forked at a
later point, so a short fork's history is a perfect prefix of both the original
session *and* its siblings. Overlap genuinely cannot tell them apart.

So `ckpt fork` records the relationship when it happens, in
`~/.ckpt/forks.jsonl`:

```json
{"session":"9c1d2e3f-…","parent":"1a2b3c4d-…","forkPoint":"ee55ff66-…","dir":"/…","at":"…"}
```

That file lives **outside** `~/.claude/`. Claude Code's transcript format is
undocumented and not ours to extend; writing a custom record type into a session
file might work today and break `--resume` in the next release.

Forks made before this existed, or by hand, fall back to uuid-overlap inference
and are labelled `(inferred)` — a guess is never displayed as fact.

---

## Development

```sh
go test ./...            # full suite
go test -race -cover ./...
gofmt -l .               # should print nothing
```

Tests use **synthetic fixtures only**. Real transcripts contain private
conversation content and absolute paths, and neither belongs in a repository.

See [CONTRIBUTING.md](CONTRIBUTING.md) for the full guide. CI runs formatting,
vet, race-enabled tests and a build on Linux and macOS —
path handling and file permissions are central here, and they are exactly what
differs between platforms.

Layout:

```
cmd/ckpt              CLI entry point
internal/transcript   JSONL parsing, the message DAG, forking
internal/store        locating sessions on disk
internal/lineage      recording which session a fork came from
internal/forest       reconstructing the fork tree
internal/htmlview     the standalone HTML view
internal/worktree     git worktrees for parallel branches
```

---

## Limitations

- **The transcript format is internal to Claude Code and undocumented.** It can
  change in any release. Parsing is confined to `internal/transcript` so a
  format change is a one-file fix.
- Records that fail to parse are skipped rather than fatal — a live session
  being appended to can end in a partial line.
- Forking follows a single ancestor chain. Where a transcript already branches,
  the fork takes the recorded path to your checkpoint.
- Subagent sidechains are carried along but not separately addressable yet.
- Merging is manual: promote the winning worktree, delete the rest.

---

## License

MIT
