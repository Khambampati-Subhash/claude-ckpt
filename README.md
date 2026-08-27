# ckpt

**Branch a Claude Code conversation like a git branch.**

Fork any past message into a new session that shares everything before it and
diverges after. Explore three approaches to the same problem in parallel, keep
the one that works, throw the rest away.

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

Now resume each one in its own worktree and let them take different paths.

## The insight

Claude Code already stores every session as a DAG. Look at any transcript in
`~/.claude/projects/`:

```json
{"type":"assistant","uuid":"85efe69f-...","parentUuid":"b662c0c0-...","message":{...}}
```

`uuid` and `parentUuid`. That is a commit graph — the same structure git uses.
Real transcripts already contain branch points where one message has two
children.

The data model branches. The CLI only ever renders one line through it and only
resumes at the tip. `ckpt` exposes the graph that is already there.

| git | Claude Code transcript |
|---|---|
| commit | message |
| commit SHA | `uuid` |
| parent commit | `parentUuid` |
| `git checkout -b` at an old commit | fork at a checkpoint |

## Install

```sh
go install github.com/khambampati-subhash/claude-ckpt@latest
```

Or build from source:

```sh
git clone https://github.com/khambampati-subhash/claude-ckpt
cd claude-ckpt && go build -o ckpt .
```

No dependencies beyond the Go standard library. Single static binary.

## Usage

```
ckpt list                          List sessions for the current directory
ckpt list <session>                Show the checkpoint graph for a session
ckpt fork <session>@<checkpoint>   Fork a session at a checkpoint
ckpt fork <session>@<checkpoint> -n 3
                                   Create three independent forks
```

Session and checkpoint IDs abbreviate to any unique prefix, like git SHAs.

Forks are written into the same project directory as the parent, so they appear
in `claude --resume` and in the `/resume` picker, labelled with the checkpoint
they came from.

## Running forks in parallel

A conversation fork is only half the isolation. Three sessions resumed from the
same checkpoint will all edit the same files and overwrite each other — so give
each one its own worktree:

```sh
ckpt fork 8bfb66f4@41241541 -n 3        # note the three session IDs

git worktree add ../try-a && (cd ../try-a && claude --resume <id-1>) &
sleep 2
git worktree add ../try-b && (cd ../try-b && claude --resume <id-2>) &
git worktree add ../try-c && (cd ../try-c && claude --resume <id-3>) &
```

**The `sleep 2` is not incidental.** A prompt cache entry only becomes readable
once the first response starts streaming. Launch all three at once and every one
of them cold-misses the shared prefix, so you pay full input price three times.
Start one, wait for it to begin, then start the rest — the other two read the
prefix from cache at roughly a tenth the price.

Merge by promoting the worktree that won and deleting the others. Merge the
*code*, not the conversations: two divergent histories cannot be spliced into a
coherent one.

## What forking does and does not isolate

**Isolated:** the conversation. Forks are independent files; writing to one
never touches another, and the parent session is never modified.

**Not isolated:** everything outside the transcript. Files on disk, databases,
anything the agent already sent over the network. Pair every fork with a
worktree, and only fork work whose side effects are reversible. An agent that
posts to Slack on branch 2 has done something no `git checkout` undoes.

## How a fork is built

1. Resolve the checkpoint uuid.
2. Walk `parentUuid` from that message back to the root — the ancestor chain.
3. Write those records to a new `<sessionId>.jsonl`, rewriting only `sessionId`.

Every other field is preserved byte for byte, including each message's original
`uuid`. That keeps a fork traceable to its parent, and keeps the replayed prefix
identical to the original so the prompt cache still hits.

## Limitations

- **The transcript format is internal to Claude Code and undocumented.** It can
  change in any release. Parsing is confined to `internal/transcript` so a
  format change is a one-file fix.
- Records that fail to parse are skipped rather than fatal — a live session
  being appended to can end in a partial line.
- Forking follows a single ancestor chain. Where a transcript already branches,
  the fork takes the recorded path to your checkpoint.
- Subagent sidechains are carried along but not separately addressable yet.

## License

MIT
