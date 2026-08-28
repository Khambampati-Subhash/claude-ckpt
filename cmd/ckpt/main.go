// Command ckpt turns Claude Code conversations into a branchable history.
//
// Claude Code already stores each session as a DAG: every message carries a
// uuid and a parentUuid, exactly like a commit graph. ckpt exposes that graph,
// so any message can be treated as a checkpoint and forked into an independent
// session that shares the parent's history but diverges from that point on.
package main

import (
	"crypto/rand"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/khambampati-subhash/claude-ckpt/internal/forest"
	"github.com/khambampati-subhash/claude-ckpt/internal/htmlview"
	"github.com/khambampati-subhash/claude-ckpt/internal/lineage"
	"github.com/khambampati-subhash/claude-ckpt/internal/store"
	"github.com/khambampati-subhash/claude-ckpt/internal/transcript"
	"github.com/khambampati-subhash/claude-ckpt/internal/worktree"
)

const usage = `ckpt — branch a Claude Code conversation

Usage:
  ckpt list                       List sessions for the current directory
  ckpt list <session>             Show the checkpoint graph for a session
  ckpt graph                      Show how sessions fork from one another
  ckpt graph --html [path]        Write that graph as a standalone HTML page
  ckpt fork <session>@<checkpoint> [-n N]
                                  Fork a session at a checkpoint
  ckpt run <session>@<checkpoint> [-n N] [--base DIR]
                                  Fork N ways, each in its own git worktree
  ckpt promote <session> [--cleanup] [--force]
                                  Merge a winning branch back
  ckpt abandon <session> [--run] [--delete-sessions] [--force]
                                  Drop a branch's worktree, keep the conversation

Session and checkpoint IDs may be abbreviated to any unique prefix.

Examples:
  ckpt list
  ckpt list 1a2b3c4d
  ckpt graph
  ckpt fork 1a2b3c4d@bb00cc11 -n 3
  ckpt run 1a2b3c4d@bb00cc11 -n 3
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "ckpt:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		fmt.Print(usage)
		return nil
	}
	switch args[0] {
	case "list", "ls":
		return cmdList(args[1:])
	case "graph":
		return cmdGraph(args[1:])
	case "fork":
		return cmdFork(args[1:])
	case "run":
		return cmdRun(args[1:])
	case "promote":
		return cmdPromote(args[1:])
	case "abandon":
		return cmdAbandon(args[1:])
	case "help", "-h", "--help":
		fmt.Print(usage)
		return nil
	default:
		return fmt.Errorf("unknown command %q (try `ckpt help`)", args[0])
	}
}

// projectDir resolves the transcript directory for the current directory.
func projectDir() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return store.ProjectDir(cwd)
}

func cmdList(args []string) error {
	dir, err := projectDir()
	if err != nil {
		return err
	}
	if len(args) == 0 {
		return listSessions(dir)
	}
	return listCheckpoints(dir, args[0])
}

func listSessions(dir string) error {
	sessions, err := store.Sessions(dir)
	if err != nil {
		return err
	}
	if len(sessions) == 0 {
		return fmt.Errorf("no sessions in %s", dir)
	}
	for _, s := range sessions {
		t, err := transcript.Load(s.Path)
		if err != nil {
			continue
		}
		title := t.Title()
		if title == "" {
			title = "(untitled)"
		}
		fmt.Printf("%s  %-44s %3d messages\n", short(s.ID), truncate(title, 44), countDAG(t))
	}
	return nil
}

func listCheckpoints(dir, id string) error {
	session, err := store.Find(dir, id)
	if err != nil {
		return err
	}
	t, err := transcript.Load(session.Path)
	if err != nil {
		return err
	}

	title := t.Title()
	if title == "" {
		title = "(untitled)"
	}
	fmt.Printf("session %s  %s\n\n", short(t.SessionID), title)

	for _, rec := range t.MainLine() {
		if rec.Type() == "attachment" || rec.IsSidechain() {
			continue
		}
		label := rec.Role()
		if label == "" {
			label = rec.Type()
		}
		summary := rec.Summary()
		if summary == "" {
			continue
		}
		marker := "  "
		if n := t.ChildCount(rec.UUID()); n > 1 {
			marker = "├─" // already branches here
		}
		fmt.Printf("%s %s  %-9s %s\n", marker, short(rec.UUID()), label, truncate(summary, 68))
	}

	fmt.Printf("\nFork with:  ckpt fork %s@<checkpoint>\n", short(t.SessionID))
	return nil
}

// cmdGraph renders every session in the project as a fork tree, the way
// `git log --graph --all` renders branches.
func cmdGraph(args []string) error {
	out := ""
	asHTML := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--html":
			asHTML = true
			// An optional path may follow, but not another flag.
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				out = args[i+1]
				i++
			}
		default:
			return fmt.Errorf("unknown flag %q (graph accepts --html [path])", args[i])
		}
	}

	dir, err := projectDir()
	if err != nil {
		return err
	}
	f, err := forest.Build(dir)
	if err != nil {
		return err
	}
	if len(f.Nodes) == 0 {
		return fmt.Errorf("no sessions in %s", dir)
	}

	if asHTML {
		if out == "" {
			out = "ckpt-graph.html"
		}
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		// 0600: the page embeds conversation excerpts, same sensitivity as the
		// transcripts it is built from.
		file, err := os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		defer file.Close()
		if err := htmlview.Render(f, cwd, file); err != nil {
			return err
		}
		abs, err := filepath.Abs(out)
		if err != nil {
			abs = out
		}
		fmt.Printf("wrote %s\n", abs)
		fmt.Printf("open with:  open %s\n", abs)
		return nil
	}

	forks := 0
	for _, n := range f.Nodes {
		if n.Parent != nil {
			forks++
		}
	}

	for i, root := range f.Roots {
		if i > 0 {
			fmt.Println()
		}
		printNode(root, "", true, true)
	}

	fmt.Printf("\n%d session(s), %d fork(s)\n", len(f.Nodes), forks)
	if forks == 0 {
		fmt.Println("No forks yet — try:  ckpt fork <session>@<checkpoint>")
	}
	return nil
}

// printNode draws one session and recurses into its forks, using the same box
// characters as a directory tree.
func printNode(n *forest.Node, prefix string, isLast, isRoot bool) {
	connector := ""
	if !isRoot {
		if isLast {
			connector = "└── "
		} else {
			connector = "├── "
		}
	}

	line := fmt.Sprintf("%s%s● %s  %-42s %4d msgs",
		prefix, connector, short(n.Session.ID), truncate(n.Title, 42), n.Messages)
	if n.Parent != nil {
		line += fmt.Sprintf("   ⑂ %s  +%d new", short(n.ForkPoint), n.Own)
		if n.Inferred {
			line += "  (inferred)"
		}
	}
	fmt.Println(line)

	// Show what the fork point actually was, so the split is readable.
	childPrefix := prefix
	if !isRoot {
		if isLast {
			childPrefix += "    "
		} else {
			childPrefix += "│   "
		}
	}
	if n.Parent != nil {
		if rec, ok := n.Parent.Transcript.Get(n.ForkPoint); ok {
			if s := rec.Summary(); s != "" {
				fmt.Printf("%s    ↳ from: %s\n", childPrefix, truncate(s, 60))
			}
		}
	}

	for i, c := range n.Children {
		printNode(c, childPrefix, i == len(n.Children)-1, false)
	}
}

// cmdRun forks a checkpoint several ways and gives each fork an isolated
// checkout, so the branches can run at the same time without overwriting each
// other's files.
func cmdRun(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: ckpt run <session>@<checkpoint> [-n N] [--base DIR]")
	}
	target := args[0]
	count := 2 // running one branch in isolation is just `ckpt fork`
	base := ""
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "-n":
			if i+1 >= len(args) {
				return fmt.Errorf("-n needs a number")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n < 1 {
				return fmt.Errorf("-n must be a positive number, got %q", args[i+1])
			}
			count, i = n, i+1
		case "--base":
			if i+1 >= len(args) {
				return fmt.Errorf("--base needs a directory")
			}
			base, i = args[i+1], i+1
		default:
			return fmt.Errorf("unknown flag %q", args[i])
		}
	}

	sessionRef, checkpointRef, ok := strings.Cut(target, "@")
	if !ok {
		return fmt.Errorf("expected <session>@<checkpoint>, got %q", target)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	repo, err := worktree.Open(cwd)
	if err != nil {
		return fmt.Errorf("%w\n\nckpt run needs a git repository so each branch gets its own worktree.\nUse `ckpt fork` if you only want the conversation forks", err)
	}
	// Worktrees branch from HEAD, so uncommitted work is left behind. Better to
	// say so now than to have someone discover it three branches later.
	if clean, err := repo.IsClean(); err == nil && !clean {
		fmt.Fprintln(os.Stderr, "ckpt: warning: uncommitted changes will not be carried into the worktrees")
	}
	if base == "" {
		base = filepath.Dir(repo.Root)
	}

	dir, err := projectDir()
	if err != nil {
		return err
	}
	session, err := store.Find(dir, sessionRef)
	if err != nil {
		return err
	}
	t, err := transcript.Load(session.Path)
	if err != nil {
		return err
	}
	checkpoint, err := t.Resolve(checkpointRef)
	if err != nil {
		return err
	}

	parentTitle := t.Title()
	if parentTitle == "" {
		parentTitle = short(t.SessionID)
	}
	repoName := filepath.Base(repo.Root)

	type branch struct {
		session  string
		worktree string
	}
	var created []branch

	for i := 1; i <= count; i++ {
		newID, err := uuidV4()
		if err != nil {
			return err
		}
		id := short(newID)
		path := filepath.Join(base, fmt.Sprintf("%s-ckpt-%s", repoName, id))
		branchName := "ckpt/" + id

		title := fmt.Sprintf("%s [run %d/%d @%s]", parentTitle, i, count, short(checkpoint))
		forkPath := filepath.Join(dir, newID+".jsonl")
		n, err := t.Fork(checkpoint, newID, forkPath, title)
		if err != nil {
			return err
		}
		wt, err := repo.Add(path, branchName)
		if err != nil {
			// The conversation fork already exists; leaving it is harmless and
			// keeps the failure recoverable by hand.
			return fmt.Errorf("forked %s but could not create its worktree: %w", id, err)
		}
		if err := lineage.Append(lineage.Record{
			Session:   newID,
			Parent:    t.SessionID,
			ForkPoint: checkpoint,
			Dir:       dir,
			Worktree:  wt.Path,
			Branch:    wt.Branch,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "ckpt: warning: could not record lineage: %v\n", err)
		}
		fmt.Printf("branch %d/%d  %s  %s  (%d messages)\n", i, count, id, wt.Path, n)
		created = append(created, branch{session: newID, worktree: wt.Path})
	}

	fmt.Printf("\nLaunch them — the sleep matters, see below:\n\n")
	for i, b := range created {
		if i == 1 {
			fmt.Println("sleep 2")
		}
		fmt.Printf("(cd %s && claude --resume %s) &\n", b.worktree, b.session)
	}
	fmt.Printf("\nThe first branch warms the shared prefix into cache; the rest then read it\n")
	fmt.Printf("at about a tenth the price. Launch all at once and every one pays full price.\n")
	fmt.Printf("\nWhen one wins:   ckpt promote %s --cleanup\n", short(created[0].session))
	fmt.Printf("If none do:      ckpt abandon %s --run\n", short(created[0].session))
	fmt.Printf("                 (drops the worktrees; the conversations stay usable)\n")
	return nil
}

// cmdAbandon drops the git side of a run — worktree and branch — while leaving
// the conversation forks alone, so they carry on as ordinary sessions.
func cmdAbandon(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: ckpt abandon <session> [--run] [--delete-sessions] [--force]")
	}
	ref := args[0]
	wholeRun, deleteSessions, force := false, false, false
	for _, a := range args[1:] {
		switch a {
		case "--run":
			wholeRun = true
		case "--delete-sessions":
			deleteSessions = true
		case "--force":
			force = true
		default:
			return fmt.Errorf("unknown flag %q", a)
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	repo, err := worktree.Open(cwd)
	if err != nil {
		return err
	}
	dir, err := projectDir()
	if err != nil {
		return err
	}

	records := lineage.Parents(dir)
	var target lineage.Record
	matches := 0
	for id, rec := range records {
		if rec.Worktree == "" {
			continue
		}
		if strings.HasPrefix(id, ref) {
			target, matches = rec, matches+1
		}
	}
	switch matches {
	case 0:
		return fmt.Errorf("no run branch matching %q\n\nOnly `ckpt run` branches have worktrees. Plain `ckpt fork` sessions\nhave nothing to abandon — they are already ordinary conversations", ref)
	case 1:
	default:
		return fmt.Errorf("%q is ambiguous (%d matches)", ref, matches)
	}

	// Collect what to drop: just this branch, or every branch from the same run.
	victims := []lineage.Record{target}
	if wholeRun {
		victims = nil
		for _, rec := range records {
			if rec.Worktree != "" && rec.Parent == target.Parent && rec.ForkPoint == target.ForkPoint {
				victims = append(victims, rec)
			}
		}
	}

	onto, err := repo.CurrentBranch()
	if err != nil {
		return err
	}

	// Refuse to silently destroy commits that exist nowhere else.
	if !force {
		blocked := false
		for _, v := range victims {
			commits, err := repo.UnmergedCommits(v.Branch, onto)
			if err != nil || len(commits) == 0 {
				continue
			}
			blocked = true
			fmt.Fprintf(os.Stderr, "%s has %d commit(s) not in %s:\n", v.Branch, len(commits), onto)
			for _, c := range commits {
				fmt.Fprintf(os.Stderr, "    %s\n", c)
			}
		}
		if blocked {
			return fmt.Errorf("refusing to discard unmerged work\n\n" +
				"Keep it:      ckpt promote <session>      (merge it first)\n" +
				"Discard it:   ckpt abandon <session> --force")
		}
	}

	dropped := 0
	for _, v := range victims {
		if !repo.Exists(v.Worktree) {
			continue
		}
		if err := repo.Remove(v.Worktree, false, force); err != nil {
			fmt.Fprintf(os.Stderr, "ckpt: could not remove %s: %v\n", v.Worktree, err)
			fmt.Fprintln(os.Stderr, "      re-run with --force to discard uncommitted changes")
			continue
		}
		fmt.Printf("abandoned %s\n", short(v.Session))
		fmt.Printf("  removed worktree  %s\n", v.Worktree)
		fmt.Printf("  deleted branch    %s\n", v.Branch)

		if deleteSessions {
			path := filepath.Join(dir, v.Session+".jsonl")
			if err := os.Remove(path); err != nil {
				fmt.Fprintf(os.Stderr, "  could not delete conversation: %v\n", err)
			} else {
				fmt.Printf("  deleted conversation %s\n", short(v.Session))
			}
		} else {
			fmt.Printf("  kept conversation %s — resume it any time:\n", short(v.Session))
			fmt.Printf("      claude --resume %s\n", v.Session)
		}
		dropped++
	}

	if dropped == 0 {
		return fmt.Errorf("nothing to abandon (worktrees already gone?)")
	}
	if !deleteSessions {
		fmt.Printf("\n%d branch(es) abandoned. Their conversations are now ordinary sessions —\n", dropped)
		fmt.Println("they behave exactly like anything made with `ckpt fork`, and `ckpt graph`")
		fmt.Println("still shows where they came from.")
	}
	return nil
}

// cmdPromote merges a winning branch back and optionally discards its siblings.
func cmdPromote(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: ckpt promote <session> [--cleanup] [--force]")
	}
	ref := args[0]
	cleanup, force := false, false
	for _, a := range args[1:] {
		switch a {
		case "--cleanup":
			cleanup = true
		case "--force":
			force = true
		default:
			return fmt.Errorf("unknown flag %q", a)
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	repo, err := worktree.Open(cwd)
	if err != nil {
		return err
	}
	dir, err := projectDir()
	if err != nil {
		return err
	}

	records := lineage.Parents(dir)
	var winner lineage.Record
	matches := 0
	for id, rec := range records {
		if rec.Worktree == "" {
			continue // a plain fork, never given a checkout
		}
		if strings.HasPrefix(id, ref) {
			winner, matches = rec, matches+1
		}
	}
	switch matches {
	case 0:
		return fmt.Errorf("no run branch matching %q (only `ckpt run` branches can be promoted)", ref)
	case 1:
	default:
		return fmt.Errorf("%q is ambiguous (%d matches)", ref, matches)
	}

	onto, err := repo.CurrentBranch()
	if err != nil {
		return err
	}
	msg := fmt.Sprintf("Merge ckpt branch %s\n\nPromoted from session %s, forked at %s.",
		winner.Branch, short(winner.Session), short(winner.ForkPoint))
	if err := repo.Merge(winner.Branch, msg); err != nil {
		return err
	}
	fmt.Printf("merged %s into %s\n", winner.Branch, onto)

	if !cleanup {
		fmt.Println("\nSiblings left in place. Re-run with --cleanup to discard them.")
		return nil
	}

	removed := 0
	for id, rec := range records {
		if rec.Worktree == "" || id == winner.Session {
			continue
		}
		// Only siblings from the same run: same parent, same fork point.
		if rec.Parent != winner.Parent || rec.ForkPoint != winner.ForkPoint {
			continue
		}
		if !repo.Exists(rec.Worktree) {
			continue
		}
		if err := repo.Remove(rec.Worktree, false, force); err != nil {
			fmt.Fprintf(os.Stderr, "ckpt: could not remove %s: %v\n", rec.Worktree, err)
			fmt.Fprintf(os.Stderr, "      it may have uncommitted changes; re-run with --force to discard them\n")
			continue
		}
		fmt.Printf("removed %s (%s)\n", rec.Worktree, rec.Branch)
		removed++
	}
	fmt.Printf("\n%d sibling worktree(s) removed. The winning worktree is kept.\n", removed)
	fmt.Println("Their conversation forks are left on disk; delete them from ~/.claude/projects if you want them gone.")
	return nil
}

func cmdFork(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: ckpt fork <session>@<checkpoint> [-n N]")
	}
	target := args[0]
	count := 1
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "-n":
			if i+1 >= len(args) {
				return fmt.Errorf("-n needs a number")
			}
			n, err := strconv.Atoi(args[i+1])
			if err != nil || n < 1 {
				return fmt.Errorf("-n must be a positive number, got %q", args[i+1])
			}
			count = n
			i++
		default:
			return fmt.Errorf("unknown flag %q", args[i])
		}
	}

	sessionRef, checkpointRef, ok := strings.Cut(target, "@")
	if !ok {
		return fmt.Errorf("expected <session>@<checkpoint>, got %q", target)
	}

	dir, err := projectDir()
	if err != nil {
		return err
	}
	session, err := store.Find(dir, sessionRef)
	if err != nil {
		return err
	}
	t, err := transcript.Load(session.Path)
	if err != nil {
		return err
	}
	checkpoint, err := t.Resolve(checkpointRef)
	if err != nil {
		return err
	}

	parentTitle := t.Title()
	if parentTitle == "" {
		parentTitle = short(t.SessionID)
	}

	for i := 1; i <= count; i++ {
		newID, err := uuidV4()
		if err != nil {
			return err
		}
		title := fmt.Sprintf("%s [fork @%s]", parentTitle, short(checkpoint))
		if count > 1 {
			title = fmt.Sprintf("%s [fork %d/%d @%s]", parentTitle, i, count, short(checkpoint))
		}
		path := filepath.Join(dir, newID+".jsonl")
		n, err := t.Fork(checkpoint, newID, path, title)
		if err != nil {
			return err
		}
		// Parentage cannot be recovered from the transcript later, so record it
		// now. A failure here only degrades `ckpt graph` to inference — the fork
		// itself is already written, so warn rather than fail.
		if err := lineage.Append(lineage.Record{
			Session:   newID,
			Parent:    t.SessionID,
			ForkPoint: checkpoint,
			Dir:       dir,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "ckpt: warning: could not record lineage: %v\n", err)
		}
		fmt.Printf("forked %s @%s -> %s (%d messages)\n", short(t.SessionID), short(checkpoint), newID, n)
	}

	fmt.Println("\nResume a fork with:  claude --resume <id>")
	if count > 1 {
		fmt.Println("Stagger parallel launches ~2s apart so the shared prefix is served from cache.")
	}
	return nil
}

// countDAG reports how many records participate in the message graph.
func countDAG(t *transcript.Transcript) int {
	n := 0
	for _, r := range t.Records {
		if r.InDAG() {
			n++
		}
	}
	return n
}

func short(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n < 4 {
		return s[:n]
	}
	return s[:n-3] + "..."
}

// uuidV4 generates a random UUID for a new session, matching the format Claude
// Code uses for session filenames.
func uuidV4() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}
