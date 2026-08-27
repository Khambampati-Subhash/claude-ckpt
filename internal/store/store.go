// Package store locates Claude Code's on-disk session transcripts.
//
// Sessions live at ~/.claude/projects/<slug>/<sessionId>.jsonl, where <slug> is
// the project's working directory with separators flattened to dashes. The
// exact slug rule is internal to Claude Code, so lookup falls back to matching
// each session's recorded cwd when the computed slug misses.
package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/khambampati-subhash/claude-ckpt/internal/transcript"
)

// Session is a transcript file on disk, without its contents loaded.
type Session struct {
	ID       string
	Path     string
	Modified int64
}

// Root returns the directory holding all project transcripts.
func Root() (string, error) {
	if dir := os.Getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		return filepath.Join(dir, "projects"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "projects"), nil
}

// slug flattens a working directory into the directory name Claude Code uses.
// Both path separators and dots become dashes, so a username like
// "first.last" appears as "first-last".
func slug(dir string) string {
	r := strings.NewReplacer(string(filepath.Separator), "-", ".", "-")
	return r.Replace(dir)
}

// ProjectDir returns the transcript directory for a working directory. It
// prefers the computed slug and falls back to scanning for a project whose
// sessions record a matching cwd, so an unexpected slug rule degrades into a
// slower lookup rather than a wrong answer.
func ProjectDir(cwd string) (string, error) {
	root, err := Root()
	if err != nil {
		return "", err
	}
	if dir := filepath.Join(root, slug(cwd)); isDir(dir) {
		return dir, nil
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", root, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		sessions, err := Sessions(dir)
		if err != nil || len(sessions) == 0 {
			continue
		}
		t, err := transcript.Load(sessions[0].Path)
		if err != nil {
			continue
		}
		for _, rec := range t.Records {
			if c, ok := rec.CWD(); ok {
				if c == cwd {
					return dir, nil
				}
				break // one cwd sample per session is enough
			}
		}
	}
	return "", fmt.Errorf("no Claude Code sessions found for %s", cwd)
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// Sessions lists transcripts in a project directory, most recent first.
func Sessions(dir string) ([]Session, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []Session
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, Session{
			ID:       strings.TrimSuffix(name, ".jsonl"),
			Path:     filepath.Join(dir, name),
			Modified: info.ModTime().Unix(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Modified > out[j].Modified })
	return out, nil
}

// Find resolves a session ID or unique ID prefix within a project directory.
func Find(dir, id string) (Session, error) {
	sessions, err := Sessions(dir)
	if err != nil {
		return Session{}, err
	}
	var matches []Session
	for _, s := range sessions {
		if s.ID == id {
			return s, nil
		}
		if strings.HasPrefix(s.ID, id) {
			matches = append(matches, s)
		}
	}
	switch len(matches) {
	case 0:
		return Session{}, fmt.Errorf("no session matching %q in %s", id, dir)
	case 1:
		return matches[0], nil
	default:
		return Session{}, fmt.Errorf("session %q is ambiguous (%d matches)", id, len(matches))
	}
}
