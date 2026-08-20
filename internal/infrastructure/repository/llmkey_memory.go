package repository

import (
	"context"
	"sync"

	"github.com/spray272598/code-agent/internal/domain/kms"
	"github.com/spray272598/code-agent/internal/domain/llmkey"
	"github.com/spray272598/code-agent/internal/domain/tenant"
	kmsinfra "github.com/spray272598/code-agent/internal/infrastructure/kms"
)

// MemoryLLMKeyRepo is the Sprint 2.3 in-memory implementation. api_key and
// api_base are stored as ciphertext via the KMS sealer; tests use a fresh
// repo per case.
type MemoryLLMKeyRepo struct {
	mu     sync.RWMutex
	data   map[string]map[string]storedKey // userID -> alias -> entry
	sealer kms.CryptoSealer
}

// storedKey keeps ciphertext in-memory; the Repository interface hands back
// Key with plaintext filled in by the wrapper.
type storedKey struct {
	Provider string
	APIKeyCT string // kms:v1:<base64>
	APIBaseCT string
	Enabled  bool
}

// NewMemoryLLMKeyRepo returns an empty in-memory repo.
func NewMemoryLLMKeyRepo(sealer kms.CryptoSealer) *MemoryLLMKeyRepo {
	if sealer == nil {
		panic("NewMemoryLLMKeyRepo: sealer required")
	}
	return &MemoryLLMKeyRepo{
		data:   make(map[string]map[string]storedKey),
		sealer: sealer,
	}
}

func (r *MemoryLLMKeyRepo) Save(ctx context.Context, k llmkey.Key) error {
	if k.UserID == "" {
		return llmkey.ErrTenantMissing
	}
	if k.Alias == "" {
		return llmkey.ErrDuplicate // reuse to keep the API surface small
	}
	ct, err := r.sealer.Encrypt(ctx, []byte(k.APIKey))
	if err != nil {
		return err
	}
	var baseCT string
	if k.APIBase != "" {
		b, err := r.sealer.Encrypt(ctx, []byte(k.APIBase))
		if err != nil {
			return err
		}
		baseCT = "kms:v1:" + kmsinfra.Encode(b)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	bucket, ok := r.data[k.UserID]
	if !ok {
		bucket = make(map[string]storedKey)
		r.data[k.UserID] = bucket
	}
	if _, exists := bucket[k.Alias]; exists {
		return llmkey.ErrDuplicate
	}
	bucket[k.Alias] = storedKey{
		Provider:  k.Provider,
		APIKeyCT:  "kms:v1:" + kmsinfra.Encode(ct),
		APIBaseCT: baseCT,
		Enabled:   k.Enabled,
	}
	return nil
}

func (r *MemoryLLMKeyRepo) Get(ctx context.Context, userID, alias string) (*llmkey.Key, error) {
	r.mu.RLock()
	bucket, ok := r.data[userID]
	if !ok {
		r.mu.RUnlock()
		return nil, llmkey.ErrNotFound
	}
	stored, ok := bucket[alias]
	r.mu.RUnlock()
	if !ok {
		return nil, llmkey.ErrNotFound
	}
	out, err := r.decrypt(ctx, userID, alias, stored)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r *MemoryLLMKeyRepo) ListByUser(ctx context.Context, userID string) ([]llmkey.Key, error) {
	r.mu.RLock()
	bucket, ok := r.data[userID]
	if !ok {
		r.mu.RUnlock()
		return []llmkey.Key{}, nil
	}
	cp := make([]storedKey, 0, len(bucket))
	for _, v := range bucket {
		cp = append(cp, v)
	}
	r.mu.RUnlock()
	out := make([]llmkey.Key, 0, len(cp))
	for _, st := range cp {
		k, err := r.decrypt(ctx, userID, "", st)
		if err != nil {
			return nil, err
		}
		out = append(out, *k)
	}
	return out, nil
}

func (r *MemoryLLMKeyRepo) Delete(_ context.Context, userID, alias string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if bucket, ok := r.data[userID]; ok {
		delete(bucket, alias)
	}
	return nil
}

func (r *MemoryLLMKeyRepo) decrypt(ctx context.Context, userID, alias string, st storedKey) (*llmkey.Key, error) {
	apiKey, err := decryptStored(ctx, r.sealer, st.APIKeyCT)
	if err != nil {
		return nil, err
	}
	apiBase, err := decryptStored(ctx, r.sealer, st.APIBaseCT)
	if err != nil {
		return nil, err
	}
	return &llmkey.Key{
		UserID:   userID,
		Alias:    alias,
		Provider: st.Provider,
		APIKey:   apiKey,
		APIBase:  apiBase,
		Enabled:  st.Enabled,
	}, nil
}

// sentinel so the tenant package is referenced (some envs need a real use).
var _ = tenant.MustFrom