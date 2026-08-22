package skill

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Marketplace is the catalog source for the skills market (3.2). It is
// intentionally transport-agnostic: the local implementation reads a bundled
// directory; a future remote implementation can talk to a registry over HTTP
// without touching Service.
type Marketplace interface {
	// List returns all catalog entries (metadata only; not necessarily installed).
	List(ctx context.Context) ([]SkillListing, error)
	// SourceDir returns the on-disk directory of a catalog entry, used by
	// InstallListing to copy it into the local skills root.
	SourceDir(id string) (string, error)
}

// LocalMarketplace reads listings from a directory of skill folders, each
// containing a SKILL.md. It never installs anything — it only catalogs.
type LocalMarketplace struct {
	dir string
}

// NewLocalMarketplace builds a marketplace backed by dir. A missing dir yields
// an empty (but valid) catalog.
func NewLocalMarketplace(dir string) *LocalMarketplace {
	if dir == "" {
		dir = "./skills/market"
	}
	return &LocalMarketplace{dir: dir}
}

// List scans the catalog directory and produces one listing per valid skill.
func (m *LocalMarketplace) List(_ context.Context) ([]SkillListing, error) {
	_ = os.MkdirAll(m.dir, 0o755)
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return nil, err
	}
	out := make([]SkillListing, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sk, err := parseSkillDir(filepath.Join(m.dir, e.Name()))
		if err != nil || sk == nil {
			continue
		}
		out = append(out, listingFromSkill(sk, "market"))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func listingFromSkill(sk *Skill, source string) SkillListing {
	return SkillListing{
		ID:          sk.ID,
		Name:        sk.Name,
		Description: sk.Description,
		Author:      sk.Author,
		Version:     sk.Version,
		Tags:        sk.Tags,
		Source:      source,
	}
}

// SetMarketplace attaches a catalog source used by SearchMarket.
func (s *Service) SetMarketplace(m Marketplace) {
	s.mu.Lock()
	s.market = m
	s.mu.Unlock()
}

// SearchMarket returns catalog entries matching q across name/description/tags,
// annotating each with its locally-installed status. With an empty query it
// returns the full catalog.
func (s *Service) SearchMarket(ctx context.Context, q string) ([]SkillListing, error) {
	s.mu.RLock()
	mkt := s.market
	s.mu.RUnlock()
	if mkt == nil {
		return nil, fmt.Errorf("no marketplace configured")
	}
	all, err := mkt.List(ctx)
	if err != nil {
		return nil, err
	}
	installed := map[string]bool{}
	for _, sk := range s.List() {
		installed[sk.ID] = true
	}
	q = strings.ToLower(strings.TrimSpace(q))
	out := make([]SkillListing, 0, len(all))
	for _, l := range all {
		l.Installed = installed[l.ID]
		if q == "" {
			out = append(out, l)
			continue
		}
		hay := strings.ToLower(l.Name + " " + l.Description + " " + strings.Join(l.Tags, " "))
		if strings.Contains(hay, q) {
			out = append(out, l)
		}
	}
	return out, nil
}

// UploadSkill ingests a user-authored SKILL.md (the "custom skill upload" half
// of 3.2). It validates the front-matter, writes it under the local skills
// root, and reloads so it becomes immediately matchable. Author defaults to
// "you" when omitted.
func (s *Service) UploadSkill(ctx context.Context, id, skillMD string) (*Skill, error) {
	id = sanitize(id)
	if id == "" {
		return nil, fmt.Errorf("skill id required")
	}
	meta, body := splitFrontmatter(skillMD)
	name := meta["name"]
	if name == "" {
		return nil, fmt.Errorf("SKILL.md must declare 'name' in front-matter")
	}
	if meta["description"] == "" {
		return nil, fmt.Errorf("SKILL.md must declare 'description' in front-matter")
	}
	dstDir := filepath.Join(s.rootDir, id)
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dstDir, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
		return nil, err
	}
	if err := s.Reload(); err != nil {
		return nil, err
	}
	got := s.Get(id)
	if got == nil {
		return nil, fmt.Errorf("uploaded skill %q not found after reload", id)
	}
	got.Source = "installed"
	_ = body
	return got, nil
}

// InstallListing installs a marketplace entry (by id) into the local skills
// root. It resolves the catalog source path and reuses InstallFromPath.
func (s *Service) InstallListing(ctx context.Context, id string) (*Skill, error) {
	s.mu.RLock()
	mkt := s.market
	s.mu.RUnlock()
	if mkt == nil {
		return nil, fmt.Errorf("no marketplace configured")
	}
	id = sanitize(id)
	listings, err := mkt.List(ctx)
	if err != nil {
		return nil, err
	}
	var srcDir string
	for _, l := range listings {
		if l.ID == id {
			srcDir, _ = mkt.SourceDir(id)
			break
		}
	}
	if srcDir == "" {
		return nil, fmt.Errorf("listing %q not found in marketplace", id)
	}
	return s.InstallFromPath(srcDir, id)
}

// SourceDir returns the on-disk directory of a catalog entry.
func (m *LocalMarketplace) SourceDir(id string) (string, error) {
	id = sanitize(id)
	p := filepath.Join(m.dir, id)
	if _, err := os.Stat(p); err != nil {
		return "", err
	}
	return p, nil
}
