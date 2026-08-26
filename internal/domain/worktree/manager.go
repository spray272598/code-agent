package worktree

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Manager creates isolated git worktrees under .code-agent/worktrees/.
// Falls back to plain directories if not a git repo.
type Manager struct {
	mu       sync.Mutex
	RepoRoot string
	BaseDir  string // .code-agent/worktrees
}

func NewManager(repoRoot string) *Manager {
	if repoRoot == "" {
		repoRoot = "."
	}
	abs, err := filepath.Abs(repoRoot)
	if err == nil {
		repoRoot = abs
	}
	return &Manager{
		RepoRoot: repoRoot,
		BaseDir:  filepath.Join(repoRoot, ".code-agent", "worktrees"),
	}
}

// Create returns worktree path and cleanup func.
func (m *Manager) Create(id string) (string, func(), error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id = sanitize(id)
	if id == "" {
		id = fmt.Sprintf("wt-%d", os.Getpid())
	}
	_ = os.MkdirAll(m.BaseDir, 0o755)
	path := filepath.Join(m.BaseDir, id)

	if isGitRepo(m.RepoRoot) {
		// remove stale
		_ = os.RemoveAll(path)
		branch := "agent/" + id
		// git worktree add -b branch path HEAD
		cmd := exec.Command("git", "worktree", "add", "-B", branch, path, "HEAD")
		cmd.Dir = m.RepoRoot
		out, err := cmd.CombinedOutput()
		if err != nil {
			// fallback directory
			if mkErr := os.MkdirAll(path, 0o755); mkErr != nil {
				return "", nil, fmt.Errorf("worktree add failed: %v (%s)", err, strings.TrimSpace(string(out)))
			}
			return path, func() { _ = os.RemoveAll(path) }, nil
		}
		cleanup := func() {
			_ = exec.Command("git", "worktree", "remove", "--force", path).Run()
			_ = exec.Command("git", "-C", m.RepoRoot, "branch", "-D", branch).Run()
			_ = os.RemoveAll(path)
		}
		return path, cleanup, nil
	}

	if err := os.MkdirAll(path, 0o755); err != nil {
		return "", nil, err
	}
	return path, func() { _ = os.RemoveAll(path) }, nil
}

func isGitRepo(root string) bool {
	st, err := os.Stat(filepath.Join(root, ".git"))
	return err == nil && st.IsDir()
}

func sanitize(id string) string {
	var b strings.Builder
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	s := b.String()
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}
