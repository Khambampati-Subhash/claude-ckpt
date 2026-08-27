// Package transcript reads Claude Code session transcripts and exposes them as
// the message DAG they already are.
//
// A transcript is JSONL. Records that carry a "uuid" form a DAG linked by
// "parentUuid" — exactly a commit graph. Records without a uuid (ai-title,
// last-prompt, queue-operation, custom-title) are session-level metadata that
// sit outside the graph.
//
// The on-disk format is internal to Claude Code and undocumented, so records
// are held as generic maps and round-tripped whole. Only fields this package
// must rewrite are touched; everything else survives a fork untouched.
package transcript

import (
	"bufio"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"strings"
)

// Record is one line of a transcript. The full decoded object is retained so a
// fork can write records back without dropping fields we do not model.
type Record struct {
	fields map[string]any
}

func (r Record) str(key string) string {
	s, _ := r.fields[key].(string)
	return s
}

// UUID is the record's identity in the DAG. Empty for metadata records.
func (r Record) UUID() string { return r.str("uuid") }

// ParentUUID is the record this one descends from. Empty for the root and for
// metadata records.
func (r Record) ParentUUID() string { return r.str("parentUuid") }

// Type is the record kind: user, assistant, attachment, system, ai-title, ...
func (r Record) Type() string { return r.str("type") }

// SessionID is the session the record belongs to. Present on every record,
// including metadata ones.
func (r Record) SessionID() string { return r.str("sessionId") }

// Timestamp is an RFC3339 string, or empty on records that carry no time.
func (r Record) Timestamp() string { return r.str("timestamp") }

// InDAG reports whether the record participates in the message graph. Metadata
// records do not.
func (r Record) InDAG() bool { return r.UUID() != "" }

// IsSidechain reports whether the record belongs to a subagent trace rather
// than the main conversation.
func (r Record) IsSidechain() bool {
	b, _ := r.fields["isSidechain"].(bool)
	return b
}

// CWD returns the working directory recorded on the record, and whether the
// record carries one at all. Metadata records do not.
func (r Record) CWD() (string, bool) {
	s, ok := r.fields["cwd"].(string)
	return s, ok
}

// Role is the message role (user, assistant) or empty if the record carries no
// message.
func (r Record) Role() string {
	msg, ok := r.fields["message"].(map[string]any)
	if !ok {
		return ""
	}
	s, _ := msg["role"].(string)
	return s
}

// Summary renders a one-line preview of the record's content for display.
// Tool calls are shown by name; text is collapsed to a single line.
func (r Record) Summary() string {
	msg, ok := r.fields["message"].(map[string]any)
	if !ok {
		return ""
	}
	switch content := msg["content"].(type) {
	case string:
		return collapse(content)
	case []any:
		var parts []string
		for _, raw := range content {
			block, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			switch block["type"] {
			case "text":
				if s, _ := block["text"].(string); strings.TrimSpace(s) != "" {
					parts = append(parts, collapse(s))
				}
			case "thinking":
				parts = append(parts, "(thinking)")
			case "tool_use":
				name, _ := block["name"].(string)
				parts = append(parts, "["+name+"]")
			case "tool_result":
				parts = append(parts, "[result]")
			}
		}
		return collapse(strings.Join(parts, " "))
	}
	return ""
}

func collapse(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return strings.TrimSpace(s)
}

// withSessionID returns a copy of the record reassigned to a new session. The
// uuid and parentUuid are deliberately preserved: they are globally unique, so
// keeping them makes a fork's lineage traceable back to its parent and keeps
// the replayed prefix byte-identical to the original.
func (r Record) withSessionID(id string) Record {
	clone := make(map[string]any, len(r.fields))
	maps.Copy(clone, r.fields)
	clone["sessionId"] = id
	return Record{fields: clone}
}

// MarshalJSON writes the record back in its original shape.
func (r Record) MarshalJSON() ([]byte, error) { return json.Marshal(r.fields) }

// Transcript is one parsed session file.
type Transcript struct {
	SessionID string
	Path      string
	Records   []Record

	byUUID   map[string]int   // uuid -> index into Records
	children map[string][]int // parentUuid -> indices into Records
}

