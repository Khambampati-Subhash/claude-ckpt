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
)

const usage = `ckpt — branch a Claude Code conversation

Usage:
  ckpt list                       List sessions for the current directory
  ckpt list <session>             Show the checkpoint graph for a session
  ckpt graph                      Show how sessions fork from one another
  ckpt graph --html [path]        Write that graph as a standalone HTML page
  ckpt fork <session>@<checkpoint> [-n N]
                                  Fork a session at a checkpoint

Session and checkpoint IDs may be abbreviated to any unique prefix.

Examples:
  ckpt list
  ckpt list 8bfb66f4
  ckpt graph
  ckpt fork 8bfb66f4@6e3f42cb -n 3
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
