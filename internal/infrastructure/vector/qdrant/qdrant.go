// Package qdrant implements domain/vector.IVectorIndex over the Qdrant REST
// API. It depends only on the standard library (net/http) so it builds and runs
// anywhere Qdrant is reachable over HTTP, with no extra client dependency.
//
// Point IDs are opaque strings at the domain layer. Qdrant requires numeric or
// UUID point ids, so we map each string id to a stable uint64 (FNV-1a) and keep
// the original id in the point payload under the "_id" key. Search results
// surface the original string id back to callers.
package qdrant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/spray272598/code-agent/internal/domain/vector"
)

// QdrantIndex is an IVectorIndex backed by a Qdrant server.
type QdrantIndex struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

// New builds a Qdrant-backed vector index. baseURL is the Qdrant root
// (e.g. http://localhost:6333). apiKey may be empty for unsecured instances.
// dim is the embedding dimension used when creating collections.
func New(baseURL, apiKey string, dim int, timeout time.Duration) (*QdrantIndex, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, fmt.Errorf("qdrant: empty base URL")
	}
	if dim <= 0 {
		return nil, fmt.Errorf("qdrant: invalid dimension %d", dim)
	}
	base := strings.TrimRight(baseURL, "/")
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &QdrantIndex{
		baseURL: base,
		apiKey:  apiKey,
		client:  &http.Client{Timeout: timeout},
	}, nil
}

// Ensure idempotently creates the collection with the given dimension (cosine
// distance). If the collection already exists with a different vector size it
// is recreated, since Qdrant cannot resize vectors in place.
func (q *QdrantIndex) Ensure(ctx context.Context, collection string, dim int) error {
	if collection == "" {
		return fmt.Errorf("qdrant: empty collection")
	}
	exists, curDim, err := q.collectionInfo(ctx, collection)
	if err != nil {
		return err
	}
	if !exists {
		return q.createCollection(ctx, collection, dim)
	}
	if curDim > 0 && curDim != dim {
		if derr := q.deleteCollection(ctx, collection); derr != nil {
			return derr
		}
		return q.createCollection(ctx, collection, dim)
	}
	return nil
}

func (q *QdrantIndex) Upsert(ctx context.Context, collection string, points []vector.Point) error {
	if collection == "" || len(points) == 0 {
		return nil
	}
	pts := make([]map[string]any, 0, len(points))
	for _, p := range points {
		payload := p.Payload
		if payload == nil {
			payload = map[string]any{}
		}
		// preserve the original string id for round-tripping
		payload["_id"] = p.ID
		pts = append(pts, map[string]any{
			"id":      pointID(p.ID),
			"vector":  p.Vector,
			"payload": payload,
		})
	}
	body, err := json.Marshal(map[string]any{"points": pts})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		q.baseURL+"/collections/"+collection+"/points?wait=true", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	q.auth(req)
	resp, err := q.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("qdrant: upsert %q: status %d %s", collection, resp.StatusCode, string(b))
	}
	return nil
}

func (q *QdrantIndex) Search(ctx context.Context, collection string, query []float32, topK int, filter map[string]any) ([]vector.Hit, error) {
	if collection == "" || len(query) == 0 {
		return nil, nil
	}
	if topK <= 0 {
		topK = 8
	}
	reqBody := map[string]any{
		"vector":       query,
		"limit":        topK,
		"with_payload": true,
	}
	if len(filter) > 0 {
		reqBody["filter"] = buildFilter(filter)
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		q.baseURL+"/collections/"+collection+"/points/search", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	q.auth(req)
	resp, err := q.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("qdrant: search %q: status %d %s", collection, resp.StatusCode, string(b))
	}
	var out struct {
		Result []struct {
			ID      uint64         `json:"id"`
			Score   float32        `json:"score"`
			Payload map[string]any `json:"payload"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	hits := make([]vector.Hit, 0, len(out.Result))
	for _, r := range out.Result {
		id := strconv.FormatUint(r.ID, 10)
		if s, ok := r.Payload["_id"].(string); ok && s != "" {
			id = s
		}
		hits = append(hits, vector.Hit{ID: id, Score: r.Score, Payload: r.Payload})
	}
	return hits, nil
}

func (q *QdrantIndex) Delete(ctx context.Context, collection, id string) error {
	if collection == "" || id == "" {
		return nil
	}
	body, err := json.Marshal(map[string]any{"points": []uint64{pointID(id)}})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		q.baseURL+"/collections/"+collection+"/points/delete?wait=true", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	q.auth(req)
	resp, err := q.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("qdrant: delete %q: status %d %s", collection, resp.StatusCode, string(b))
	}
	return nil
}

// collectionInfo reports whether a collection exists and its current vector size.
func (q *QdrantIndex) collectionInfo(ctx context.Context, name string) (exists bool, dim int, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, q.baseURL+"/collections/"+name, nil)
	if err != nil {
		return false, 0, err
	}
	q.auth(req)
	resp, err := q.client.Do(req)
	if err != nil {
		return false, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return false, 0, nil
	}
	if resp.StatusCode != http.StatusOK {
		return false, 0, fmt.Errorf("qdrant: get collection %q: status %d", name, resp.StatusCode)
	}
	var body struct {
		Result struct {
			Vectors struct {
				Size int `json:"size"`
			} `json:"vectors"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return true, 0, err
	}
	return true, body.Result.Vectors.Size, nil
}

func (q *QdrantIndex) createCollection(ctx context.Context, name string, dim int) error {
	payload := map[string]any{"vectors": map[string]any{"size": dim, "distance": "Cosine"}}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		q.baseURL+"/collections/"+name, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	q.auth(req)
	resp, err := q.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("qdrant: create collection %q: status %d %s", name, resp.StatusCode, string(b))
	}
	return nil
}

func (q *QdrantIndex) deleteCollection(ctx context.Context, name string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, q.baseURL+"/collections/"+name, nil)
	if err != nil {
		return err
	}
	q.auth(req)
	resp, err := q.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("qdrant: delete collection %q: status %d %s", name, resp.StatusCode, string(b))
	}
	return nil
}

func (q *QdrantIndex) auth(req *http.Request) {
	if q.apiKey != "" {
		req.Header.Set("api-key", q.apiKey)
	}
}

// pointID maps a string id to a stable uint64 Qdrant point id.
func pointID(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

// buildFilter converts domain filter key/values into Qdrant's filter DSL.
func buildFilter(f map[string]any) map[string]any {
	must := make([]map[string]any, 0, len(f))
	for k, v := range f {
		must = append(must, map[string]any{
			"key":   k,
			"match": map[string]any{"value": v},
		})
	}
	return map[string]any{"must": must}
}

// compile-time interface check
var _ vector.IVectorIndex = (*QdrantIndex)(nil)
