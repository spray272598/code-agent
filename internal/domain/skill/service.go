package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/spray272598/code-agent/internal/domain/agent/adapter/port"
	"github.com/spray272598/code-agent/internal/domain/tool"
)

type Service struct {
	mu      sync.RWMutex
	rootDir string
	skills  map[string]*Skill
	llm     port.ILLMPort
	embed   port.IEmbeddingPort
	// emb is a lazy cache of skill vectors (skillID -> embedding).
	emb map[string][]float32
	// market is the catalog source for the skills marketplace (3.2).
	market Marketplace
}

func NewService(rootDir string) *Service {
	if rootDir == "" {
		rootDir = "./skills"
	}
	s := &Service{rootDir: rootDir, skills: map[string]*Skill{}, emb: map[string][]float32{}}
	_ = s.Reload()
	return s
}

// SetLLM injects an LLM for semantic skill matching fallback. When nil, Match
// stays purely rule-based.
func (s *Service) SetLLM(llm port.ILLMPort) { s.llm = llm }

// SetEmbedder injects an embedding port for vector-based skill matching
// (preferred fast path; LLM is the fallback). Reload clears the vector cache.
func (s *Service) SetEmbedder(e port.IEmbeddingPort) { s.embed = e }

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
	s.emb = map[string][]float32{} // invalidate vector cache
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

const skillMatchSystemPrompt = `You decide whether a user message should activate one of the available skills.
Reply with STRICT JSON: {"skill":"<id>"} to activate, or {"skill":""} when no skill fits.
Choose a skill ONLY when the message is clearly within its domain. Be conservative: when unsure, return empty.`

// skillEmbThreshold is the cosine similarity floor for vector-based skill match.
const skillEmbThreshold = 0.55

// MatchSemantic resolves a skill for natural-language input when rule matching
// missed. Fast path: vector similarity (cheap). Fallback: LLM judgment.
func (s *Service) MatchSemantic(ctx context.Context, userInput string) *Skill {
	if s == nil {
		return nil
	}
	// fast path: embedding similarity
	if sk := s.MatchEmbedding(ctx, userInput); sk != nil {
		return sk
	}
	// fallback: LLM judgment
	if s.llm == nil {
		return nil
	}
	return s.matchLLM(ctx, userInput)
}

// MatchEmbedding ranks skills by cosine similarity of the user input against
// each skill's name+description+triggers. Returns nil when no vector passes the
// threshold or the embedder is unavailable.
func (s *Service) MatchEmbedding(ctx context.Context, userInput string) *Skill {
	if s == nil || s.embed == nil {
		return nil
	}
	cands := s.List()
	if len(cands) == 0 {
		return nil
	}
	qvecs, err := s.embed.Embed(ctx, []string{userInput})
	if err != nil || len(qvecs) != 1 || len(qvecs[0]) == 0 {
		return nil
	}
	qvec := qvecs[0]

	bestID := ""
	bestSim := 0.0
	for _, sk := range cands {
		if !sk.Enabled {
			continue
		}
		vec := s.skillVector(ctx, sk)
		if len(vec) == 0 {
			continue
		}
		if sim := cosSim(qvec, vec); sim > bestSim {
			bestSim = sim
			bestID = sk.ID
		}
	}
	if bestSim < skillEmbThreshold {
		return nil
	}
	return s.Get(bestID)
}

// skillVector returns (computing + caching) the embedding of a skill's textual
// identity: name + description + triggers.
func (s *Service) skillVector(ctx context.Context, sk *Skill) []float32 {
	s.mu.RLock()
	if v, ok := s.emb[sk.ID]; ok {
		s.mu.RUnlock()
		return v
	}
	s.mu.RUnlock()

	text := strings.Join(append([]string{sk.Name, sk.Description}, sk.Triggers...), " ")
	vecs, err := s.embed.Embed(ctx, []string{text})
	if err != nil || len(vecs) != 1 || len(vecs[0]) == 0 {
		return nil
	}
	s.mu.Lock()
	s.emb[sk.ID] = vecs[0]
	s.mu.Unlock()
	return vecs[0]
}

