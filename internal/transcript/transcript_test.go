package transcript

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// msg describes one synthetic record. Fixtures are built by hand rather than
// copied from real sessions: transcripts contain private conversation content
// and absolute paths, and neither belongs in a repository.
type msg struct {
	uuid    string
	parent  string // "" means root
	role    string // user or assistant
	text    string
	tool    string // when set, content is a tool_use block instead of text
	extra   map[string]any
	kind    string // record type; defaults to role
	noUUID  bool   // metadata record, outside the DAG
	rawLine string // written verbatim, for malformed-input tests
	blank   bool   // emit an empty line
}

func writeTranscript(t *testing.T, path, sessionID string, msgs []msg) {
	t.Helper()
	var b strings.Builder
	for _, m := range msgs {
		if m.blank {
			b.WriteString("\n")
			continue
		}
		if m.rawLine != "" {
			b.WriteString(m.rawLine + "\n")
			continue
		}
		rec := map[string]any{"sessionId": sessionID, "timestamp": "2026-01-01T00:00:00Z"}
		kind := m.kind
		if kind == "" {
			kind = m.role
		}
		rec["type"] = kind
		if !m.noUUID {
			rec["uuid"] = m.uuid
			if m.parent == "" {
				rec["parentUuid"] = nil
			} else {
				rec["parentUuid"] = m.parent
			}
			rec["cwd"] = "/work"
			rec["isSidechain"] = false
		}
		switch {
		case m.tool != "":
			rec["message"] = map[string]any{"role": m.role, "content": []any{
				map[string]any{"type": "tool_use", "name": m.tool, "input": map[string]any{}},
			}}
		case m.text != "":
			rec["message"] = map[string]any{"role": m.role, "content": m.text}
		}
		maps.Copy(rec, m.extra)
		line, err := json.Marshal(rec)
		if err != nil {
			t.Fatalf("marshal fixture: %v", err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
}

// linear builds a straight-line conversation: a -> b -> c -> d.
func linear(t *testing.T, dir, sessionID string) string {
	t.Helper()
	path := filepath.Join(dir, sessionID+".jsonl")
	writeTranscript(t, path, sessionID, []msg{
		{kind: "ai-title", noUUID: true, extra: map[string]any{"aiTitle": "Generated"}},
		{uuid: "aaaaaaaa-1", role: "user", text: "first question"},
		{uuid: "bbbbbbbb-2", parent: "aaaaaaaa-1", role: "assistant", text: "first answer"},
		{uuid: "cccccccc-3", parent: "bbbbbbbb-2", role: "user", text: "second question"},
		{uuid: "dddddddd-4", parent: "cccccccc-3", role: "assistant", text: "second answer"},
	})
	return path
}

func load(t *testing.T, path string) *Transcript {
	t.Helper()
	tr, err := Load(path)
	if err != nil {
		t.Fatalf("Load(%s): %v", path, err)
	}
	return tr
}

func TestLoadReadsDAGAndMetadata(t *testing.T) {
	tr := load(t, linear(t, t.TempDir(), "sess-1"))

	if got, want := tr.SessionID, "sess-1"; got != want {
		t.Errorf("SessionID = %q, want %q", got, want)
	}
	if got, want := len(tr.Records), 5; got != want {
		t.Errorf("records = %d, want %d", got, want)
	}
	inDAG := 0
	for _, r := range tr.Records {
		if r.InDAG() {
			inDAG++
		}
	}
	if got, want := inDAG, 4; got != want {
		t.Errorf("DAG records = %d, want %d (metadata must stay out of the graph)", got, want)
	}
	if got, want := tr.Title(), "Generated"; got != want {
		t.Errorf("Title() = %q, want %q", got, want)
	}
}

func TestTitlePrefersCustomOverGenerated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	writeTranscript(t, path, "s", []msg{
		{kind: "ai-title", noUUID: true, extra: map[string]any{"aiTitle": "Generated"}},
		{kind: "custom-title", noUUID: true, extra: map[string]any{"customTitle": "Mine"}},
		{uuid: "a", role: "user", text: "hi"},
	})
	if got, want := load(t, path).Title(), "Mine"; got != want {
		t.Errorf("Title() = %q, want %q", got, want)
	}
}

// A live session is appended to while being read, so a trailing partial write
// must not make an otherwise good transcript unreadable.
func TestLoadSkipsMalformedLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	writeTranscript(t, path, "s", []msg{
		{uuid: "a", role: "user", text: "one"},
		{rawLine: `{"type":"assistant","uuid":"b","parentUuid":"a"`}, // truncated
		{uuid: "c", parent: "a", role: "assistant", text: "two"},
		{blank: true},
	})
	tr := load(t, path)
	if got, want := len(tr.Records), 2; got != want {
		t.Fatalf("records = %d, want %d", got, want)
	}
	if _, ok := tr.Get("c"); !ok {
		t.Error("record after the malformed line was dropped")
	}
}

func TestLoadRejectsEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.jsonl")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("Load on an empty file should fail")
	}
}