// Load reads and parses a transcript file.
//
// Malformed lines are skipped rather than fatal: transcripts are appended to by
// a live process, so a trailing partial write is expected and must not make an
// otherwise good session unreadable.
func Load(path string) (*Transcript, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	t := &Transcript{
		Path:     path,
		byUUID:   map[string]int{},
		children: map[string][]int{},
	}

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 32*1024*1024) // tool results get large
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var fields map[string]any
		if err := json.Unmarshal(line, &fields); err != nil {
			continue
		}
		rec := Record{fields: fields}
		if t.SessionID == "" {
			t.SessionID = rec.SessionID()
		}
		i := len(t.Records)
		t.Records = append(t.Records, rec)
		if id := rec.UUID(); id != "" {
			t.byUUID[id] = i
			t.children[rec.ParentUUID()] = append(t.children[rec.ParentUUID()], i)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if len(t.Records) == 0 {
		return nil, fmt.Errorf("%s: empty transcript", path)
	}
	return t, nil
}

// Title returns the best available human label for the session, preferring a
// user-set title over a generated one.
func (t *Transcript) Title() string {
	var ai string
	for _, r := range t.Records {
		switch r.Type() {
		case "custom-title":
			if s, _ := r.fields["customTitle"].(string); s != "" {
				return s
			}
		case "ai-title":
			if s, _ := r.fields["aiTitle"].(string); s != "" {
				ai = s
			}
		}
	}
	return ai
}

// ChildCount reports how many records descend directly from uuid. A count
// above one means the conversation already branches at that point.
func (t *Transcript) ChildCount(uuid string) int { return len(t.children[uuid]) }

// Resolve expands a uuid prefix to the full uuid, the way git accepts short
// SHAs. It fails on an ambiguous prefix rather than guessing.
func (t *Transcript) Resolve(prefix string) (string, error) {
	if _, ok := t.byUUID[prefix]; ok {
		return prefix, nil
	}
	var matches []string
	for id := range t.byUUID {
		if strings.HasPrefix(id, prefix) {
			matches = append(matches, id)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no checkpoint matching %q", prefix)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("checkpoint %q is ambiguous (%d matches)", prefix, len(matches))
	}
}

// Ancestors returns the chain from the root down to and including uuid, in
// conversation order. This is the set of records a fork replays.
func (t *Transcript) Ancestors(uuid string) ([]Record, error) {
	var reversed []Record
	seen := map[string]bool{}
	for cur := uuid; cur != ""; {
		if seen[cur] {
			return nil, fmt.Errorf("cycle in transcript at %s", cur[:8])
		}
		seen[cur] = true
		i, ok := t.byUUID[cur]
		if !ok {
			return nil, fmt.Errorf("checkpoint %s not found", cur)
		}
		reversed = append(reversed, t.Records[i])
		cur = t.Records[i].ParentUUID()
	}
	chain := make([]Record, len(reversed))
	for i, r := range reversed {
		chain[len(reversed)-1-i] = r
	}
	return chain, nil
}

// MainLine walks from the root following the first child at each step, which
// is the path Claude Code renders as "the" conversation. Records that branch
// are still reported via ChildCount so the caller can flag them.
func (t *Transcript) MainLine() []Record {
	var line []Record
	roots := t.children[""]
	if len(roots) == 0 {
		return nil
	}
	for i := roots[0]; ; {
		rec := t.Records[i]
		line = append(line, rec)
		kids := t.children[rec.UUID()]
		if len(kids) == 0 {
			return line
		}
		i = kids[0]
	}
}

// Fork writes the ancestor chain of uuid to a new session file at path, under
// the identity newSessionID. The result is a transcript that Claude Code will
// resume as a conversation ending at the chosen checkpoint.
func (t *Transcript) Fork(uuid, newSessionID, path, title string) (int, error) {
	chain, err := t.Ancestors(uuid)
	if err != nil {
		return 0, err
	}

	// 0600 matches the permissions Claude Code gives its own transcripts. A
	// fork carries the same conversation content as its parent, so it must not
	// be world-readable.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)

	if title != "" {
		marker := Record{fields: map[string]any{
			"type":        "custom-title",
			"customTitle": title,
			"sessionId":   newSessionID,
		}}
		if err := enc.Encode(marker); err != nil {
			return 0, err
		}
	}
	for _, rec := range chain {
		if err := enc.Encode(rec.withSessionID(newSessionID)); err != nil {
			return 0, err
		}
	}
	if err := w.Flush(); err != nil {
		return 0, err
	}
	return len(chain), nil
}
