package skill

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type Service struct {
	mu      sync.RWMutex
	rootDir string
	skills  map[string]*Skill
}

func NewService(rootDir string) *Service {
	if rootDir == "" {
		rootDir = "./skills"
	}
	s := &Service{rootDir: rootDir, skills: map[string]*Skill{}}
	_ = s.Reload()
	return s
}

func (s *Service) RootDir() string { return s.rootDir }

func (s *Service) Reload() error {
	_ = os.MkdirAll(s.rootDir, 0o755)
	loaded := map[string]*Skill{}
	entries, err := os.ReadDir(s.rootDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sk, err := parseSkillDir(filepath.Join(s.rootDir, e.Name()))
		if err != nil || sk == nil {
			continue
		}
		sk.Enabled = true
		sk.Source = "installed"
		loaded[sk.ID] = sk
	}
	s.mu.Lock()
	s.skills = loaded
	s.mu.Unlock()
	return nil
}

func (s *Service) List() []*Skill {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Skill, 0, len(s.skills))
	for _, sk := range s.skills {
		cp := *sk
		out = append(out, &cp)
	}
	return out
}

func (s *Service) Get(id string) *Skill {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sk := s.skills[id]
	if sk == nil {
		return nil
	}
	cp := *sk
	return &cp
}

func (s *Service) Match(userInput string) *Skill {
	s.mu.RLock()
	defer s.mu.RUnlock()
	lower := strings.ToLower(userInput)
	var best *Skill
	bestScore := 0
	for id, sk := range s.skills {
		if !sk.Enabled {
			continue
		}
		if strings.Contains(lower, "/"+strings.ToLower(id)) ||
			strings.Contains(lower, "skill "+strings.ToLower(id)) {
			cp := *sk
			return &cp
		}
		sc := 0
		if strings.Contains(lower, strings.ToLower(sk.Name)) {
			sc += 25
		}
		for _, t := range sk.Triggers {
			if t != "" && strings.Contains(lower, strings.ToLower(t)) {
				sc += 20 + len([]rune(t))/2
			}
		}
		if sc > bestScore {
			bestScore = sc
			cp := *sk
			best = &cp
		}
	}
	if bestScore < 15 {
		return nil
	}
	return best
}

func (s *Service) PromptSection(sk *Skill) string {
	if sk == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Active Skill: ")
	b.WriteString(sk.Name)
	b.WriteString(" (")
	b.WriteString(sk.ID)
	b.WriteString(")\n")
	if sk.Description != "" {
		b.WriteString(sk.Description)
		b.WriteString("\n")
	}
	if len(sk.Tools) > 0 {
		b.WriteString("Prefer tools: ")
		b.WriteString(strings.Join(sk.Tools, ", "))
		b.WriteString("\n")
	}
	if sk.Body != "" {
		b.WriteString("\n### Skill guide\n")
		b.WriteString(sk.Body)
		b.WriteString("\n")
	}
	return b.String()
}

func (s *Service) InstallFromPath(srcPath, id string) (*Skill, error) {
	srcPath = filepath.Clean(srcPath)
	st, err := os.Stat(srcPath)
	if err != nil {
		return nil, err
	}
	srcDir := srcPath
	if !st.IsDir() {
		if !strings.EqualFold(filepath.Base(srcPath), "SKILL.md") {
			return nil, fmt.Errorf("need skill dir or SKILL.md")
		}
		srcDir = filepath.Dir(srcPath)
	}
	sk, err := parseSkillDir(srcDir)
	if err != nil {
		return nil, err
	}
	if id == "" {
		id = sk.ID
	}
	id = sanitize(id)
	dst := filepath.Join(s.rootDir, id)
	if err := copyDir(srcDir, dst); err != nil {
		return nil, err
	}
	if err := s.Reload(); err != nil {
		return nil, err
	}
	if got := s.Get(id); got != nil {
		return got, nil
	}
	return s.Get(sk.ID), nil
}

func (s *Service) Uninstall(id string) error {
	id = sanitize(id)
	dst := filepath.Join(s.rootDir, id)
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	return s.Reload()
}

func parseSkillDir(dir string) (*Skill, error) {
	data, err := os.ReadFile(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return nil, err
	}
	meta, body := splitFrontmatter(string(data))
	id := filepath.Base(dir)
	sk := &Skill{ID: id, Name: id, Body: strings.TrimSpace(body), Path: dir, Enabled: true}
	if v := meta["name"]; v != "" {
		sk.Name = v
	}
	if v := meta["id"]; v != "" {
		sk.ID = sanitize(v)
	}
	if v := meta["description"]; v != "" {
		sk.Description = v
	}
	if v := meta["triggers"]; v != "" {
		sk.Triggers = splitCSV(v)
	}
	if v := meta["tools"]; v != "" {
		sk.Tools = splitCSV(v)
	}
	return sk, nil
}

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

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == '\n' }) {
		p = strings.TrimSpace(strings.Trim(p, `"'`))
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func sanitize(id string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(id) {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else if r == ' ' {
			b.WriteByte('-')
		}
	}
	return b.String()
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		_ = os.MkdirAll(filepath.Dir(target), 0o755)
		out, err := os.Create(target)
		if err != nil {
			return err
		}
		defer out.Close()
		_, err = io.Copy(out, in)
		return err
	})
}
