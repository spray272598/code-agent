package spec

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Loader reads spec.md, tasks.md, checklist.md and CLAUDE.md from a project directory.
type Loader struct {
	BaseDir string
}

func NewLoader(baseDir string) *Loader {
	return &Loader{BaseDir: baseDir}
}

// LoadAll loads all spec files from the base directory.
func (l *Loader) LoadAll() (*SpecBundle, error) {
	bundle := NewSpecBundle(l.BaseDir)

	if spec := l.loadSpec(); spec != nil {
		bundle.Spec = spec
	}
	bundle.Tasks = l.loadTasks()
	bundle.Checklist = l.loadChecklist()
	bundle.ClaudeMD = l.loadClaudeMD()

	if bundle.IsEmpty() {
		return bundle, nil
	}
	return bundle, nil
}

// HasSpec checks whether spec.md exists in the base directory.
func (l *Loader) HasSpec() bool {
	_, err := os.Stat(filepath.Join(l.BaseDir, "spec.md"))
	return err == nil
}

// HasCLAUDE checks whether CLAUDE.md exists in the base directory.
func (l *Loader) HasCLAUDE() bool {
	_, err := os.Stat(filepath.Join(l.BaseDir, "CLAUDE.md"))
	return err == nil
}

func (l *Loader) loadSpec() *Spec {
	path := filepath.Join(l.BaseDir, "spec.md")
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	content := string(b)
	meta, body := splitFrontmatter(content)

	spec := &Spec{
		ID:       strings.TrimSpace(meta["id"]),
		Title:    strings.TrimSpace(meta["title"]),
		Goal:     strings.TrimSpace(meta["goal"]),
		Body:     strings.TrimSpace(body),
		Meta:     meta,
		LoadedAt: time.Now().Format(time.RFC3339),
	}
	if spec.Title == "" {
		spec.Title = spec.ID
	}
	if spec.Goal == "" {
		spec.Goal = extractGoalFromBody(body)
	}
	if v := meta["constraints"]; v != "" {
		spec.Constraints = splitList(v)
	}
	if v := meta["acceptance"]; v != "" {
		spec.Acceptance = splitList(v)
	}
	if v := meta["tech_notes"]; v != "" {
		spec.TechNotes = v
	}
	if v := meta["technotes"]; v != "" {
		spec.TechNotes = v
	}
	// parse markdown sections from body
	spec.parseSections(body)
	return spec
}

func (s *Spec) parseSections(body string) {
	if body == "" {
		return
	}
	// extract ## Constraints section
	s.Constraints = extractBulletSection(body, "Constraints", s.Constraints)
	s.Acceptance = extractBulletSection(body, "Acceptance Criteria", s.Acceptance)
	if len(s.Acceptance) == 0 {
		s.Acceptance = extractBulletSection(body, "Acceptance", s.Acceptance)
	}
}

func extractBulletSection(body, heading string, existing []string) []string {
	if len(existing) > 0 {
		return existing
	}
	re := regexp.MustCompile(`(?m)^##\s+` + regexp.QuoteMeta(heading) + `\s*$`)
	loc := re.FindStringIndex(body)
	if loc == nil {
		return existing
	}
	after := body[loc[1]:]
	// find next ## heading
	nextRe := regexp.MustCompile(`(?m)^##\s+`)
	next := nextRe.FindStringIndex(after)
	var section string
	if next != nil {
		section = after[:next[0]]
	} else {
		section = after
	}
	var items []string
	for _, line := range strings.Split(section, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			items = append(items, strings.TrimSpace(line[2:]))
		}
	}
	return items
}

func extractGoalFromBody(body string) string {
	re := regexp.MustCompile(`(?m)^#\s+(.+)$`)
	m := re.FindStringSubmatch(body)
	if len(m) > 1 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func (l *Loader) loadTasks() []Task {
	path := filepath.Join(l.BaseDir, "tasks.md")
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	content := string(b)
	meta, _ := splitFrontmatter(content)

	var tasks []Task
	// parse markdown task list
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "---") || strings.HasPrefix(line, "#") {
			continue
		}
		// match task patterns:
		// - [ ] task_id: description (pending)
		// - [x] task_id: description (done)
		// - [→] task_id: description (in_progress)
		// numeric prefix: 1. task description
		if t, ok := parseTaskLine(line); ok {
			tasks = append(tasks, t)
		}
	}
	// parse from YAML frontmatter if markdown parsing found nothing
	if len(tasks) == 0 {
		if raw := meta["tasks"]; raw != "" {
			for _, item := range splitList(raw) {
				tasks = append(tasks, Task{
					ID:     fmt.Sprintf("task-%d", len(tasks)+1),
					Title:  item,
					Status: "pending",
				})
			}
		}
	}
	return tasks
}

