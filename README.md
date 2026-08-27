# ckpt

**Branch a Claude Code conversation like a git branch.**

Go back to any earlier message in a session, split off from it, and carry on
down a different path — without touching the original. Run several of those
paths at once, keep the one that worked, throw the rest away.

```
$ ckpt list 8bfb66f4
session 8bfb66f4  Refactor the auth module

   b662c0c0  user      Refactor the auth module to use middleware
   85efe69f  assistant I'll start by mapping the current request path...
   41241541  user      Actually — should we use middleware or a decorator?
   c9a704a3  assistant Middleware is the better fit here because...

Fork with:  ckpt fork 8bfb66f4@<checkpoint>

$ ckpt fork 8bfb66f4@41241541 -n 3
forked 8bfb66f4 @41241541 -> 64eac53f-6971-4fbe-9fa1-53951eed1d58 (30 messages)
forked 8bfb66f4 @41241541 -> a71b3e02-11cd-4a9e-8b77-2f0e5c9d4411 (30 messages)
forked 8bfb66f4 @41241541 -> f0c8d914-6a25-4f31-9e02-7b1a3d6f8c55 (30 messages)
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
{"type":"assistant","uuid":"85efe69f-…","parentUuid":"b662c0c0-…","message":{…}}
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
a1b2c3d4  Refactor the auth module                     142 messages
e5f6a7b8  Fix segment compaction bug                    88 messages
```

If you see `no Claude Code sessions found`, you are in a directory Claude Code
has never run in. That is the expected message, not a failure.

### 2. Find a checkpoint

```sh
ckpt list a1b2c3d4
```

Every line is a checkpoint you can fork from. A `├─` marks a message that
already has more than one child. Look for a **user** line where you made a
decision worth revisiting:

```
   41241541  user      Actually — should we use middleware or a decorator?
```

### 3. Fork it

```sh
ckpt fork a1b2c3d4@41241541
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
```

### `ckpt graph`

The `git log --graph --all` view — every session in the project and where each
one split off:

```
● 8bfb66f4  Refactor the auth module                    386 msgs
├── ● 570238e7  … [fork @bf2e4f2e]                       51 msgs   ⑂ bf2e4f2e  +0 new
│       ↳ from: Should we use middleware or a decorator?
├── ● 9ae99e52  … [fork @41241541]                       30 msgs   ⑂ 41241541  +12 new
│       ↳ from: Can we cache the token instead?
└── ● d2af8cda  … [fork @79574ea1]                       48 msgs   ⑂ 79574ea1  +3 new

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
same checkpoint will all edit the same files and overwrite each other, so give
each one its own worktree:

```sh
ckpt fork a1b2c3d4@41241541 -n 3        # note the three session IDs

git worktree add ../try-a
git worktree add ../try-b
git worktree add ../try-c

(cd ../try-a && claude --resume <id-1>) &
sleep 2
(cd ../try-b && claude --resume <id-2>) &
(cd ../try-c && claude --resume <id-3>) &
```

**The `sleep 2` is not incidental.** A prompt cache entry only becomes readable
once the first response starts streaming. Launch all three at once and every one
of them misses the shared prefix, so you pay full input price three times. Start
one, let it begin, then start the rest.

When one wins, merge that worktree and `git worktree remove` the others. Merge
the **code**, not the conversations — two divergent histories cannot be spliced
into a coherent one.

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
{"session":"9ae99e52-…","parent":"8bfb66f4-…","forkPoint":"41241541-…","dir":"/…","at":"…"}
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

CI runs formatting, vet, race-enabled tests and a build on Linux and macOS —
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
