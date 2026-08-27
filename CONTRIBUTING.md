# Contributing to ckpt

Thanks for taking a look. This is a small, dependency-free Go tool, so getting
started is quick.

## Before anything else: two hard rules

**1. Never commit a real transcript.**

Claude Code transcripts contain private conversation content, absolute paths,
and sometimes credentials that were pasted into a session. Tests use synthetic
fixtures built by hand — see `writeTranscript` in
`internal/transcript/transcript_test.go` for the pattern. If you need a new
record shape, construct it; do not copy one out of `~/.claude/projects/`.

The same applies to documentation. Examples in the README use obviously
synthetic IDs (`1a2b3c4d`, `aa11bb22`). Do not paste real ones from your own
machine, even truncated — they identify real sessions.

`ckpt-graph.html` is gitignored because it embeds conversation excerpts. Leave
it that way.

**2. Never write to Claude Code's own files.**

`ckpt` reads transcripts and creates new ones. It must never modify or delete an
existing session. Parents are opened read-only, and new files are created with
`O_EXCL` so a collision fails rather than overwrites.

For the same reason, fork provenance lives in `~/.ckpt/forks.jsonl` rather than
inside a transcript. The transcript format belongs to Claude Code and is
undocumented; adding our own record type to it might work today and break
`--resume` in the next release. Keep anything ckpt-specific in ckpt's own files.

## Setup

Requires Go 1.25+.

```sh
git clone https://github.com/khambampati-subhash/claude-ckpt
cd claude-ckpt
go install ./cmd/ckpt
```

The binary is named `ckpt` and lands in `$(go env GOPATH)/bin`, which must be on
your `PATH`.

## Before opening a pull request

```sh
gofmt -l .                  # must print nothing
go vet ./...
go test -race -cover ./...
```

CI runs exactly these on Linux and macOS. Both platforms matter: path handling
and file permissions are central to this tool and are precisely what differs
between them.

> **If you have several Go toolchains installed** (gvm, Homebrew, asdf), use one
> consistently. Mixing them produces
> `compile: version "go1.x.y" does not match go tool version "go1.x.z"` on
> `-race` builds. That is a toolchain conflict, not a code error, and
> `go clean -cache` will not fix it.

## Layout

```
cmd/ckpt              CLI entry point and output formatting
internal/transcript   JSONL parsing, the message DAG, forking
internal/store        locating sessions on disk
internal/lineage      recording which session a fork came from
internal/forest       reconstructing the fork tree across sessions
internal/htmlview     the standalone HTML view
```

**All parsing of Claude Code's format is confined to `internal/transcript`.**
That is deliberate: the format is undocumented and can change in any release, so
a break should be a one-file fix. Please don't spread field access into other
packages — add an accessor instead.

## When Claude Code changes its format

This is the most likely reason something breaks. Symptoms are usually
`ckpt list` printing nothing, or sessions missing from `ckpt graph`.

1. Diff a fresh transcript against what `internal/transcript` expects.
2. Fix the parsing there.
3. Add a fixture for the new shape to `transcript_test.go`.
4. Keep the old shape working if you can — people have years of old sessions.

Unknown record types and unknown fields are already tolerated: records are held
as generic maps and round-tripped whole, so a new field survives a fork without
any code change. Preserve that property.

## Testing guidance

New behaviour needs a test. The existing suite is a good guide to the level of
detail expected — each case guards a failure that either already happened or
would be silent:

- Fork invariants: one root, no dangling parents, correct tip, and inherited
  records byte-identical to the parent apart from `sessionId`. That last one is
  what keeps the replayed prefix cacheable; breaking it makes forks silently
  cost full price rather than a cache read.
- Ambiguous ID prefixes must error, never guess. Resolving to the wrong
  checkpoint would fork from a point the user did not choose, with no error.
- Anything that can be empty should be tested empty. A session with no
  displayable messages once serialized as JSON `null` and blanked the whole
  HTML view on load.
- Malformed and blank lines must be skipped rather than fatal: a live session is
  appended to while you read it.

## Commits and pull requests

- One logical change per commit; keep the tree building at every commit.
- Conventional-commit prefixes (`feat:`, `fix:`, `docs:`, `test:`, `chore:`),
  with a scope where it helps (`feat(graph):`).
- Explain **why** in the body, not just what. The diff already says what.
- Describe how you tested it in the PR, especially anything involving real
  Claude Code sessions — CI cannot cover that.

## Good first contributions

- `ckpt run` — fork, create worktrees, and launch branches with the cache
  stagger, in one command.
- `ckpt promote <session>` — merge the winning worktree and clean up the rest.
- `ckpt prune` — delete forks that were never resumed.
- Windows support: path handling and permissions both need work.
- Subagent sidechains are carried along but not separately addressable.

## Scope

`ckpt` branches conversations and shows the resulting tree. It is deliberately
not an agent runner, a Claude Code replacement, or a general transcript editor.
Features that would require writing into Claude Code's own files are out of
scope regardless of how useful they sound.

## License

Contributions are accepted under the MIT license, same as the project.