// matchLLM is the LLM fallback for skill matching.
func (s *Service) matchLLM(ctx context.Context, userInput string) *Skill {
	cands := s.List()
	if len(cands) == 0 {
		return nil
	}

	var b strings.Builder
	for _, sk := range cands {
		if !sk.Enabled {
			continue
		}
		b.WriteString("- id: ")
		b.WriteString(sk.ID)
		b.WriteString("\n  name: ")
		b.WriteString(sk.Name)
		if sk.Description != "" {
			b.WriteString("\n  description: ")
			b.WriteString(sk.Description)
		}
		if len(sk.Triggers) > 0 {
			b.WriteString("\n  triggers: ")
			b.WriteString(strings.Join(sk.Triggers, ", "))
		}
		b.WriteString("\n")
	}
	if strings.TrimSpace(b.String()) == "" {
		return nil
	}

	resp, err := s.llm.Generate(ctx, &port.ChatRequest{
		SystemPrompt: skillMatchSystemPrompt + "\n\n## Available skills\n" + b.String(),
		Messages:     []port.ChatMessage{{Role: "user", Content: userInput}},
		Temperature:  0.1,
		MaxTokens:    64,
	})
	if err != nil || resp == nil {
		return nil
	}
	id := parseSkillMatch(resp.Content)
	if id == "" {
		return nil
	}
	return s.Get(id)
}

