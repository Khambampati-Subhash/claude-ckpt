package forest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/khambampati-subhash/claude-ckpt/internal/lineage"
)

// writeSession writes a straight-line transcript whose messages are the given
// uuids, in order.
func writeSession(t *testing.T, dir, id string, uuids []string) {
	t.Helper()
	var body []byte
	for i, u := range uuids {
		rec := map[string]any{
			"type":      "user",
			"uuid":      u,
			"sessionId": id,
			"cwd":       "/work",
			"timestamp": "2026-01-01T00:00:0" + string(rune('0'+i%10)) + "Z",
			"message":   map[string]any{"role": "user", "content": "message " + u},
		}
		if i == 0 {
			rec["parentUuid"] = nil
		} else {
			rec["parentUuid"] = uuids[i-1]
		}
		line, err := json.Marshal(rec)
		if err != nil {
			t.Fatal(err)
		}
		body = append(body, line...)
		body = append(body, '\n')
	}
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func nodeByID(f *Forest, id string) *Node {
	for _, n := range f.Nodes {
		if n.Session.ID == id {
			return n
		}
	}
	return nil
}

// The case that motivated recording lineage at all.
//
// "short" holds a,b. "long" holds a,b,c. Both are forks of "parent", but
// short's history is also a perfect prefix of long's — so overlap alone cannot
// tell whether short came from parent or from long. Only the recorded fork
// event can.
func TestBuildPrefersRecordedLineage(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CKPT_HOME", t.TempDir())

	writeSession(t, dir, "parent", []string{"a", "b", "c", "d"})
	writeSession(t, dir, "short", []string{"a", "b"})
	writeSession(t, dir, "long", []string{"a", "b", "c"})

	for _, r := range []lineage.Record{
		{Session: "short", Parent: "parent", ForkPoint: "b", Dir: dir},
		{Session: "long", Parent: "parent", ForkPoint: "c", Dir: dir},
	} {
		if err := lineage.Append(r); err != nil {
			t.Fatal(err)
		}
	}

	f, err := Build(dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	for _, id := range []string{"short", "long"} {
		n := nodeByID(f, id)
		if n == nil {
			t.Fatalf("missing node %s", id)
		}
		if n.Parent == nil {
			t.Fatalf("%s has no parent", id)
		}
		if got := n.Parent.Session.ID; got != "parent" {
			t.Errorf("%s parent = %q, want parent", id, got)
		}
		if n.Inferred {
			t.Errorf("%s was inferred despite a recorded fork event", id)
		}
	}

	if got, want := len(f.Roots), 1; got != want {
		t.Errorf("roots = %d, want %d", got, want)
	}
	if root := f.Roots[0]; len(root.Children) != 2 {
		t.Errorf("root children = %d, want 2", len(root.Children))
	}
}

func TestBuildRecordsForkPointAndCounts(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CKPT_HOME", t.TempDir())

	writeSession(t, dir, "parent", []string{"a", "b", "c", "d"})
	writeSession(t, dir, "fork", []string{"a", "b", "x", "y"}) // resumed and continued

	if err := lineage.Append(lineage.Record{
		Session: "fork", Parent: "parent", ForkPoint: "b", Dir: dir,
	}); err != nil {
		t.Fatal(err)
	}

	f, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	n := nodeByID(f, "fork")
	if n.ForkPoint != "b" {
		t.Errorf("ForkPoint = %q, want b", n.ForkPoint)
	}
	if n.Inherited != 2 {
		t.Errorf("Inherited = %d, want 2", n.Inherited)
	}
	if n.Own != 2 {
		t.Errorf("Own = %d, want 2", n.Own)
	}
}

// Forks made before lineage was recorded still need a best guess, but it must
// be labelled as one.
func TestBuildFallsBackToInference(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CKPT_HOME", t.TempDir()) // no lineage recorded

	writeSession(t, dir, "parent", []string{"a", "b", "c", "d"})
	writeSession(t, dir, "fork", []string{"a", "b"})

	f, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	n := nodeByID(f, "fork")
	if n.Parent == nil {
		t.Fatal("fork should have been attached to a parent by inference")
	}
	if got := n.Parent.Session.ID; got != "parent" {
		t.Errorf("inferred parent = %q, want parent", got)
	}
	if !n.Inferred {
		t.Error("a guessed parent must be marked Inferred, never shown as fact")
	}
}

func TestBuildUnrelatedSessionsAreSeparateRoots(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CKPT_HOME", t.TempDir())

	writeSession(t, dir, "one", []string{"a", "b"})
	writeSession(t, dir, "two", []string{"x", "y"})

	f, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(f.Roots), 2; got != want {
		t.Errorf("roots = %d, want %d (sessions sharing no history are unrelated)", got, want)
	}
}

// A lineage record naming a session that is no longer on disk must not attach
// the child to nothing, or drop it from the tree entirely.
func TestBuildIgnoresLineageForDeletedParent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CKPT_HOME", t.TempDir())

	writeSession(t, dir, "fork", []string{"a", "b"})
	if err := lineage.Append(lineage.Record{
		Session: "fork", Parent: "deleted-parent", ForkPoint: "b", Dir: dir,
	}); err != nil {
		t.Fatal(err)
	}

	f, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Nodes) != 1 {
		t.Fatalf("nodes = %d, want 1", len(f.Nodes))
	}
	if f.Nodes[0].Parent != nil {
		t.Error("parent should be nil when the recorded parent no longer exists")
	}
	if len(f.Roots) != 1 {
		t.Errorf("roots = %d, want 1 (an orphan is still shown)", len(f.Roots))
	}
}
