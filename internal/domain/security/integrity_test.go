package security

import (
	"testing"
	"time"
)

func TestIntegrityChain_AppendAndVerify(t *testing.T) {
	ic := NewIntegrityChain()

	e1 := ic.Append("tool_call", "s1", "user1", "read_file executed", map[string]any{"tool": "read_file", "path": "test.txt"})
	e2 := ic.Append("deny", "s1", "user1", "denied by policy", map[string]any{"rule": "rm_rf_root"})
	e3 := ic.Append("allow", "s1", "user1", "allowed", map[string]any{"tool": "read_file"})

	if e1.Index != 1 || e2.Index != 2 || e3.Index != 3 {
		t.Error("indexes should be sequential")
	}

	if e1.Hash == "" || e2.Hash == "" || e3.Hash == "" {
		t.Error("all entries should have hashes")
	}

	// Verify chain
	pending := ic.Pending()
	if len(pending) != 3 {
		t.Fatalf("Pending() returned %d entries want 3", len(pending))
	}

	result := ic.Verify(pending)
	if !result.Valid {
		t.Errorf("chain should be valid, got: %s", result.FirstBadReason)
	}
	if result.ChainLength != 3 {
		t.Errorf("ChainLength=%d want 3", result.ChainLength)
	}
}

func TestIntegrityChain_HashChaining(t *testing.T) {
	ic := NewIntegrityChain()

	e1 := ic.Append("action1", "s1", "", "detail1", nil)
	e2 := ic.Append("action2", "s1", "", "detail2", nil)

	// e2's PrevHash should equal e1's Hash
	if e2.PrevHash != e1.Hash {
		t.Error("e2.PrevHash should equal e1.Hash")
	}

	// CurrentHash should be e2's Hash
	if ic.CurrentHash() != e2.Hash {
		t.Error("CurrentHash should be e2.Hash")
	}
}

func TestIntegrityChain_TamperDetection(t *testing.T) {
	ic := NewIntegrityChain()

	e1 := ic.Append("action1", "s1", "", "detail1", nil)
	_ = ic.Append("action2", "s1", "", "detail2", nil)
	_ = e1

	pending := ic.Pending()

	// Tamper with an entry
	pending[0].Detail = "tampered"

	result := ic.Verify(pending)
	if result.Valid {
		t.Error("tampered chain should be detected as invalid")
	}
	if result.FirstBadIndex != 1 {
		t.Errorf("FirstBadIndex=%d want 1", result.FirstBadIndex)
	}
}

func TestIntegrityChain_InsertDetection(t *testing.T) {
	ic := NewIntegrityChain()

	_ = ic.Append("action1", "s1", "", "detail1", nil)
	_ = ic.Append("action2", "s1", "", "detail2", nil)

	pending := ic.Pending()
	if len(pending) != 2 {
		t.Fatalf("expected 2 entries got %d", len(pending))
	}

	// Try to insert a fake entry
	fakeEntry := IntegrityEntry{
		Index:     1,
		Timestamp: time.Now().UTC(),
		Action:    "fake",
		PrevHash:  pending[0].PrevHash,
	}
	fakeEntry.Hash = computeEntryHash(fakeEntry)

	tampered := append([]IntegrityEntry{fakeEntry}, pending...)
	result := ic.Verify(tampered)
	if result.Valid {
		t.Error("inserted entry should be detected")
	}
}

func TestIntegrityChain_AppendBatch(t *testing.T) {
	ic := NewIntegrityChain()

	batch := []IntegrityEntry{
		{Action: "batch1", SessionID: "s1", Detail: "detail1"},
		{Action: "batch2", SessionID: "s1", Detail: "detail2"},
		{Action: "batch3", SessionID: "s1", Detail: "detail3"},
	}

	results := ic.AppendBatch(batch)
	if len(results) != 3 {
		t.Fatalf("AppendBatch returned %d entries want 3", len(results))
	}

	// Verify all entries
	pending := ic.Pending()
	result := ic.Verify(pending)
	if !result.Valid {
		t.Errorf("batch chain should be valid, got: %s", result.FirstBadReason)
	}

	if result.ChainLength != 3 {
		t.Errorf("ChainLength=%d want 3", result.ChainLength)
	}
}

