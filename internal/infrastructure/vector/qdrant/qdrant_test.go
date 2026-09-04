package qdrant

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/spray272598/code-agent/internal/domain/vector"
)

// fakeRT is an http.RoundTripper that simulates a Qdrant server in-process. It
// avoids a live socket (and the connection-flush quirks of httptest in some
// environments) while still exercising the adapter's request construction,
// JSON bodies, point-id mapping and filter building.
type fakeRT struct {
	mu      sync.Mutex
	calls   []string
	created map[string]bool
	points  map[string][]map[string]any
	fail500 bool
}

func newFakeRT() *fakeRT {
	return &fakeRT{
		created: map[string]bool{},
		points:  map[string][]map[string]any{},
	}
}

func (f *fakeRT) RoundTrip(req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.calls = append(f.calls, req.Method+" "+req.URL.Path)
	f.mu.Unlock()

	path := strings.Trim(req.URL.Path, "/")
	parts := strings.Split(path, "/")

	resp := func(status int, body string) (*http.Response, error) {
		return &http.Response{
			StatusCode: status,
			Body:       io.NopCloser(strings.NewReader(body)),
			Header:     http.Header{"Content-Type": []string{"application/json"}},
		}, nil
	}

	switch {
	case len(parts) == 2 && parts[0] == "collections":
		name := parts[1]
		switch req.Method {
		case http.MethodGet:
			f.mu.Lock()
			exists := f.created[name]
			f.mu.Unlock()
			if !exists {
				return resp(http.StatusNotFound, `{"status":"ok","result":null}`)
			}
			return resp(http.StatusOK, `{"status":"ok","result":{"status":"green","vectors":{"size":3}}}`)
		case http.MethodPut:
			f.mu.Lock()
			f.created[name] = true
			f.mu.Unlock()
			return resp(http.StatusOK, `{"status":"ok","result":null}`)
		case http.MethodDelete:
			f.mu.Lock()
			delete(f.created, name)
			delete(f.points, name)
			f.mu.Unlock()
			return resp(http.StatusOK, `{"status":"ok","result":null}`)
		}
	case len(parts) == 3 && parts[0] == "collections" && parts[2] == "points" && req.Method == http.MethodPut:
		name := parts[1]
		body, _ := io.ReadAll(req.Body)
		var payload struct {
			Points []map[string]any `json:"points"`
		}
		_ = json.Unmarshal(body, &payload)
		f.mu.Lock()
		f.points[name] = append(f.points[name], payload.Points...)
		f.mu.Unlock()
		return resp(http.StatusOK, `{"status":"ok","result":null}`)
	case len(parts) == 4 && parts[0] == "collections" && parts[2] == "points" && parts[3] == "search" && req.Method == http.MethodPost:
		if f.fail500 {
			return resp(http.StatusInternalServerError, `{"status":"error","status":{"error":"boom"}}`)
		}
		name := parts[1]
		f.mu.Lock()
		stored := f.points[name]
		f.mu.Unlock()
		out := map[string]any{"status": "ok", "result": []map[string]any{}}
		res := out["result"].([]map[string]any)
		for _, p := range stored {
			res = append(res, map[string]any{
				"id":      p["id"],
				"score":   0.99,
				"payload": p["payload"],
			})
		}
		out["result"] = res
		buf, _ := json.Marshal(out)
		return resp(http.StatusOK, string(buf))
	case len(parts) == 4 && parts[0] == "collections" && parts[2] == "points" && parts[3] == "delete" && req.Method == http.MethodPost:
		return resp(http.StatusOK, `{"status":"ok","result":null}`)
	}
	return resp(http.StatusNotFound, `{"status":"error","status":{"error":"not found"}}`)
}

func newTestIndex(t *testing.T, rt *fakeRT) *QdrantIndex {
	t.Helper()
	idx, err := New("http://qdrant.test", "", 3, 0)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	idx.client.Transport = rt
	return idx
}

func TestQdrantEnsureCreatesCollection(t *testing.T) {
	rt := newFakeRT()
	idx := newTestIndex(t, rt)
	if err := idx.Ensure(context.Background(), "memories", 3); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	rt.mu.Lock()
	created := rt.created["memories"]
	rt.mu.Unlock()
	if !created {
		t.Fatal("collection not created")
	}
	// Ensure is idempotent (second call must not error)
	if err := idx.Ensure(context.Background(), "memories", 3); err != nil {
		t.Fatalf("Ensure idempotent: %v", err)
	}
}

