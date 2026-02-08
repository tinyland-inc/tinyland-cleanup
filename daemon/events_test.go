package daemon

import (
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestEventBusPublishSubscribe(t *testing.T) {
	bus := NewEventBus(16)
	defer bus.Close()

	var received int32
	bus.Subscribe("test", func(e Event) {
		atomic.AddInt32(&received, 1)
	})

	// Publish several events
	for i := 0; i < 10; i++ {
		bus.PublishTyped(EventPluginStart, PluginStartPayload{
			PluginName: "test-plugin",
		})
	}

	// Wait for processing
	time.Sleep(50 * time.Millisecond)
	if got := atomic.LoadInt32(&received); got != 10 {
		t.Errorf("expected 10 events, got %d", got)
	}
}

func TestEventBusMultipleSubscribers(t *testing.T) {
	bus := NewEventBus(16)
	defer bus.Close()

	var count1, count2 int32
	bus.Subscribe("sub1", func(e Event) { atomic.AddInt32(&count1, 1) })
	bus.Subscribe("sub2", func(e Event) { atomic.AddInt32(&count2, 1) })

	bus.PublishTyped(EventCycleStart, CycleStartPayload{CycleID: 1})
	time.Sleep(50 * time.Millisecond)

	if atomic.LoadInt32(&count1) != 1 || atomic.LoadInt32(&count2) != 1 {
		t.Error("both subscribers should receive the event")
	}
}

func TestEventBusNonBlocking(t *testing.T) {
	// Use buffer size 1 to test non-blocking behavior
	bus := NewEventBus(1)

	// Slow subscriber that blocks
	bus.Subscribe("slow", func(e Event) {
		time.Sleep(100 * time.Millisecond)
	})

	// Publishing should not block even if subscriber is slow
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			bus.PublishTyped(EventHeartbeat, HeartbeatPayload{})
		}
		close(done)
	}()

	select {
	case <-done:
		// Good - publish completed without blocking
	case <-time.After(time.Second):
		t.Error("publish blocked - should be non-blocking")
	}

	bus.Close()
}

func TestEventBusClose(t *testing.T) {
	bus := NewEventBus(16)

	var processed int32
	bus.Subscribe("test", func(e Event) {
		atomic.AddInt32(&processed, 1)
	})

	bus.PublishTyped(EventHeartbeat, HeartbeatPayload{})
	bus.Close()

	// Publishing after close should not panic
	bus.PublishTyped(EventHeartbeat, HeartbeatPayload{})
}

func TestEventTypeString(t *testing.T) {
	tests := []struct {
		et   EventType
		want string
	}{
		{EventCycleStart, "cycle_start"},
		{EventCycleEnd, "cycle_end"},
		{EventPluginStart, "plugin_start"},
		{EventPluginEnd, "plugin_end"},
		{EventPluginError, "plugin_error"},
		{EventBytesFreed, "bytes_freed"},
		{EventLevelChanged, "level_changed"},
		{EventHeartbeat, "heartbeat"},
		{EventPluginSkipped, "plugin_skipped"},
		{EventPreflightFailed, "preflight_failed"},
		{EventType(99), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.et.String(); got != tt.want {
			t.Errorf("EventType(%d).String() = %q, want %q", tt.et, got, tt.want)
		}
	}
}

func TestMetricsSubscriber(t *testing.T) {
	m := NewMetricsSubscriber()

	m.Handle(Event{
		Type: EventCycleEnd,
		Payload: CycleEndPayload{
			TotalFreed:   1024 * 1024,
			PluginErrors: 1,
		},
	})

	m.Handle(Event{
		Type: EventPluginEnd,
		Payload: PluginEndPayload{
			PluginName: "docker",
			Duration:   5 * time.Second,
			BytesFreed: 512 * 1024,
		},
	})

	if got := m.GetTotalFreed(); got != 1024*1024 {
		t.Errorf("TotalFreed = %d, want %d", got, 1024*1024)
	}
	if got := m.GetTotalCycles(); got != 1 {
		t.Errorf("TotalCycles = %d, want 1", got)
	}
	if got := m.GetTotalErrors(); got != 1 {
		t.Errorf("TotalErrors = %d, want 1", got)
	}

	stats := m.GetPluginStats()
	if ds, ok := stats["docker"]; !ok || ds.LastDuration != 5*time.Second {
		t.Error("docker plugin stats missing or incorrect")
	}
}

func TestHeartbeatSubscriber(t *testing.T) {
	tmpDir := t.TempDir()
	hbPath := tmpDir + "/heartbeat.json"

	hb := NewHeartbeatSubscriber(hbPath)
	hb.Handle(Event{
		Type:      EventCycleEnd,
		Timestamp: time.Now(),
		Payload: CycleEndPayload{
			CycleID:    1,
			TotalFreed: 1024,
		},
	})

	// Verify heartbeat file was written
	data, err := os.ReadFile(hbPath)
	if err != nil {
		t.Fatalf("heartbeat file not written: %v", err)
	}
	if len(data) == 0 {
		t.Error("heartbeat file is empty")
	}
}

func TestEventBusTimestampAutoSet(t *testing.T) {
	bus := NewEventBus(16)
	defer bus.Close()

	var receivedTime time.Time
	var wg sync.WaitGroup
	wg.Add(1)
	bus.Subscribe("time-check", func(e Event) {
		receivedTime = e.Timestamp
		wg.Done()
	})

	before := time.Now()
	bus.Publish(Event{Type: EventHeartbeat, Payload: HeartbeatPayload{}})
	wg.Wait()
	after := time.Now()

	if receivedTime.Before(before) || receivedTime.After(after) {
		t.Errorf("auto-set timestamp %v not between %v and %v", receivedTime, before, after)
	}
}

func TestNewEventBusDefaultBufferSize(t *testing.T) {
	bus := NewEventBus(0)
	if bus.bufferSize != 256 {
		t.Errorf("expected default buffer size 256, got %d", bus.bufferSize)
	}

	bus2 := NewEventBus(-5)
	if bus2.bufferSize != 256 {
		t.Errorf("expected default buffer size 256 for negative input, got %d", bus2.bufferSize)
	}
}
