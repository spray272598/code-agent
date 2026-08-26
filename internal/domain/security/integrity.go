package security

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// IntegrityChain provides tamper-evident audit logging by chaining
// hash values. Each entry's hash depends on the previous entry's hash,
// making it computationally expensive to insert or modify entries.
type IntegrityChain struct {
	mu sync.Mutex

	// Genesis hash: fixed starting point for the chain
	genesisHash string

	// Current chain state
	currentHash string
	entryCount  int64

	// Pending entries waiting to be appended
	pending []IntegrityEntry

	// Verification history
	lastVerifiedHash   string
	lastVerifiedAt     time.Time
	lastVerificationOK bool
}

// IntegrityEntry is a single tamper-evident log entry.
type IntegrityEntry struct {
	Index     int64          `json:"index"`
	Timestamp time.Time      `json:"timestamp"`
	Action    string         `json:"action"`
	SessionID string         `json:"sessionId,omitempty"`
	UserID    string         `json:"userId,omitempty"`
	Detail    string         `json:"detail"`
	Data      map[string]any `json:"data,omitempty"`
	PrevHash  string         `json:"prevHash"`
	Hash      string         `json:"hash"`
}

// VerificationResult reports the integrity state of the audit chain.
type VerificationResult struct {
	Valid              bool      `json:"valid"`
	EntriesChecked     int64     `json:"entriesChecked"`
	FirstBadIndex      int64     `json:"firstBadIndex,omitempty"`
	FirstBadReason     string    `json:"firstBadReason,omitempty"`
	ChainLength        int64     `json:"chainLength"`
	CurrentHash        string    `json:"currentHash"`
	CheckedAt          time.Time `json:"checkedAt"`
	HistoryVerified    bool      `json:"historyVerified"`
	LastVerifiedHash   string    `json:"lastVerifiedHash,omitempty"`
	LastVerifiedAt     time.Time `json:"lastVerifiedAt,omitempty"`
	LastVerificationOK bool      `json:"lastVerificationOK"`
}

// NewIntegrityChain creates a new tamper-evident audit chain.
func NewIntegrityChain() *IntegrityChain {
	genesis := computeGenesisHash()
	return &IntegrityChain{
		genesisHash: genesis,
		currentHash: genesis,
		entryCount:  0,
		pending:     make([]IntegrityEntry, 0, 64),
	}
}