// cosSim computes cosine similarity between two equal-length float32 vectors.
func cosSim(a, b []float32) float64 {
	if len(a) == 0 || len(b) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		ai := float64(a[i])
		bi := float64(b[i])
		dot += ai * bi
		na += ai * ai
		nb += bi * bi
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// parseSkillMatch extracts {"skill":"<id>"} from an LLM reply.
func parseSkillMatch(content string) string {
	content = strings.TrimSpace(content)
	start := strings.Index(content, "{")
	end := strings.LastIndex(content, "}")
	if start < 0 || end <= start {
		return ""
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(content[start:end+1]), &m); err != nil {
		return ""
	}
	return strings.TrimSpace(m["skill"])
}

func (s *Service) PromptSection(sk *Skill) string {
	if sk == nil {
		return ""
	}
	// compose depends (one level + transitive with cycle guard)
	composed := s.Compose(sk)
	var b strings.Builder
	for i, c := range composed {
		if i == 0 {
			b.WriteString("## Active Skill: ")
		} else {
			b.WriteString("\n## Dependent Skill: ")
		}
		b.WriteString(c.Name)
		b.WriteString(" (")
		b.WriteString(c.ID)
		b.WriteString(")\n")
		if c.Description != "" {
			b.WriteString(c.Description)
			b.WriteString("\n")
		}
		if len(c.Tools) > 0 {
			b.WriteString("Allowed tools: ")
			b.WriteString(strings.Join(c.Tools, ", "))
			b.WriteString("\n")
		}
		if c.Body != "" {
			b.WriteString("\n### Skill guide\n")
			b.WriteString(c.Body)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// Compose returns skill + dependencies (depth-first, cycle-safe).
func (s *Service) Compose(sk *Skill) []*Skill {
	if sk == nil {
		return nil
	}
	var out []*Skill
	visiting := map[string]bool{}
	visited := map[string]bool{}
	var walk func(*Skill) bool
	walk = func(cur *Skill) bool {
		if cur == nil {
			return false
		}
		if visiting[cur.ID] {
			return true // cycle detected – stop recursion, already visiting
		}
		if visited[cur.ID] {
			return false
		}
		visiting[cur.ID] = true
		hasCycle := false
		for _, dep := range cur.Depends {
			if walk(s.Get(dep)) {
				hasCycle = true
			}
		}
		visiting[cur.ID] = false
		visited[cur.ID] = true
		out = append(out, cur)
		return hasCycle
	}
	walk(sk)
	return out
}

// ComposeWithCycleCheck returns the composed skill list plus a bool
// indicating whether a cycle was detected.
func (s *Service) ComposeWithCycleCheck(sk *Skill) ([]*Skill, bool) {
	if sk == nil {
		return nil, false
	}
	var out []*Skill
	visiting := map[string]bool{}
	visited := map[string]bool{}
	cycle := false
	var walk func(*Skill)
	walk = func(cur *Skill) {
		if cur == nil {
			return
		}
		if visiting[cur.ID] {
			cycle = true
			return
		}
		if visited[cur.ID] {
			return
		}
		visiting[cur.ID] = true
		for _, dep := range cur.Depends {
			walk(s.Get(dep))
		}
		visiting[cur.ID] = false
		visited[cur.ID] = true
		out = append(out, cur)
	}
	walk(sk)
	return out, cycle
}

// BuildSkillTools returns the given skills wrapped as tool.ITool
// implementations suitable for registration into a tool registry.
// Disabled skills are skipped.
func (s *Service) BuildSkillTools() []tool.ITool {
	var out []tool.ITool
	for _, sk := range s.List() {
		if !sk.Enabled {
			continue
		}
		out = append(out, s.NewExecutableSkill(sk))
	}
	return out
}

// MergedTools unions tools from skill + depends; empty means unrestricted.
func (s *Service) MergedTools(sk *Skill) []string {
	composed := s.Compose(sk)
	var all []string
	empty := false
	seen := map[string]bool{}
	for _, c := range composed {
		if len(c.Tools) == 0 {
			empty = true
			continue
		}
		for _, t := range c.Tools {
			if !seen[t] {
				seen[t] = true
				all = append(all, t)
			}
		}
	}
	if empty && len(all) == 0 {
		return nil // unrestricted
	}
	return all
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
	if v := meta["author"]; v != "" {
		sk.Author = v
	}
	if v := meta["version"]; v != "" {
		sk.Version = v
	}
	if v := meta["tags"]; v != "" {
		sk.Tags = splitCSV(v)
	}
	if v := meta["triggers"]; v != "" {
		sk.Triggers = splitCSV(v)
	}
	if v := meta["tools"]; v != "" {
		sk.Tools = splitCSV(v)
	}
	if v := meta["depends"]; v != "" {
		sk.Depends = splitCSV(v)
	}
	if v := meta["dependencies"]; v != "" {
		sk.Depends = append(sk.Depends, splitCSV(v)...)
	}
	if v := meta["argument-hint"]; v != "" {
		sk.ArgumentHint = v
	}
	if v := meta["allowed-tools"]; v != "" {
		sk.Tools = append(sk.Tools, splitCSV(v)...)
	}
	if v := meta["license"]; v != "" {
		sk.License = v
	}
	if v := meta["compatibility"]; v != "" {
		sk.Compatibility = v
	}
	if v := meta["model"]; v != "" {
		sk.Model = v
	}
	if v := meta["effort"]; v != "" {
		sk.Effort = v
	}
	if v := meta["user-invocable"]; v != "" {
		b := v == "true" || v == "yes"
		sk.UserInvocable = &b
	}
	if v := meta["disable-model-invocation"]; v != "" {
		b := v == "true" || v == "yes"
		sk.DisableModelInv = &b
	}
	if v := meta["metadata"]; v != "" {
		sk.Metadata = parseMetadataMap(v)
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

// parseMetadataMap parses a simple "key=val,key2=val2" metadata string.
func parseMetadataMap(s string) map[string]string {
	m := map[string]string{}
	if s == "" {
		return m
	}
	for _, pair := range strings.Split(s, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) == 2 {
			m[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
		}
	}
	return m
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
