package skill

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func writeMarketSkill(t *testing.T, dir, id, md string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, id), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id, "SKILL.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
}

func sampleMarketMD() string {
	return "---\nname: Git Helper\nid: git-helper\ndescription: Automate git workflows\nauthor: acme\nversion: 1.0.0\ntags: git, vcs\n---\nGuide body.\n"
}

func TestLocalMarketplace_List(t *testing.T) {
	dir := t.TempDir()
	writeMarketSkill(t, dir, "git-helper", sampleMarketMD())
	mkt := NewLocalMarketplace(dir)
	ls, err := mkt.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(ls) != 1 || ls[0].ID != "git-helper" || ls[0].Author != "acme" {
		t.Fatalf("unexpected listing: %+v", ls)
	}
	if len(ls[0].Tags) != 2 {
		t.Fatalf("tags not parsed: %+v", ls[0].Tags)
	}
}

func TestSearchMarket_MarksInstalled(t *testing.T) {
	marketDir := t.TempDir()
	writeMarketSkill(t, marketDir, "git-helper", sampleMarketMD())

	localDir := t.TempDir()
	svc := NewService(localDir)
	svc.SetMarketplace(NewLocalMarketplace(marketDir))

	// Not installed yet.
	res, err := svc.SearchMarket(context.Background(), "")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(res) != 1 || res[0].Installed {
		t.Fatalf("expected not-installed listing: %+v", res)
	}

	// Install from marketplace → now marked installed.
	if _, err := svc.InstallListing(context.Background(), "git-helper"); err != nil {
		t.Fatalf("install: %v", err)
	}
	res, _ = svc.SearchMarket(context.Background(), "")
	if !res[0].Installed {
		t.Fatalf("expected installed=true after InstallListing: %+v", res)
	}
}

func TestSearchMarket_Query(t *testing.T) {
	marketDir := t.TempDir()
	writeMarketSkill(t, marketDir, "git-helper", sampleMarketMD())
	writeMarketSkill(t, marketDir, "docker-helper", "---\nname: Docker Helper\nid: docker-helper\ndescription: Container ops\n---\nbody\n")
	svc := NewService(t.TempDir())
	svc.SetMarketplace(NewLocalMarketplace(marketDir))

	res, err := svc.SearchMarket(context.Background(), "git")
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 1 || res[0].ID != "git-helper" {
		t.Fatalf("query 'git' should match 1: %+v", res)
	}
	res, _ = svc.SearchMarket(context.Background(), "container")
	if len(res) != 1 || res[0].ID != "docker-helper" {
		t.Fatalf("query 'container' should match docker: %+v", res)
	}
	res, _ = svc.SearchMarket(context.Background(), "zzz")
	if len(res) != 0 {
		t.Fatalf("query 'zzz' should match none: %+v", res)
	}
}

func TestUploadSkill_Validation(t *testing.T) {
	svc := NewService(t.TempDir())

	// Missing name.
	if _, err := svc.UploadSkill(context.Background(), "x", "---\ndescription: no name\n---\nbody\n"); err == nil {
		t.Fatal("expected error for missing name")
	}
	// Missing description.
	if _, err := svc.UploadSkill(context.Background(), "x", "---\nname: X\n---\nbody\n"); err == nil {
		t.Fatal("expected error for missing description")
	}
	// Missing id.
	if _, err := svc.UploadSkill(context.Background(), "", "---\nname: X\ndescription: d\n---\nbody\n"); err == nil {
		t.Fatal("expected error for missing id")
	}
}

func TestUploadSkill_SuccessAndMatchable(t *testing.T) {
	svc := NewService(t.TempDir())
	md := "---\nname: My Linter\nid: my-linter\ndescription: Custom lint skill\nauthor: you\ntags: lint\n---\nRun the linter.\n"
	sk, err := svc.UploadSkill(context.Background(), "my-linter", md)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if sk == nil || sk.ID != "my-linter" || sk.Author != "you" {
		t.Fatalf("uploaded skill wrong: %+v", sk)
	}
	// It should now be matchable by its trigger/name.
	if got := svc.Match("run my linter"); got == nil || got.ID != "my-linter" {
		t.Fatalf("uploaded skill should be matchable, got %+v", got)
	}
}
