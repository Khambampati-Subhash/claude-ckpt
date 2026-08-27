package lineage

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathHonoursCkptHome(t *testing.T) {
	t.Setenv("CKPT_HOME", "/custom/ckpt")
	got, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("/custom/ckpt", "forks.jsonl"); got != want {
		t.Errorf("Path() = %q, want %q", got, want)
	}
}

func TestAppendThenRead(t *testing.T) {
	t.Setenv("CKPT_HOME", t.TempDir())

	records := []Record{
		{Session: "fork-1", Parent: "parent-a", ForkPoint: "cp-1", Dir: "/work"},
		{Session: "fork-2", Parent: "parent-a", ForkPoint: "cp-2", Dir: "/work"},
	}
	for _, r := range records {
		if err := Append(r); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	got := Parents("/work")
	if len(got) != 2 {
		t.Fatalf("parents = %d, want 2", len(got))
	}
	if got["fork-1"].Parent != "parent-a" || got["fork-1"].ForkPoint != "cp-1" {
		t.Errorf("fork-1 = %+v", got["fork-1"])
	}
	if got["fork-1"].At == "" {
		t.Error("Append should stamp a timestamp when one is not supplied")
	}
}

// Two projects can hold sessions with unrelated histories; lineage from one
// must never be applied to the other.
func TestParentsFiltersByDirectory(t *testing.T) {
	t.Setenv("CKPT_HOME", t.TempDir())

	if err := Append(Record{Session: "a", Parent: "p", Dir: "/one"}); err != nil {
		t.Fatal(err)
	}
	if err := Append(Record{Session: "b", Parent: "p", Dir: "/two"}); err != nil {
		t.Fatal(err)
	}

	got := Parents("/one")
	if len(got) != 1 {
		t.Fatalf("parents = %d, want 1", len(got))
	}
	if _, ok := got["a"]; !ok {
		t.Error("expected the record from /one")
	}
}

// Forks made before lineage existed, or by hand, simply have no record. That
// must read as "unknown", never as an error.
func TestParentsMissingFileIsEmpty(t *testing.T) {
	t.Setenv("CKPT_HOME", filepath.Join(t.TempDir(), "does-not-exist"))
	if got := Parents("/work"); len(got) != 0 {
		t.Errorf("parents = %v, want empty", got)
	}
}

func TestParentsSkipsMalformedLines(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CKPT_HOME", home)

	path := filepath.Join(home, "forks.jsonl")
	body := "{not json\n" +
		`{"session":"good","parent":"p","dir":"/work"}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	got := Parents("/work")
	if len(got) != 1 || got["good"].Parent != "p" {
		t.Errorf("parents = %v, want the one good record", got)
	}
}

func TestAppendWritesPrivateFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CKPT_HOME", home)
	if err := Append(Record{Session: "a", Parent: "p", Dir: "/work"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(home, "forks.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := info.Mode().Perm(), os.FileMode(0o600); got != want {
		t.Errorf("permissions = %v, want %v", got, want)
	}
}
