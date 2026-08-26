package host

import (
	"context"
	"testing"
	"time"
)

func TestHeartbeatManagerCreation(t *testing.T) {
	bridge := NewBridge()
	cfg := DefaultHeartbeatConfig()
	hm := NewHeartbeatManager(bridge, cfg)
	if hm == nil {
		t.Fatal("expected HeartbeatManager, got nil")
	}
	if len(hm.statuses) != 0 {
		t.Error("expected empty statuses initially")
	}
}

func TestHeartbeatHealthTracking(t *testing.T) {
	bridge := NewBridge()
	cfg := DefaultHeartbeatConfig()
	cfg.Interval = 100 * time.Millisecond
	cfg.SessionTimeout = 500 * time.Millisecond
	cfg.MaxFails = 2
	hm := NewHeartbeatManager(bridge, cfg)

	bridge.Register(&HostSession{
		DeviceID:  "device-1",
		Workspace: "/tmp",
		Send:      func(env Envelope) error { return nil },
		LastSeen:  time.Now(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hm.Start(ctx)
	time.Sleep(200 * time.Millisecond)

	info, ok := hm.GetDeviceHealth("device-1")
	if !ok {
		t.Fatal("expected device-1 health to exist")
	}
	if info.Status != HealthOnline {
		t.Errorf("expected online, got %s", info.Status)
	}
}

func TestHeartbeatDeviceHealthy(t *testing.T) {
	bridge := NewBridge()
	hm := NewHeartbeatManager(bridge, DefaultHeartbeatConfig())

	bridge.Register(&HostSession{
		DeviceID:  "device-1",
		Workspace: "/tmp",
		Send:      func(env Envelope) error { return nil },
		LastSeen:  time.Now(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hm.Start(ctx)
	time.Sleep(300 * time.Millisecond)

	if !hm.IsHealthy("device-1") {
		t.Error("expected device-1 to be healthy")
	}
	if hm.IsHealthy("nonexistent") {
		t.Error("expected nonexistent device to be unhealthy")
	}
}

func TestHeartbeatGracefulDegradation(t *testing.T) {
	bridge := NewBridge()
	cfg := DefaultHeartbeatConfig()
	hm := NewHeartbeatManager(bridge, cfg)

	bridge.Register(&HostSession{
		DeviceID:  "device-1",
		Workspace: "/tmp",
		Send:      func(env Envelope) error { return nil },
		LastSeen:  time.Now(),
	})

	ctx := context.Background()

	text, err := hm.GracefulCall(ctx, "device-1", "call-1", "test_tool",
		map[string]any{"key": "val"}, 5*time.Second, DegradeToLocal,
		func(ctx context.Context, args map[string]any) (string, error) {
			return "local result", nil
		})
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if text != "local result" {
		t.Errorf("expected 'local result', got '%s'", text)
	}
}

func TestHeartbeatDegradationStrategies(t *testing.T) {
	bridge := NewBridge()
	hm := NewHeartbeatManager(bridge, DefaultHeartbeatConfig())
	ctx := context.Background()

	tests := []struct {
		name     string
		strategy GracefulDegradationStrategy
		wantErr  bool
	}{
		{"degrade to local", DegradeToLocal, false},
		{"fail fast", FailFast, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := hm.GracefulCall(ctx, "nonexistent", "call-1", "test",
				nil, 5*time.Second, tt.strategy,
				func(ctx context.Context, args map[string]any) (string, error) {
					return "local", nil
				})
			if (err != nil) != tt.wantErr {
				t.Errorf("strategy=%d: expected error=%v, got %v", tt.strategy, tt.wantErr, err)
			}
		})
	}
}

func TestHeartbeatStop(t *testing.T) {
	bridge := NewBridge()
	hm := NewHeartbeatManager(bridge, DefaultHeartbeatConfig())

	ctx, cancel := context.WithCancel(context.Background())
	hm.Start(ctx)

	hm.Stop()
	cancel()

	if hm.connected {
		t.Error("expected connected to be false after stop")
	}
}

func TestHeartbeatHealthStatuses(t *testing.T) {
	statuses := []HealthStatus{HealthOnline, HealthDegraded, HealthReconnecting, HealthOffline, HealthUnknown}
	for _, s := range statuses {
		if s == "" {
			t.Error("health status should not be empty")
		}
	}
}

func TestBridgeWithHeartbeat(t *testing.T) {
	cfg := DefaultHeartbeatConfig()
	bridge := NewBridgeWithHeartbeat(cfg)

	if bridge.HeartbeatManager() == nil {
		t.Error("expected heartbeat manager to be set")
	}

	if bridge.OnlineCountHealthy() != 0 {
		t.Error("expected 0 healthy sessions initially")
	}

	bridge.Register(&HostSession{
		DeviceID: "test",
		Send:     func(env Envelope) error { return nil },
		LastSeen: time.Now(),
	})

	if bridge.OnlineCountHealthy() != 1 {
		t.Error("expected 1 healthy session")
	}
}

func TestBridgeCallWithDegradation(t *testing.T) {
	bridge := NewBridgeWithHeartbeat(DefaultHeartbeatConfig())

	_, err := bridge.CallWithDegradation(
		context.Background(), "nonexistent", "call-1", "test",
		nil, 5*time.Second, DegradeToLocal,
		func(ctx context.Context, args map[string]any) (string, error) {
			return "fallback", nil
		},
	)
	if err != nil {
		t.Errorf("expected no error with degradation, got %v", err)
	}
}

func TestHeartbeatReconnectDelay(t *testing.T) {
	cfg := DefaultHeartbeatConfig()
	hm := NewHeartbeatManager(NewBridge(), cfg)

	tests := []struct {
		attempt int
		min     time.Duration
		max     time.Duration
	}{
		{0, 5 * time.Second, 5 * time.Second},
		{1, 10 * time.Second, 10 * time.Second},
		{3, 40 * time.Second, 60 * time.Second},
	}

	for _, tt := range tests {
		d := hm.reconnectDelay(tt.attempt)
		if d < tt.min || d > tt.max {
			t.Errorf("attempt %d: delay %v out of range [%v, %v]", tt.attempt, d, tt.min, tt.max)
		}
	}
}

func TestHeartbeatGetHealth(t *testing.T) {
	bridge := NewBridge()
	hm := NewHeartbeatManager(bridge, DefaultHeartbeatConfig())

	infos := hm.GetHealth()
	if len(infos) != 0 {
		t.Error("expected empty health info initially")
	}
}
