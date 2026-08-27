package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// session writes a minimal transcript so store can find and read it.
func session(t *testing.T, dir, id, cwd string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".jsonl")
	line := `{"type":"user","uuid":"u1","parentUuid":null,"sessionId":"` + id +
		`","cwd":"` + cwd + `","message":{"role":"user","content":"hi"}}` + "\n"
	if err := os.WriteFile(path, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRootHonoursConfigDir(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/custom/claude")
	got, err := Root()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("/custom/claude", "projects"); got != want {
		t.Errorf("Root() = %q, want %q", got, want)
	}
}

// The slug flattens both path separators and dots, so a username like
// "first.last" becomes "first-last". Getting this wrong silently finds no
// sessions for anyone whose username contains a dot.
func TestProjectDirSlugFlattensDotsAndSeparators(t *testing.T) {
	config := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)

	cwd := "/Users/first.last/Documents/github"
	want := filepath.Join(config, "projects", "-Users-first-last-Documents-github")
	session(t, want, "s1", cwd)

	got, err := ProjectDir(cwd)
	if err != nil {
		t.Fatalf("ProjectDir: %v", err)
	}
	if got != want {
		t.Errorf("ProjectDir = %q, want %q", got, want)
	}
}

// The slug rule belongs to Claude Code and could change. When it does, lookup
// must degrade to a scan rather than reporting no sessions.
func TestProjectDirFallsBackToRecordedCWD(t *testing.T) {
	config := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)

	cwd := "/Users/someone/project"
	unexpected := filepath.Join(config, "projects", "slug_rule_we_do_not_know")
	session(t, unexpected, "s1", cwd)

	got, err := ProjectDir(cwd)
	if err != nil {
		t.Fatalf("ProjectDir: %v", err)
	}
	if got != unexpected {
		t.Errorf("ProjectDir = %q, want %q", got, unexpected)
	}
}

func TestProjectDirUnknownDirectory(t *testing.T) {
	config := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", config)
	if err := os.MkdirAll(filepath.Join(config, "projects"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := ProjectDir("/nowhere/at/all"); err == nil {
		t.Error("ProjectDir on an unknown directory should fail")
	}
}

func TestSessionsSortedNewestFirst(t *testing.T) {
	dir := t.TempDir()
	old := session(t, dir, "older", "/work")
	recent := session(t, dir, "newer", "/work")

	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	_ = recent

	// A stray non-transcript file must be ignored.
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := Sessions(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("sessions = %d, want 2", len(got))
	}
	if got[0].ID != "newer" {
		t.Errorf("first session = %q, want newer", got[0].ID)
	}
}

func TestFind(t *testing.T) {
	dir := t.TempDir()
	session(t, dir, "abc111", "/work")
	session(t, dir, "abc222", "/work")
	session(t, dir, "xyz333", "/work")

	t.Run("unique prefix", func(t *testing.T) {
		got, err := Find(dir, "xyz")
		if err != nil || got.ID != "xyz333" {
			t.Errorf("Find(xyz) = %v, %v", got.ID, err)
		}
	})
	t.Run("exact id", func(t *testing.T) {
		got, err := Find(dir, "abc111")
		if err != nil || got.ID != "abc111" {
			t.Errorf("Find(abc111) = %v, %v", got.ID, err)
		}
	})
	t.Run("ambiguous prefix is an error", func(t *testing.T) {
		if _, err := Find(dir, "abc"); err == nil {
			t.Error("ambiguous session prefix must error, not guess")
		}
	})
	t.Run("unknown prefix is an error", func(t *testing.T) {
		if _, err := Find(dir, "zzz"); err == nil {
			t.Error("unknown session prefix must error")
		}
	})
}
