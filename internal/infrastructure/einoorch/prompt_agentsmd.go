package einoorch

import (
	"os"
	"path/filepath"
	"strings"
)

type AgentsMdFile struct {
	FilePath string
	FileName string
	Content  string
}

func DiscoverAgentsMdFiles(workspace string, maxDepth int) []AgentsMdFile {
	if workspace == "" {
		return nil
	}
	var results []AgentsMdFile
	visited := make(map[string]bool)
	_ = walkAgentsMd(workspace, workspace, 0, maxDepth, &results, visited)
	return results
}

func walkAgentsMd(root, current string, depth, maxDepth int, results *[]AgentsMdFile, visited map[string]bool) error {
	if depth > maxDepth {
		return nil
	}
	abs, _ := filepath.Abs(current)
	if visited[abs] {
		return nil
	}
	visited[abs] = true

	entries, err := os.ReadDir(current)
	if err != nil {
		return nil
	}

	for _, e := range entries {
		if e.IsDir() {
			name := e.Name()
			if isSkippedDir(name) {
				continue
			}
			child := filepath.Join(current, name)
			if err := walkAgentsMd(root, child, depth+1, maxDepth, results, visited); err != nil {
				return nil
			}
		} else {
			name := e.Name()
			if isAgentsMd(name) {
				fullPath := filepath.Join(current, name)
				content, err := os.ReadFile(fullPath)
				if err == nil && len(content) > 0 {
					relPath := fullPath
					if rel, err := filepath.Rel(root, fullPath); err == nil {
						relPath = filepath.ToSlash(filepath.Join(root, rel))
					}
					*results = append(*results, AgentsMdFile{
						FilePath: relPath,
						FileName: name,
						Content:  string(content),
					})
				}
			}
		}
	}
	return nil
}

func isAgentsMd(name string) bool {
	lower := strings.ToLower(name)
	return lower == "agents.md" || lower == "codex.md" || lower == "rules.md" || lower == "cursorrules"
}

func isSkippedDir(name string) bool {
	switch name {
	case ".git", ".hg", ".svn", "node_modules", "vendor", "dist", "build", ".next", ".cache":
		return true
	}
	return false
}

func FormatAgentsMdSection(files []AgentsMdFile) string {
	if len(files) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<system-reminder>\n")
	b.WriteString("Project instruction files found. You MUST read and follow these instructions:\n\n")
	for _, f := range files {
		b.WriteString("## From: ")
		b.WriteString(f.FilePath)
		b.WriteString("\n\n")
		b.WriteString(f.Content)
		b.WriteString("\n\n")
	}
	b.WriteString("These instructions form part of your durable identity for this session. When instructions from different files conflict, the deeper file takes precedence.\n")
	b.WriteString("</system-reminder>")
	return b.String()
}

func AgentsMdUserReminder(files []AgentsMdFile) string {
	section := FormatAgentsMdSection(files)
	if section == "" {
		return ""
	}
	return section
}
