package security

import (
	"testing"
	"time"
)

func TestBehaviorTracker_RapidSensitiveAccess(t *testing.T) {
	tracker := NewBehaviorTracker()
	tracker.SetRapidAccessThreshold(5*time.Minute, 3)

	// Simulate rapid access to sensitive files
	tracker.Track(BehaviorEvent{
		SessionID: "s1",
		Tool:      "read_file",
		Target:    ".ssh/id_rsa",
		Category:  "read",
	})
	tracker.Track(BehaviorEvent{
		SessionID: "s1",
		Tool:      "read_file",
		Target:    ".env/production",
		Category:  "read",
	})
	anomalies := tracker.Track(BehaviorEvent{
		SessionID: "s1",
		Tool:      "read_file",
		Target:    "credentials.json",
		Category:  "read",
	})

	found := false
	for _, a := range anomalies {
		if a.Type == AnomalyRapidSensitiveAccess {
			found = true
			if a.Severity < BehaviorHigh {
				t.Error("rapid access anomaly should be at least High severity")
			}
			break
		}
	}
	if !found {
		t.Error("expected rapid sensitive access anomaly after 3 sensitive file reads")
	}
}

func TestBehaviorTracker_DeletionBurst(t *testing.T) {
	tracker := NewBehaviorTracker()
	tracker.SetDeletionBurstThreshold(10*time.Minute, 3)

	// Simulate burst of deletion operations
	tracker.Track(BehaviorEvent{
		SessionID: "s1",
		Tool:      "delete_file",
		Target:    "file1.txt",
		Category:  "delete",
	})
	tracker.Track(BehaviorEvent{
		SessionID: "s1",
		Tool:      "remove_file",
		Target:    "file2.txt",
		Category:  "delete",
	})
	anomalies := tracker.Track(BehaviorEvent{
		SessionID: "s1",
		Tool:      "rm_tool",
		Target:    "/tmp/test",
		Category:  "delete",
	})

	found := false
	for _, a := range anomalies {
		if a.Type == AnomalyLargeScopeDeletion {
			found = true
			if a.Severity < BehaviorCritical {
				t.Error("deletion burst anomaly should be Critical severity")
			}
			break
		}
	}
	if !found {
		t.Error("expected deletion burst anomaly after 3 deletion operations")
	}
}

func TestBehaviorTracker_NetworkEgressBurst(t *testing.T) {
	tracker := NewBehaviorTracker()
	tracker.networkBurstMax = 2

	// Simulate network egress burst
	tracker.Track(BehaviorEvent{
		SessionID: "s1",
		Tool:      "curl",
		Target:    "http://external.com/data",
		Category:  "network",
	})
	anomalies := tracker.Track(BehaviorEvent{
		SessionID: "s1",
		Tool:      "wget",
		Target:    "http://evil.com/dump",
		Category:  "network",
	})

	found := false
	for _, a := range anomalies {
		if a.Type == AnomalyNetworkEgressBurst {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected network egress burst anomaly")
	}
}

func TestBehaviorTracker_CredentialAccess(t *testing.T) {
	tracker := NewBehaviorTracker()

	// First do a write operation
	tracker.Track(BehaviorEvent{
		SessionID: "s1",
		Tool:      "write_file",
		Target:    "output.txt",
		Category:  "write",
	})

	// Then access a credential file
	anomalies := tracker.Track(BehaviorEvent{
		SessionID: "s1",
		Tool:      "read_file",
		Target:    ".pem/aws-key.pem",
		Category:  "read",
	})

	found := false
	for _, a := range anomalies {
		if a.Type == AnomalyCredentialAccess {
			found = true
			if a.Severity < BehaviorCritical {
				t.Error("credential access near write should be Critical")
			}
			break
		}
	}
	if !found {
		t.Error("expected credential access anomaly after write+credential sequence")
	}
}

func TestBehaviorTracker_PathTraversal(t *testing.T) {
	tracker := NewBehaviorTracker()

	anomalies := tracker.Track(BehaviorEvent{
		SessionID: "s1",
		Tool:      "read_file",
		Target:    "../../etc/passwd",
		Category:  "read",
	})

	found := false
	for _, a := range anomalies {
		if a.Type == AnomalyCrossBoundaryAccess {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected path traversal anomaly for ../ patterns")
	}
}

func TestBehaviorTracker_ReadThenWriteJump(t *testing.T) {
	tracker := NewBehaviorTracker()

	// Read sensitive data
	tracker.Track(BehaviorEvent{
		SessionID: "s1",
		Tool:      "read_file",
		Target:    ".env/secrets",
		Category:  "read",
	})

	// Then write to a new location
	anomalies := tracker.Track(BehaviorEvent{
		SessionID: "s1",
		Tool:      "write_file",
		Target:    "output_dump.txt",
		Category:  "write",
	})

	found := false
	for _, a := range anomalies {
		if a.Type == AnomalyReadThenWriteJump {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected read-then-write jump anomaly")
	}
}

func TestBehaviorTracker_SessionRisk(t *testing.T) {
	tracker := NewBehaviorTracker()

	// Normal behavior
	risk := tracker.GetSessionRisk("new_session")
	if risk != BehaviorNormal {
		t.Errorf("unknown session risk=%v want Normal", risk)
	}

	// Add critical anomaly
	tracker.Track(BehaviorEvent{
		SessionID: "s1",
		Tool:      "delete_file",
		Target:    "important.dat",
	})
	// Create a deletion burst to get critical anomaly
	for i := 0; i < 5; i++ {
		tracker.Track(BehaviorEvent{
			SessionID: "s1",
			Tool:      "delete_file",
			Target:    "file" + string(rune('a'+i)) + ".txt",
		})
	}

	risk = tracker.GetSessionRisk("s1")
	if risk < BehaviorCritical {
		t.Errorf("session with deletion burst should be Critical, got %v", risk)
	}
}

func TestBehaviorTracker_SessionSummary(t *testing.T) {
	tracker := NewBehaviorTracker()

	tracker.Track(BehaviorEvent{
		SessionID: "s1",
		Tool:      "read_file",
		Target:    "file1.txt",
	})
	tracker.Track(BehaviorEvent{
		SessionID: "s1",
		Tool:      "write_file",
		Target:    "file2.txt",
	})
	tracker.Track(BehaviorEvent{
		SessionID: "s1",
		Tool:      "read_file",
		Target:    "file3.txt",
	})

	summary := tracker.GetSessionSummary("s1")
	if summary.TotalEvents != 3 {
		t.Errorf("TotalEvents=%d want 3", summary.TotalEvents)
	}
	if len(summary.RecentTools) == 0 {
		t.Error("should have recent tools")
	}
}

func TestBehaviorTracker_GetAnomalies(t *testing.T) {
	tracker := NewBehaviorTracker()

	// Create some anomalies
	for i := 0; i < 4; i++ {
		tracker.Track(BehaviorEvent{
			SessionID: "s1",
			Tool:      "delete_file",
			Target:    "file" + string(rune('a'+i)) + ".txt",
		})
	}

	// Get with limit
	anomalies := tracker.GetAnomalies("s1", 2)
	if len(anomalies) > 2 {
		t.Errorf("limited anomalies=%d want <= 2", len(anomalies))
	}

	// Unknown session
	empty := tracker.GetAnomalies("unknown", 0)
	if empty != nil {
		t.Error("unknown session should return nil")
	}
}