func TestResolve(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	writeTranscript(t, path, "s", []msg{
		{uuid: "abc111", role: "user", text: "one"},
		{uuid: "abc222", parent: "abc111", role: "assistant", text: "two"},
		{uuid: "xyz333", parent: "abc222", role: "user", text: "three"},
	})
	tr := load(t, path)

	t.Run("unique prefix", func(t *testing.T) {
		got, err := tr.Resolve("xyz")
		if err != nil || got != "xyz333" {
			t.Errorf("Resolve(xyz) = %q, %v; want xyz333, nil", got, err)
		}
	})
	t.Run("exact match", func(t *testing.T) {
		got, err := tr.Resolve("abc111")
		if err != nil || got != "abc111" {
			t.Errorf("Resolve(abc111) = %q, %v", got, err)
		}
	})
	// Silently resolving to the wrong checkpoint would be the worst possible
	// failure: you would fork from a point you did not choose.
	t.Run("ambiguous prefix is an error", func(t *testing.T) {
		if _, err := tr.Resolve("abc"); err == nil {
			t.Error("ambiguous prefix must error, not guess")
		}
	})
	t.Run("unknown prefix is an error", func(t *testing.T) {
		if _, err := tr.Resolve("nope"); err == nil {
			t.Error("unknown prefix must error")
		}
	})
}

func TestAncestorsReturnsRootFirst(t *testing.T) {
	tr := load(t, linear(t, t.TempDir(), "s"))
	chain, err := tr.Ancestors("cccccccc-3")
	if err != nil {
		t.Fatalf("Ancestors: %v", err)
	}
	want := []string{"aaaaaaaa-1", "bbbbbbbb-2", "cccccccc-3"}
	if len(chain) != len(want) {
		t.Fatalf("chain length = %d, want %d", len(chain), len(want))
	}
	for i, uuid := range want {
		if chain[i].UUID() != uuid {
			t.Errorf("chain[%d] = %s, want %s", i, chain[i].UUID(), uuid)
		}
	}
}

func TestAncestorsUnknownCheckpoint(t *testing.T) {
	tr := load(t, linear(t, t.TempDir(), "s"))
	if _, err := tr.Ancestors("missing"); err == nil {
		t.Error("Ancestors on an unknown uuid must error")
	}
}

func TestMainLineAndBranchDetection(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	// b and c both descend from a: a real transcript branch.
	writeTranscript(t, path, "s", []msg{
		{uuid: "a", role: "user", text: "root"},
		{uuid: "b", parent: "a", role: "assistant", text: "first child"},
		{uuid: "c", parent: "a", role: "assistant", text: "second child"},
		{uuid: "d", parent: "b", role: "user", text: "under first"},
	})
	tr := load(t, path)

	if got, want := tr.ChildCount("a"), 2; got != want {
		t.Errorf("ChildCount(a) = %d, want %d", got, want)
	}
	line := tr.MainLine()
	got := make([]string, len(line))
	for i, r := range line {
		got[i] = r.UUID()
	}
	want := []string{"a", "b", "d"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("MainLine = %v, want %v (must follow the first child)", got, want)
	}
	if tr.Tip() != "d" {
		t.Errorf("Tip = %q, want d", tr.Tip())
	}
}

