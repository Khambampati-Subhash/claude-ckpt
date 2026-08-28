package worktree

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// newRepo creates a git repository with one commit.
func newRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main", ".")
	git(t, dir, "config", "user.email", "test@example.com")
	git(t, dir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "app.txt"), []byte("v1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-qm", "initial")
	return dir
}

func open(t *testing.T, dir string) *Repo {
	t.Helper()
	r, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return r
}

func branches(t *testing.T, dir string) []string {
	t.Helper()
	out := git(t, dir, "branch", "--format=%(refname:short)")
	var names []string
	for _, b := range splitLines(out) {
		if b != "" {
			names = append(names, b)
		}
	}
	return names
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := range len(s) {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func TestOpenOutsideRepository(t *testing.T) {
	if _, err := Open(t.TempDir()); err == nil {
		t.Error("Open outside a git repository should fail")
	}
}

func TestCurrentBranchAndIsClean(t *testing.T) {
	dir := newRepo(t)
	r := open(t, dir)

	got, err := r.CurrentBranch()
	if err != nil {
		t.Fatal(err)
	}
	if got != "main" {
		t.Errorf("CurrentBranch = %q, want main", got)
	}

	clean, err := r.IsClean()
	if err != nil {
		t.Fatal(err)
	}
	if !clean {
		t.Error("a freshly committed repo should be clean")
	}

	if err := os.WriteFile(filepath.Join(dir, "app.txt"), []byte("edited\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if clean, _ := r.IsClean(); clean {
		t.Error("repo with an uncommitted edit reported clean")
	}
}

func TestAddAndList(t *testing.T) {
	dir := newRepo(t)
	r := open(t, dir)

	path := filepath.Join(filepath.Dir(dir), "wt-a")
	wt, err := r.Add(path, "ckpt/a")
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if wt.Branch != "ckpt/a" {
		t.Errorf("Branch = %q, want ckpt/a", wt.Branch)
	}
	if _, err := os.Stat(filepath.Join(wt.Path, "app.txt")); err != nil {
		t.Errorf("worktree does not contain the repo contents: %v", err)
	}

	list, err := r.List()
	if err != nil {
		t.Fatal(err)
	}
	// The main working tree is not a worktree we manage.
	if len(list) != 1 {
		t.Fatalf("List = %d entries, want 1 (main tree must be excluded)", len(list))
	}
	if list[0].Branch != "ckpt/a" {
		t.Errorf("listed branch = %q, want ckpt/a", list[0].Branch)
	}
	if !r.Exists(path) {
		t.Error("Exists should report true for a registered worktree")
	}
}

// Regression: Remove once resolved the branch *after* deleting the worktree, so
// the lookup came back empty and the branch was silently left behind.
func TestRemoveDeletesWorktreeAndBranch(t *testing.T) {
	dir := newRepo(t)
	r := open(t, dir)

	path := filepath.Join(filepath.Dir(dir), "wt-doomed")
	if _, err := r.Add(path, "ckpt/doomed"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(branches(t, dir), "ckpt/doomed") {
		t.Fatal("branch was not created")
	}

	if err := r.Remove(path, false, false); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if r.Exists(path) {
		t.Error("worktree still registered after Remove")
	}
	if slices.Contains(branches(t, dir), "ckpt/doomed") {
		t.Error("branch survived Remove — it must be deleted alongside the worktree")
	}
}

func TestRemoveCanKeepBranch(t *testing.T) {
	dir := newRepo(t)
	r := open(t, dir)

	path := filepath.Join(filepath.Dir(dir), "wt-keep")
	if _, err := r.Add(path, "ckpt/keep"); err != nil {
		t.Fatal(err)
	}
	if err := r.Remove(path, true, false); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if !slices.Contains(branches(t, dir), "ckpt/keep") {
		t.Error("branch should survive when keepBranch is set")
	}
}

// A branch with unmerged commits is exactly what a losing branch looks like, so
// deletion must not refuse it.
func TestRemoveDeletesUnmergedBranch(t *testing.T) {
	dir := newRepo(t)
	r := open(t, dir)

	path := filepath.Join(filepath.Dir(dir), "wt-unmerged")
	wt, err := r.Add(path, "ckpt/unmerged")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt.Path, "app.txt"), []byte("diverged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, wt.Path, "add", "-A")
	git(t, wt.Path, "commit", "-qm", "diverge")

	if err := r.Remove(path, false, false); err != nil {
		t.Fatalf("Remove on an unmerged branch: %v", err)
	}
	if slices.Contains(branches(t, dir), "ckpt/unmerged") {
		t.Error("unmerged branch survived Remove")
	}
}

// Uncommitted work must not be destroyed unless force is given.
func TestRemoveRefusesDirtyWorktreeWithoutForce(t *testing.T) {
	dir := newRepo(t)
	r := open(t, dir)

	path := filepath.Join(filepath.Dir(dir), "wt-dirty")
	wt, err := r.Add(path, "ckpt/dirty")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt.Path, "app.txt"), []byte("unsaved\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := r.Remove(path, false, false); err == nil {
		t.Error("Remove should refuse a dirty worktree without force")
	}
	if !r.Exists(path) {
		t.Error("worktree was removed despite the refusal")
	}
	if err := r.Remove(path, false, true); err != nil {
		t.Errorf("Remove with force: %v", err)
	}
}

func TestMergeBringsBranchWorkIntoMain(t *testing.T) {
	dir := newRepo(t)
	r := open(t, dir)

	path := filepath.Join(filepath.Dir(dir), "wt-winner")
	wt, err := r.Add(path, "ckpt/winner")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt.Path, "app.txt"), []byte("winning\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, wt.Path, "add", "-A")
	git(t, wt.Path, "commit", "-qm", "winning change")

	if err := r.Merge("ckpt/winner", "Merge the winner"); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "app.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "winning\n" {
		t.Errorf("main tree has %q, want the merged content", got)
	}
}

func TestMergeUnknownBranch(t *testing.T) {
	r := open(t, newRepo(t))
	if err := r.Merge("ckpt/does-not-exist", "msg"); err == nil {
		t.Error("merging an unknown branch should fail")
	}
}
