// Package forest reconstructs fork relationships between sessions.
//
// Claude Code has no concept of one session descending from another — each
// transcript is an independent file. But because a fork preserves the message
// uuids it inherits, two sessions that share uuids share history, and the last
// shared message is the point where they diverged. That is enough to rebuild
// the tree after the fact, with no extra bookkeeping written to disk.
package forest

import (
	"sort"

	"github.com/khambampati-subhash/claude-ckpt/internal/lineage"
	"github.com/khambampati-subhash/claude-ckpt/internal/store"
	"github.com/khambampati-subhash/claude-ckpt/internal/transcript"
)

// Node is one session and its position in the fork tree.
type Node struct {
	Session    store.Session
	Transcript *transcript.Transcript
	Title      string

	// Messages is the number of records in the session's message graph.
	Messages int
	// ForkPoint is the uuid where this session diverged from its parent.
	// Empty for a root.
	ForkPoint string
	// Inherited counts messages carried over from the parent; Own counts
	// messages that exist only in this session.
	Inherited int
	Own       int

	// Inferred is true when parentage was guessed from uuid overlap rather
	// than read from a recorded fork event, and may therefore be wrong.
	Inferred bool

	Parent   *Node
	Children []*Node
}

// Forest is the set of session trees in one project directory.
type Forest struct {
	Roots []*Node
	Nodes []*Node
}

type loaded struct {
	node  *Node
	uuids map[string]struct{}
	line  []transcript.Record // main line, root first
}

// Build loads every session in a project directory and infers which sessions
// are forks of which.
//
// Direction — deciding which of two overlapping sessions is the parent — is a
// heuristic: the child is the one whose first divergent message is newer, since
// a fork is created from history that already exists. Where timestamps tie, the
// session with fewer messages is treated as the child.
func Build(dir string) (*Forest, error) {
	sessions, err := store.Sessions(dir)
	if err != nil {
		return nil, err
	}

	var all []*loaded
	for _, s := range sessions {
		t, err := transcript.Load(s.Path)
		if err != nil {
			continue // unreadable sessions are skipped, not fatal
		}
		line := t.MainLine()
		n := 0
		for _, r := range t.Records {
			if r.InDAG() {
				n++
			}
		}
		title := t.Title()
		if title == "" {
			title = "(untitled)"
		}
		all = append(all, &loaded{
			node:  &Node{Session: s, Transcript: t, Title: title, Messages: n},
			uuids: t.MessageUUIDs(),
			line:  line,
		})
	}

	byID := map[string]*loaded{}
	for _, l := range all {
		byID[l.node.Session.ID] = l
	}
	recorded := lineage.Parents(dir)

	// Prefer recorded parentage; fall back to inference only where a fork
	// predates the lineage file or was made by hand.
	for _, child := range all {
		var best *loaded
		bestShared := 0

		if rec, ok := recorded[child.node.Session.ID]; ok {
			if parent, ok := byID[rec.Parent]; ok && parent != child {
				best = parent
				bestShared = sharedPrefix(child.line, parent.uuids)
				child.node.Inferred = false
			}
		}

		if best == nil {
			for _, cand := range all {
				if cand == child {
					continue
				}
				shared := sharedPrefix(child.line, cand.uuids)
				if shared == 0 || shared <= bestShared {
					continue
				}
				if !isChildOf(child, cand, shared) {
					continue
				}
				best, bestShared = cand, shared
			}
			if best != nil {
				child.node.Inferred = true
			}
		}

		if best != nil && bestShared > 0 {
			child.node.Parent = best.node
			child.node.Inherited = bestShared
			child.node.Own = child.node.Messages - bestShared
			child.node.ForkPoint = child.line[bestShared-1].UUID()
			best.node.Children = append(best.node.Children, child.node)
		}
	}

	f := &Forest{}
	for _, l := range all {
		f.Nodes = append(f.Nodes, l.node)
		if l.node.Parent == nil {
			f.Roots = append(f.Roots, l.node)
		}
	}
	for _, n := range f.Nodes {
		sort.Slice(n.Children, func(i, j int) bool {
			return n.Children[i].Session.Modified < n.Children[j].Session.Modified
		})
	}
	sort.Slice(f.Roots, func(i, j int) bool {
		return f.Roots[i].Session.Modified > f.Roots[j].Session.Modified
	})
	return f, nil
}

// sharedPrefix counts how many leading messages of line also exist in uuids.
// A fork copies a contiguous chain from the root, so overlap always starts at
// the beginning; stopping at the first gap avoids matching coincidental
// overlap further down.
func sharedPrefix(line []transcript.Record, uuids map[string]struct{}) int {
	n := 0
	for _, r := range line {
		if _, ok := uuids[r.UUID()]; !ok {
			break
		}
		n++
	}
	return n
}

// isChildOf reports whether child looks like a fork of cand rather than the
// other way around, given they share the first `shared` messages.
func isChildOf(child, cand *loaded, shared int) bool {
	// The child ends inside the candidate's history: a truncation fork.
	if shared == len(child.line) && len(cand.line) > shared {
		return true
	}
	// Both continue past the fork point — the newer divergence is the fork.
	if shared < len(child.line) && shared < len(cand.line) {
		ct, pt := child.line[shared].Timestamp(), cand.line[shared].Timestamp()
		if ct != pt {
			return ct > pt
		}
	}
	return child.node.Messages < cand.node.Messages
}