func TestIntegrityChain_EmptyVerification(t *testing.T) {
	ic := NewIntegrityChain()

	// Verify empty chain
	result := ic.Verify(nil)
	if !result.Valid {
		t.Error("empty chain should be valid")
	}
	if result.ChainLength != 0 {
		t.Errorf("ChainLength=%d want 0", result.ChainLength)
	}
}

func TestIntegrityChain_Serialization(t *testing.T) {
	ic := NewIntegrityChain()

	ic.Append("action1", "s1", "", "detail1", map[string]any{"key": "value"})
	ic.Append("action2", "s1", "", "detail2", nil)

	pending := ic.Pending()

	// Serialize and deserialize
	data, err := SerializeEntries(pending)
	if err != nil {
		t.Fatalf("SerializeEntries error: %v", err)
	}

	restored, err := DeserializeEntries(data)
	if err != nil {
		t.Fatalf("DeserializeEntries error: %v", err)
	}

	if len(restored) != len(pending) {
		t.Errorf("restored=%d entries want %d", len(restored), len(pending))
	}

	// Verify restored entries
	ic2 := NewIntegrityChain()
	result := ic2.Verify(restored)
	if !result.Valid {
		t.Error("restored entries should form valid chain")
	}
}

func TestIntegrityChain_VerifyFromHash(t *testing.T) {
	ic := NewIntegrityChain()

	e1 := ic.Append("action1", "s1", "", "detail1", nil)
	_ = ic.Append("action2", "s1", "", "detail2", nil)

	pending := ic.Pending()

	// Verify from genesis hash
	result := ic.VerifyFromHash(pending, ic.genesisHash)
	if !result.Valid {
		t.Error("verification from genesis should pass")
	}

	// Verify from e1's hash (should start from e2)
	result2 := ic.VerifyFromHash(pending[1:], e1.Hash)
	if !result2.Valid {
		t.Error("verification from e1 hash should pass for remaining entries")
	}

	// Verify from wrong hash (should fail)
	result3 := ic.VerifyFromHash(pending, "invalidhash")
	if result3.Valid {
		t.Error("verification from invalid hash should fail")
	}
}

func TestIntegrityChain_LastVerification(t *testing.T) {
	ic := NewIntegrityChain()

	ic.Append("action1", "s1", "", "detail1", nil)
	pending := ic.Pending()

	result := ic.Verify(pending)
	if !result.Valid {
		t.Fatal("chain should be valid")
	}

	hash, at, ok := ic.LastVerification()
	if !ok {
		t.Error("LastVerification should be OK after successful verify")
	}
	if hash == "" {
		t.Error("LastVerification hash should not be empty")
	}
	if at.IsZero() {
		t.Error("LastVerification time should not be zero")
	}
}

func TestIntegrityChain_CurrentHash(t *testing.T) {
	ic := NewIntegrityChain()

	initial := ic.CurrentHash()

	ic.Append("action1", "s1", "", "detail1", nil)
	after1 := ic.CurrentHash()

	if initial == after1 {
		t.Error("hash should change after append")
	}

	ic.Append("action2", "s1", "", "detail2", nil)
	after2 := ic.CurrentHash()

	if after1 == after2 {
		t.Error("hash should change after second append")
	}
}

func TestIntegrityChain_EntryCount(t *testing.T) {
	ic := NewIntegrityChain()

	if ic.EntryCount() != 0 {
		t.Errorf("initial EntryCount=%d want 0", ic.EntryCount())
	}

	ic.Append("a1", "", "", "", nil)
	ic.Append("a2", "", "", "", nil)
	ic.Append("a3", "", "", "", nil)

	if ic.EntryCount() != 3 {
		t.Errorf("EntryCount=%d want 3", ic.EntryCount())
	}
}
