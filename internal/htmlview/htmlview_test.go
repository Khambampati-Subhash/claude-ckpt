package htmlview

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/khambampati-subhash/claude-ckpt/internal/forest"
	"github.com/khambampati-subhash/claude-ckpt/internal/lineage"
)

func writeSession(t *testing.T, dir, id string, uuids []string, displayable bool) {
	t.Helper()
	var body []byte
	for i, u := range uuids {
		rec := map[string]any{
			"type":      "user",
			"uuid":      u,
			"sessionId": id,
			"cwd":       "/work",
			"timestamp": "2026-01-01T00:00:00Z",
		}
		if i == 0 {
			rec["parentUuid"] = nil
		} else {
			rec["parentUuid"] = uuids[i-1]
		}
		if displayable {
			rec["message"] = map[string]any{"role": "user", "content": "hello " + u}
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

func render(t *testing.T, dir string) string {
	t.Helper()
	f, err := forest.Build(dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	var sb strings.Builder
	if err := Render(f, dir, &sb); err != nil {
		t.Fatalf("Render: %v", err)
	}
	return sb.String()
}

var payload = regexp.MustCompile(`(?s)<script id="ckpt-data" type="application/json">(.*?)</script>`)

func embedded(t *testing.T, page string) map[string]any {
	t.Helper()
	m := payload.FindStringSubmatch(page)
	if m == nil {
		t.Fatal("no embedded data payload found")
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(m[1]), &out); err != nil {
		t.Fatalf("embedded payload is not valid JSON: %v", err)
	}
	return out
}

// A nil slice marshals to JSON null, and the page then throws on load reading
// .length. One session with nothing displayable would blank the whole view.
func TestSessionWithNoDisplayableMessagesYieldsEmptyArrays(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CKPT_HOME", t.TempDir())
	writeSession(t, dir, "quiet", []string{"a", "b"}, false)

	page := render(t, dir)
	if strings.Contains(page, `"timeline":null`) {
		t.Error(`page contains "timeline":null, which crashes on load`)
	}
	if strings.Contains(page, `"children":null`) {
		t.Error(`page contains "children":null`)
	}

	roots := embedded(t, page)["roots"].([]any)
	root := roots[0].(map[string]any)
	if root["timeline"] == nil {
		t.Error("timeline serialized as null, want an empty array")
	}
	if root["children"] == nil {
		t.Error("children serialized as null, want an empty array")
	}
}

// The page must open from file:// on a machine with no network.
func TestPageIsSelfContained(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CKPT_HOME", t.TempDir())
	writeSession(t, dir, "s", []string{"a", "b"}, true)

	page := render(t, dir)
	for _, pattern := range []string{`<script src=`, `<link `, `<img `, `@import`} {
		if strings.Contains(page, pattern) {
			t.Errorf("page references an external resource: %q", pattern)
		}
	}
}

func TestForkStructureReachesThePage(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CKPT_HOME", t.TempDir())
	writeSession(t, dir, "parent", []string{"a", "b", "c"}, true)
	writeSession(t, dir, "fork", []string{"a", "b"}, true)
	if err := lineage.Append(lineage.Record{
		Session: "fork", Parent: "parent", ForkPoint: "b", Dir: dir,
	}); err != nil {
		t.Fatal(err)
	}

	data := embedded(t, render(t, dir))
	if got := data["forks"]; got != float64(1) {
		t.Errorf("forks = %v, want 1", got)
	}
	roots := data["roots"].([]any)
	if len(roots) != 1 {
		t.Fatalf("roots = %d, want 1", len(roots))
	}
	children := roots[0].(map[string]any)["children"].([]any)
	if len(children) != 1 {
		t.Fatalf("children = %d, want 1", len(children))
	}
	child := children[0].(map[string]any)
	if child["forkPoint"] != "b" {
		t.Errorf("forkPoint = %v, want b", child["forkPoint"])
	}
}

// Transcripts run to megabytes; an uncapped page would be unopenable.
func TestLongMessagesAreTruncated(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CKPT_HOME", t.TempDir())

	long := strings.Repeat("x", excerptLimit*2)
	rec := map[string]any{
		"type": "user", "uuid": "a", "parentUuid": nil, "sessionId": "s",
		"cwd": "/work", "message": map[string]any{"role": "user", "content": long},
	}
	line, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "s.jsonl"), append(line, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	data := embedded(t, render(t, dir))
	timeline := data["roots"].([]any)[0].(map[string]any)["timeline"].([]any)
	text := timeline[0].(map[string]any)["text"].(string)
	if len(text) >= len(long) {
		t.Errorf("text length = %d, want it capped near %d", len(text), excerptLimit)
	}
	if !strings.Contains(text, "truncated") {
		t.Error("truncation should be visible to the reader, not silent")
	}
}
