// Package worktree manages the git worktrees that isolate parallel branches.
//
// Forking a conversation isolates the conversation and nothing else. Two
// sessions resumed from the same checkpoint still share a working directory, so
// they edit the same files and overwrite each other. A worktree per branch is
// what makes parallel exploration actually parallel.
package worktree

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Repo is a git repository that worktrees can be added to.
type Repo struct {
	Root string // absolute path to the main working tree
}

// Open locates the repository containing dir.
func Open(dir string) (*Repo, error) {
	out, err := run(dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, fmt.Errorf("%s is not inside a git repository: %w", dir, err)
	}
	return &Repo{Root: canonical(strings.TrimSpace(out))}, nil
}

// CurrentBranch reports the branch currently checked out in the main tree.
func (r *Repo) CurrentBranch() (string, error) {
	out, err := run(r.Root, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// IsClean reports whether the working tree has no uncommitted changes.
// Worktrees branch from HEAD, so uncommitted work would not be carried into
// them — worth warning about before a run rather than after.
func (r *Repo) IsClean() (bool, error) {
	out, err := run(r.Root, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "", nil
}

// Worktree is one isolated checkout.
type Worktree struct {
	Path   string
	Branch string
}

// Add creates a worktree at path on a new branch. The branch starts at the
// main tree's current HEAD, so every parallel branch begins from the same
// state the fork's conversation last saw.
func (r *Repo) Add(path, branch string) (*Worktree, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if _, err := run(r.Root, "worktree", "add", "-b", branch, abs); err != nil {
		return nil, fmt.Errorf("git worktree add %s: %w", abs, err)
	}
	// Report the resolved path so it matches what git will hand back later.
	return &Worktree{Path: canonical(abs), Branch: branch}, nil
}

// Remove deletes a worktree and, unless keepBranch, its branch.
//
// force discards uncommitted changes in the worktree. Branch deletion uses -D
// rather than -d: a losing branch is expected to be unmerged, and that is
// exactly what we are throwing away.
func (r *Repo) Remove(path string, keepBranch bool, force bool) error {
	// Resolve the branch first: once the worktree is removed it no longer
	// appears in `git worktree list`, so looking it up afterwards always comes
	// back empty and the branch is silently left behind.
	branch := ""
	if !keepBranch {
		branch, _ = branchOf(r, path)
	}

	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, path)
	if _, err := run(r.Root, args...); err != nil {
		return fmt.Errorf("git worktree remove %s: %w", path, err)
	}

	if branch != "" {
		// -D rather than -d: a losing branch is expected to be unmerged, and
		// that is precisely what is being discarded.
		if _, err := run(r.Root, "branch", "-D", branch); err != nil {
			return fmt.Errorf("removed %s but could not delete branch %s: %w", path, branch, err)
		}
	}
	return nil
}

// Merge brings branch into the main tree's current branch.
func (r *Repo) Merge(branch, message string) error {
	args := []string{"merge", "--no-ff", branch}
	if message != "" {
		args = append(args, "-m", message)
	}
	if _, err := run(r.Root, args...); err != nil {
		return fmt.Errorf("git merge %s: %w", branch, err)
	}
	return nil
}

// Exists reports whether a worktree is registered at path.
func (r *Repo) Exists(path string) bool {
	list, err := r.List()
	if err != nil {
		return false
	}
	want := canonical(path)
	for _, w := range list {
		if canonical(w.Path) == want {
			return true
		}
	}
	return false
}

// List returns every worktree registered on the repository, excluding the main
// working tree itself.
func (r *Repo) List() ([]Worktree, error) {
	out, err := run(r.Root, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var (
		result  []Worktree
		current Worktree
	)
	flush := func() {
		if current.Path != "" && canonical(current.Path) != r.Root {
			result = append(result, current)
		}
		current = Worktree{}
	}
	for line := range strings.SplitSeq(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			current.Path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "branch "):
			current.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		}
	}
	flush()
	return result, nil
}

func branchOf(r *Repo, path string) (string, error) {
	list, err := r.List()
	if err != nil {
		return "", err
	}
	want := canonical(path)
	for _, w := range list {
		if canonical(w.Path) == want {
			return w.Branch, nil
		}
	}
	return "", nil
}

// canonical resolves a path for comparison against git's output.
//
// git reports worktree paths with symlinks resolved. On macOS the usual
// temporary and working directories are symlinked (/tmp -> /private/tmp,
// /var -> /private/var), so comparing raw absolute paths silently fails to
// match and a worktree looks unregistered when it is not.
func canonical(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs // path may not exist yet; absolute is the best we can do
}

func run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