func TestQdrantUpsertPreservesID(t *testing.T) {
	rt := newFakeRT()
	idx := newTestIndex(t, rt)
	_ = idx.Ensure(context.Background(), "memories", 3)
	pts := []vector.Point{
		{ID: "mem-1", Vector: []float32{0.1, 0.2, 0.3}, Payload: map[string]any{"user_id": "u1", "content": "hello"}},
	}
	if err := idx.Upsert(context.Background(), "memories", pts); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	got := rt.points["memories"]
	if len(got) != 1 {
		t.Fatalf("expected 1 point, got %d", len(got))
	}
	if got[0]["payload"].(map[string]any)["_id"] != "mem-1" {
		t.Fatalf("original id not preserved in payload: %v", got[0]["payload"])
	}
}

func TestQdrantSearchReturnsOriginalID(t *testing.T) {
	rt := newFakeRT()
	idx := newTestIndex(t, rt)
	_ = idx.Ensure(context.Background(), "memories", 3)
	_ = idx.Upsert(context.Background(), "memories", []vector.Point{
		{ID: "mem-42", Vector: []float32{0.1, 0.2, 0.3}, Payload: map[string]any{"user_id": "u1"}},
	})
	hits, err := idx.Search(context.Background(), "memories", []float32{0.1, 0.2, 0.3}, 5, map[string]any{"user_id": "u1"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 1 {
		t.Fatalf("expected 1 hit, got %d", len(hits))
	}
	if hits[0].ID != "mem-42" {
		t.Fatalf("expected original id mem-42, got %q", hits[0].ID)
	}
	if hits[0].Score != 0.99 {
		t.Fatalf("unexpected score %v", hits[0].Score)
	}
}

func TestQdrantSearchServerError(t *testing.T) {
	rt := newFakeRT()
	rt.fail500 = true
	idx := newTestIndex(t, rt)
	hits, err := idx.Search(context.Background(), "memories", []float32{0.1, 0.2, 0.3}, 5, nil)
	if err == nil {
		t.Fatal("expected error on 500")
	}
	if hits != nil {
		t.Fatalf("expected nil hits on error, got %v", hits)
	}
}

func TestQdrantDelete(t *testing.T) {
	rt := newFakeRT()
	idx := newTestIndex(t, rt)
	_ = idx.Ensure(context.Background(), "memories", 3)
	if err := idx.Delete(context.Background(), "memories", "mem-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestPointIDStable(t *testing.T) {
	a := pointID("codeagent/internal/foo.go#0")
	b := pointID("codeagent/internal/foo.go#0")
	if a != b {
		t.Fatalf("pointID not stable: %d != %d", a, b)
	}
	if pointID("x") == pointID("y") {
		t.Fatal("pointID collides for distinct inputs")
	}
}

func TestBuildFilter(t *testing.T) {
	f := buildFilter(map[string]any{"user_id": "u1", "scope": "global"})
	must, ok := f["must"].([]map[string]any)
	if !ok || len(must) != 2 {
		t.Fatalf("unexpected filter shape: %v", f)
	}
	// Look up by key rather than by index: the assertion must not depend on
	// iteration order even though buildFilter now sorts.
	got := make(map[string]any, len(must))
	for _, m := range must {
		match, ok := m["match"].(map[string]any)
		if !ok {
			t.Fatalf("clause missing match: %v", m)
		}
		got[m["key"].(string)] = match["value"]
	}
	if got["user_id"] != "u1" || got["scope"] != "global" {
		t.Fatalf("filter key/value not mapped: %v", got)
	}
}

// TestBuildFilterDeterministic guards request-body stability across runs.
// Go randomises map iteration order, so an unsorted buildFilter flips the
// clause order roughly half the time.
func TestBuildFilterDeterministic(t *testing.T) {
	in := map[string]any{"user_id": "u1", "scope": "global", "kind": "code", "lang": "go"}
	want := buildFilter(in)
	for i := 0; i < 50; i++ {
		got := buildFilter(in)
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("buildFilter not deterministic on iteration %d:\n want %v\n got  %v", i, want, got)
		}
	}
	keys := make([]string, 0, len(in))
	for _, m := range want["must"].([]map[string]any) {
		keys = append(keys, m["key"].(string))
	}
	if !sort.StringsAreSorted(keys) {
		t.Fatalf("filter keys not sorted: %v", keys)
	}
}

func ExampleQdrantIndex() {
	// Intended usage sketch (not executed):
	idx, _ := New("http://localhost:6333", "", 1536, 0)
	_ = idx.Ensure(context.Background(), "code", 1536)
	_ = idx.Upsert(context.Background(), "code", []vector.Point{
		{ID: "internal/foo.go#0", Vector: []float32{0.1}, Payload: map[string]any{"rel_path": "internal/foo.go"}},
	})
	hits, _ := idx.Search(context.Background(), "code", []float32{0.1}, 5, nil)
	fmt.Println(len(hits))
}