func TestForkProducesValidTranscript(t *testing.T) {
	dir := t.TempDir()
	parentPath := linear(t, dir, "parent")
	parent := load(t, parentPath)

	forkPath := filepath.Join(dir, "fork.jsonl")
	n, err := parent.Fork("cccccccc-3", "fork-session", forkPath, "a fork")
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	if got, want := n, 3; got != want {
		t.Errorf("forked %d messages, want %d", got, want)
	}

	fork := load(t, forkPath)

	t.Run("session id is rewritten everywhere", func(t *testing.T) {
		for _, r := range fork.Records {
			if got := r.SessionID(); got != "fork-session" {
				t.Fatalf("record %s has sessionId %q, want fork-session", r.UUID(), got)
			}
		}
	})

	t.Run("exactly one root and no dangling parents", func(t *testing.T) {
		roots, ids := 0, map[string]bool{}
		for _, r := range fork.Records {
			if r.InDAG() {
				ids[r.UUID()] = true
			}
		}
		for _, r := range fork.Records {
			if !r.InDAG() {
				continue
			}
			if r.ParentUUID() == "" {
				roots++
			} else if !ids[r.ParentUUID()] {
				t.Errorf("record %s references missing parent %s", r.UUID(), r.ParentUUID())
			}
		}
		if roots != 1 {
			t.Errorf("roots = %d, want 1", roots)
		}
	})

	t.Run("tip is the requested checkpoint", func(t *testing.T) {
		if got, want := fork.Tip(), "cccccccc-3"; got != want {
			t.Errorf("Tip = %q, want %q", got, want)
		}
	})

	t.Run("messages after the checkpoint are excluded", func(t *testing.T) {
		if _, ok := fork.Get("dddddddd-4"); ok {
			t.Error("fork contains a message from after the checkpoint")
		}
	})

	// Byte-identical inherited records keep the replayed prefix stable, which
	// is what lets the API serve it from cache instead of at full price.
	t.Run("inherited records differ only by sessionId", func(t *testing.T) {
		for _, forked := range fork.Records {
			if !forked.InDAG() {
				continue
			}
			original, ok := parent.Get(forked.UUID())
			if !ok {
				t.Fatalf("fork invented record %s", forked.UUID())
			}
			a, b := toMap(t, forked), toMap(t, original)
			delete(a, "sessionId")
			delete(b, "sessionId")
			if !jsonEqual(t, a, b) {
				t.Errorf("record %s was altered by forking", forked.UUID())
			}
		}
	})

	t.Run("carries the fork title", func(t *testing.T) {
		if got, want := fork.Title(), "a fork"; got != want {
			t.Errorf("Title = %q, want %q", got, want)
		}
	})

	// Forks hold the same private conversation content as their parent.
	t.Run("written 0600", func(t *testing.T) {
		info, err := os.Stat(forkPath)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := info.Mode().Perm(), os.FileMode(0o600); got != want {
			t.Errorf("permissions = %v, want %v", got, want)
		}
	})
}

func TestForkRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	parent := load(t, linear(t, dir, "parent"))
	existing := filepath.Join(dir, "taken.jsonl")
	if err := os.WriteFile(existing, []byte("do not clobber\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := parent.Fork("bbbbbbbb-2", "new", existing, ""); err == nil {
		t.Fatal("Fork overwrote an existing file")
	}
	got, err := os.ReadFile(existing)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "do not clobber\n" {
		t.Error("existing file was modified")
	}
}

func TestForkUnknownCheckpoint(t *testing.T) {
	dir := t.TempDir()
	parent := load(t, linear(t, dir, "parent"))
	if _, err := parent.Fork("nope", "new", filepath.Join(dir, "out.jsonl"), ""); err == nil {
		t.Error("forking an unknown checkpoint must error")
	}
}

func TestSummaryAndText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.jsonl")
	writeTranscript(t, path, "s", []msg{
		{uuid: "a", role: "user", text: "line one\nline two"},
		{uuid: "b", parent: "a", role: "assistant", tool: "Bash"},
	})
	tr := load(t, path)

	first, _ := tr.Get("a")
	if got, want := first.Summary(), "line one line two"; got != want {
		t.Errorf("Summary = %q, want %q (newlines collapse)", got, want)
	}
	if got, want := first.Text(), "line one\nline two"; got != want {
		t.Errorf("Text = %q, want %q (structure preserved)", got, want)
	}

	second, _ := tr.Get("b")
	if got, want := second.Summary(), "[Bash]"; got != want {
		t.Errorf("Summary = %q, want %q", got, want)
	}
	if !strings.Contains(second.Text(), "Bash") {
		t.Errorf("Text = %q, want it to name the tool", second.Text())
	}
}

func TestMessageUUIDs(t *testing.T) {
	tr := load(t, linear(t, t.TempDir(), "s"))
	ids := tr.MessageUUIDs()
	if got, want := len(ids), 4; got != want {
		t.Errorf("MessageUUIDs = %d entries, want %d", got, want)
	}
	if _, ok := ids["aaaaaaaa-1"]; !ok {
		t.Error("missing a known uuid")
	}
}

func toMap(t *testing.T, r Record) map[string]any {
	t.Helper()
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func jsonEqual(t *testing.T, a, b map[string]any) bool {
	t.Helper()
	x, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	y, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	return string(x) == string(y)
}
