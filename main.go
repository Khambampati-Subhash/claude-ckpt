// Command ckpt turns Claude Code conversations into a branchable history.
//
// Claude Code already stores each session as a DAG: every message carries a
// uuid and a parentUuid, exactly like a commit graph. ckpt exposes that graph,
// so any message can be treated as a checkpoint.
package main

import (
	"fmt"
	"os"

	"github.com/khambampati-subhash/claude-ckpt/internal/store"
	"github.com/khambampati-subhash/claude-ckpt/internal/transcript"
)

const usage = `ckpt — branch a Claude Code conversation

Usage:
  ckpt list                       List sessions for the current directory
  ckpt list <session>             Show the checkpoint graph for a session

Session and checkpoint IDs may be abbreviated to any unique prefix.

Examples:
  ckpt list
  ckpt list 8bfb66f4
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
