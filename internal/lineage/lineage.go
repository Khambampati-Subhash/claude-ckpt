// Package lineage records which session a fork came from.
//
// Fork parentage cannot be recovered from transcripts alone. A fork inherits
// its parent's message uuids, but so does every other fork taken at a later
// point in the same conversation — so a short fork's history is a prefix of
// both the original session and its siblings, and overlap cannot tell them
// apart. The relationship has to be written down when the fork is made.
//
// Records live in ~/.ckpt/forks.jsonl, deliberately outside Claude Code's own
// directories: nothing here should risk confusing a tool whose on-disk format
// is undocumented and not ours to extend.
package lineage

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Record is one fork event.
type Record struct {
	Session   string `json:"session"`   // the new session's ID
	Parent    string `json:"parent"`    // the session it was forked from
	ForkPoint string `json:"forkPoint"` // message uuid it was forked at
	Dir       string `json:"dir"`       // project directory, for disambiguation
	At        string `json:"at"`        // RFC3339 timestamp

	// Set when the fork was created by `ckpt run`, which pairs each fork with
	// an isolated checkout. Empty for a plain `ckpt fork`.
	Worktree string `json:"worktree,omitempty"`
	Branch   string `json:"branch,omitempty"`
}

// Path returns the lineage file location.
func Path() (string, error) {
	if dir := os.Getenv("CKPT_HOME"); dir != "" {
		return filepath.Join(dir, "forks.jsonl"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ckpt", "forks.jsonl"), nil
}

// Append records a fork. A failure here must never fail the fork itself — the
// transcript is the artifact that matters, and lineage only improves display —
// so the error is returned for the caller to warn about, not to abort on.
func Append(r Record) error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()

	if r.At == "" {
		r.At = time.Now().UTC().Format(time.RFC3339)
	}
	enc := json.NewEncoder(f)
	enc.SetEscapeHTML(false)
	return enc.Encode(r)
}

// Parents maps session ID to parent session ID for one project directory.
// A missing or unreadable lineage file yields an empty map: forks made before
// this was recorded, or by hand, simply fall back to inference.
func Parents(dir string) map[string]Record {
	out := map[string]Record{}
	path, err := Path()
	if err != nil {
		return out
	}
	f, err := os.Open(path)
	if err != nil {
		return out
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 8*1024), 1024*1024)
	for scanner.Scan() {
		var r Record
		if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
			continue
		}
		if r.Dir != "" && r.Dir != dir {
			continue
		}
		if r.Session != "" {
			out[r.Session] = r // later records supersede earlier ones
		}
	}
	return out
}