var (
	taskLineRe     = regexp.MustCompile(`^\s*[-*]\s+\[([ xX→!])\]\s*(.+)$`)
	taskNumberedRe = regexp.MustCompile(`^\s*(\d+)\.\s*(.+)$`)
)

func parseTaskLine(line string) (Task, bool) {
	if m := taskLineRe.FindStringSubmatch(line); m != nil {
		status := "pending"
		switch m[1] {
		case "x", "X":
			status = "done"
		case "→":
			status = "in_progress"
		case "!":
			status = "blocked"
		}
		rest := strings.TrimSpace(m[2])
		id, title := splitTaskID(rest)
		return Task{ID: id, Title: title, Status: status}, true
	}
	if m := taskNumberedRe.FindStringSubmatch(line); m != nil {
		id, title := splitTaskID(strings.TrimSpace(m[2]))
		return Task{ID: id, Title: title, Status: "pending"}, true
	}
	return Task{}, false
}

func splitTaskID(rest string) (id, title string) {
	re := regexp.MustCompile(`^([a-zA-Z0-9_\-]+)[:：]\s*(.+)$`)
	if m := re.FindStringSubmatch(rest); m != nil {
		return m[1], strings.TrimSpace(m[2])
	}
	return "", rest
}

func (l *Loader) loadChecklist() []ChecklistItem {
	path := filepath.Join(l.BaseDir, "checklist.md")
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	content := string(b)
	var items []ChecklistItem
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "---") || strings.HasPrefix(line, "#") {
			continue
		}
		if item, ok := parseChecklistLine(line); ok {
			items = append(items, item)
		}
	}
	return items
}

var checkLineRe = regexp.MustCompile(`^\s*[-*]\s+\[([ xX✗])\]\s*(.+)$`)

func parseChecklistLine(line string) (ChecklistItem, bool) {
	if m := checkLineRe.FindStringSubmatch(line); m != nil {
		status := "pending"
		switch m[1] {
		case "x", "X":
			status = "done"
		case "✗":
			status = "failed"
		}
		return ChecklistItem{
			ID:     fmt.Sprintf("chk-%d", len([]ChecklistItem{})+1),
			Text:   strings.TrimSpace(m[2]),
			Status: status,
		}, true
	}
	return ChecklistItem{}, false
}

func (l *Loader) loadClaudeMD() string {
	path := filepath.Join(l.BaseDir, "CLAUDE.md")
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	content := strings.TrimSpace(string(b))
	// strip frontmatter
	_, body := splitFrontmatter(content)
	if body != "" {
		return strings.TrimSpace(body)
	}
	return content
}

// splitFrontmatter splits YAML frontmatter from markdown body.
func splitFrontmatter(content string) (map[string]string, string) {
	content = strings.TrimSpace(content)
	meta := map[string]string{}
	if !strings.HasPrefix(content, "---") {
		return meta, content
	}
	rest := content[3:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return meta, content
	}
	fm := rest[:end]
	body := strings.TrimSpace(rest[end+4:])
	var currentKey string
	var list []string
	flush := func() {
		if currentKey != "" && len(list) > 0 {
			meta[currentKey] = strings.Join(list, ",")
			list = nil
		}
	}
	for _, line := range strings.Split(fm, "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		if strings.HasPrefix(trim, "- ") && currentKey != "" {
			list = append(list, strings.TrimSpace(trim[2:]))
			continue
		}
		flush()
		if i := strings.Index(line, ":"); i > 0 {
			key := strings.ToLower(strings.TrimSpace(line[:i]))
			val := strings.TrimSpace(line[i+1:])
			val = strings.Trim(val, `"'`)
			currentKey = key
			if val == "" || val == "|" {
				list = nil
				continue
			}
			meta[key] = val
		}
	}
	flush()
	return meta, body
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == '\n' }) {
		p = strings.TrimSpace(strings.Trim(p, `"'`))
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
