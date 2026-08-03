package memory_test

import (
	"context"
	"testing"

	"github.com/spray272598/code-agent/internal/domain/memory"
	memport "github.com/spray272598/code-agent/internal/domain/memory/adapter/port"
	"github.com/spray272598/code-agent/internal/infrastructure/repository"
)

func TestMemorySaveSearchFormat(t *testing.T) {
	repo := repository.NewMemoryCoreRepo()
	svc := memory.NewService(repo)
	ctx := context.Background()
	err := svc.Save(ctx, &memport.MemoryItem{
		UserID: "u1", Scope: memport.ScopeUser, Category: "pref",
		Content: "prefer go test over manual", Importance: 80, Source: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = svc.Save(ctx, &memport.MemoryItem{
		UserID: "u1", ProjectID: "p1", Scope: memport.ScopeProject,
		Category: "build", Content: "use make build", Importance: 60,
	})
	list, err := svc.Search(ctx, "u1", "p1", "go test", 5)
	if err != nil || len(list) == 0 {
		t.Fatalf("search: %v %#v", err, list)
	}
	prompt := svc.FormatForPrompt(ctx, "u1", "p1", "how to test", 5)
	if prompt == "" {
		t.Fatal("expected prompt block")
	}
}