func computeGenesisHash() string {
	data := []byte("code-agent-integrity-genesis-v1")
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// Append adds an entry to the integrity chain with hash chaining.
func (ic *IntegrityChain) Append(action, sessionID, userID, detail string, data map[string]any) IntegrityEntry {
	ic.mu.Lock()
	defer ic.mu.Unlock()

	ic.entryCount++
	entry := IntegrityEntry{
		Index:     ic.entryCount,
		Timestamp: time.Now().UTC(),
		Action:    action,
		SessionID: sessionID,
		UserID:    userID,
		Detail:    detail,
		Data:      data,
		PrevHash:  ic.currentHash,
	}

	entry.Hash = computeEntryHash(entry)
	ic.currentHash = entry.Hash
	ic.pending = append(ic.pending, entry)

	return entry
}

// AppendBatch adds multiple entries atomically.
func (ic *IntegrityChain) AppendBatch(entries []IntegrityEntry) []IntegrityEntry {
	ic.mu.Lock()
	defer ic.mu.Unlock()

	for i := range entries {
		ic.entryCount++
		entries[i].Index = ic.entryCount
		entries[i].Timestamp = time.Now().UTC()
		entries[i].PrevHash = ic.currentHash
		entries[i].Hash = computeEntryHash(entries[i])
		ic.currentHash = entries[i].Hash
		ic.pending = append(ic.pending, entries[i])
	}

	return entries
}

// Pending returns and clears all pending entries.
func (ic *IntegrityChain) Pending() []IntegrityEntry {
	ic.mu.Lock()
	defer ic.mu.Unlock()
	result := ic.pending
	ic.pending = make([]IntegrityEntry, 0, 64)
	return result
}

// CurrentHash returns the latest chain hash.
func (ic *IntegrityChain) CurrentHash() string {
	ic.mu.Lock()
	defer ic.mu.Unlock()
	return ic.currentHash
}

// EntryCount returns the total number of entries in the chain.
func (ic *IntegrityChain) EntryCount() int64 {
	ic.mu.Lock()
	defer ic.mu.Unlock()
	return ic.entryCount
}

// Verify checks the integrity of a sequence of entries.
// Returns a result indicating whether the chain is intact.
func (ic *IntegrityChain) Verify(entries []IntegrityEntry) VerificationResult {
	return ic.VerifyFromHash(entries, ic.genesisHash)
}

// VerifyFromHash verifies entries starting from a known root hash.
func (ic *IntegrityChain) VerifyFromHash(entries []IntegrityEntry, rootHash string) VerificationResult {
	now := time.Now().UTC()
	result := VerificationResult{
		ChainLength: int64(len(entries)),
		CheckedAt:   now,
	}

	prevHash := rootHash
	for i, entry := range entries {
		result.EntriesChecked++

		// Check PrevHash chain
		if entry.PrevHash != prevHash {
			result.FirstBadIndex = int64(i + 1)
			result.FirstBadReason = fmt.Sprintf("prev_hash mismatch at entry %d: expected %s, got %s",
				i+1, shortHash(prevHash), shortHash(entry.PrevHash))
			return result
		}

		// Check entry hash integrity
		expectedHash := computeEntryHash(entry)
		if entry.Hash != expectedHash {
			result.FirstBadIndex = int64(i + 1)
			result.FirstBadReason = fmt.Sprintf("hash mismatch at entry %d: expected %s, got %s",
				i+1, shortHash(expectedHash), shortHash(entry.Hash))
			return result
		}

		prevHash = entry.Hash
	}

	result.Valid = true
	result.CurrentHash = prevHash
	result.LastVerifiedHash = prevHash
	result.LastVerifiedAt = now
	result.LastVerificationOK = true

	ic.mu.Lock()
	ic.lastVerifiedHash = prevHash
	ic.lastVerifiedAt = now
	ic.lastVerificationOK = true
	ic.mu.Unlock()

	return result
}

// ComputeHashFromEntries reconstructs the chain hash from a list of entries.
// Useful for verifying entries after reload from persistent storage.
func (ic *IntegrityChain) ComputeHashFromEntries(entries []IntegrityEntry) string {
	prevHash := ic.genesisHash
	for _, entry := range entries {
		entry.PrevHash = prevHash
		entry.Hash = computeEntryHash(entry)
		prevHash = entry.Hash
	}
	return prevHash
}

// VerifyAndUpdate verifies the current chain and updates the verification state.
func (ic *IntegrityChain) VerifyAndUpdate(allEntries []IntegrityEntry) VerificationResult {
	result := ic.Verify(allEntries)
	ic.mu.Lock()
	ic.lastVerifiedHash = result.CurrentHash
	ic.lastVerifiedAt = result.CheckedAt
	ic.lastVerificationOK = result.Valid
	ic.mu.Unlock()
	return result
}

// LastVerification returns the results of the last verification.
func (ic *IntegrityChain) LastVerification() (hash string, at time.Time, ok bool) {
	ic.mu.Lock()
	defer ic.mu.Unlock()
	return ic.lastVerifiedHash, ic.lastVerifiedAt, ic.lastVerificationOK
}

// computeEntryHash computes the SHA-256 hash of an entry's content.
// Excludes the Hash field itself from the computation to avoid circularity.
func computeEntryHash(entry IntegrityEntry) string {
	type entryForHash struct {
		Index     int64          `json:"index"`
		Timestamp time.Time      `json:"timestamp"`
		Action    string         `json:"action"`
		SessionID string         `json:"sessionId,omitempty"`
		UserID    string         `json:"userId,omitempty"`
		Detail    string         `json:"detail"`
		Data      map[string]any `json:"data,omitempty"`
		PrevHash  string         `json:"prevHash"`
	}

	h := entryForHash{
		Index:     entry.Index,
		Timestamp: entry.Timestamp.UTC(),
		Action:    entry.Action,
		SessionID: entry.SessionID,
		UserID:    entry.UserID,
		Detail:    entry.Detail,
		Data:      entry.Data,
		PrevHash:  entry.PrevHash,
	}

	data, _ := json.Marshal(h)
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

// SerializeEntries serializes integrity entries to JSON for persistence.
func SerializeEntries(entries []IntegrityEntry) ([]byte, error) {
	return json.Marshal(entries)
}

// DeserializeEntries deserializes integrity entries from persisted JSON.
func DeserializeEntries(data []byte) ([]IntegrityEntry, error) {
	var entries []IntegrityEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// shortHash safely truncates a hash string to 12 characters for display.
func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}
