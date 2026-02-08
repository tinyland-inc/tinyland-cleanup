package daemon

import (
	"sync"
	"time"
)

// EventType represents the type of cleanup event.
type EventType int

const (
	EventCycleStart EventType = iota
	EventCycleEnd
	EventPluginStart
	EventPluginEnd
	EventPluginError
	EventBytesFreed
	EventLevelChanged
	EventHeartbeat
	EventPluginSkipped
	EventPreflightFailed
)

// String returns the string representation of the event type.
func (e EventType) String() string {
	switch e {
	case EventCycleStart:
		return "cycle_start"
	case EventCycleEnd:
		return "cycle_end"
	case EventPluginStart:
		return "plugin_start"
	case EventPluginEnd:
		return "plugin_end"
	case EventPluginError:
		return "plugin_error"
	case EventBytesFreed:
		return "bytes_freed"
	case EventLevelChanged:
		return "level_changed"
	case EventHeartbeat:
		return "heartbeat"
	case EventPluginSkipped:
		return "plugin_skipped"
	case EventPreflightFailed:
		return "preflight_failed"
	default:
		return "unknown"
	}
}

// Event is a typed event published on the bus.
type Event struct {
	Type      EventType
	Timestamp time.Time
	Payload   interface{}
}

// CycleStartPayload is the payload for EventCycleStart.
type CycleStartPayload struct {
	CycleID     int64
	Level       string
	PluginCount int
}

// CycleEndPayload is the payload for EventCycleEnd.
type CycleEndPayload struct {
	CycleID      int64
	Duration     time.Duration
	TotalFreed   int64
	PluginsRun   int
	PluginErrors int
}

// PluginStartPayload is the payload for EventPluginStart.
type PluginStartPayload struct {
	CycleID       int64
	PluginName    string
	ResourceGroup string
}

// PluginEndPayload is the payload for EventPluginEnd.
type PluginEndPayload struct {
	CycleID      int64
	PluginName   string
	Duration     time.Duration
	BytesFreed   int64
	ItemsCleaned int
}

// PluginErrorPayload is the payload for EventPluginError.
type PluginErrorPayload struct {
	CycleID    int64
	PluginName string
	Error      error
}

// BytesFreedPayload is the payload for EventBytesFreed.
type BytesFreedPayload struct {
	PluginName string
	Mount      string
	Bytes      int64
}

// LevelChangedPayload is the payload for EventLevelChanged.
type LevelChangedPayload struct {
	PreviousLevel string
	NewLevel      string
	Mount         string
	UsedPercent   float64
}

// HeartbeatPayload is the payload for EventHeartbeat.
type HeartbeatPayload struct {
	UptimeSeconds float64
	CyclesRun     int64
	TotalFreed    int64
	LastCycleAt   time.Time
}

// PluginSkippedPayload is the payload for EventPluginSkipped.
type PluginSkippedPayload struct {
	PluginName string
	Reason     string
}

// PreflightFailedPayload is the payload for EventPreflightFailed.
type PreflightFailedPayload struct {
	PluginName string
	Reason     string
	FreeGB     float64
	NeededGB   float64
}

// Subscriber is a function that handles events.
type Subscriber func(Event)

// EventBus is a pub/sub event bus with buffered channels per subscriber.
type EventBus struct {
	mu          sync.RWMutex
	subscribers []subscriberEntry
	bufferSize  int
	closed      bool
	droppedOnce sync.Once // log dropped event warning only once
}

type subscriberEntry struct {
	name string
	ch   chan Event
	fn   Subscriber
	done chan struct{}
}

// NewEventBus creates a new event bus with the specified buffer size per subscriber.
func NewEventBus(bufferSize int) *EventBus {
	if bufferSize <= 0 {
		bufferSize = 256
	}
	return &EventBus{
		bufferSize: bufferSize,
	}
}

// Subscribe adds a named subscriber to the event bus.
// Each subscriber gets its own buffered channel and goroutine.
func (b *EventBus) Subscribe(name string, fn Subscriber) {
	b.mu.Lock()
	defer b.mu.Unlock()

	ch := make(chan Event, b.bufferSize)
	done := make(chan struct{})
	entry := subscriberEntry{name: name, ch: ch, fn: fn, done: done}

	// Start subscriber goroutine
	go func() {
		defer close(done)
		for event := range ch {
			fn(event)
		}
	}()

	b.subscribers = append(b.subscribers, entry)
}

// Publish sends an event to all subscribers. Non-blocking: if a subscriber's
// buffer is full, the event is dropped for that subscriber (logged once).
func (b *EventBus) Publish(event Event) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	if b.closed {
		return
	}

	for _, sub := range b.subscribers {
		select {
		case sub.ch <- event:
		default:
			// Buffer full, drop event (non-blocking)
		}
	}
}

// PublishTyped is a convenience method to publish a typed event.
func (b *EventBus) PublishTyped(eventType EventType, payload interface{}) {
	b.Publish(Event{
		Type:      eventType,
		Timestamp: time.Now(),
		Payload:   payload,
	})
}

// Close shuts down the event bus and waits for all subscribers to drain.
func (b *EventBus) Close() {
	b.mu.Lock()
	b.closed = true
	subs := make([]subscriberEntry, len(b.subscribers))
	copy(subs, b.subscribers)
	b.mu.Unlock()

	// Close all subscriber channels
	for _, sub := range subs {
		close(sub.ch)
	}

	// Wait for all subscribers to finish processing
	for _, sub := range subs {
		<-sub.done
	}
}
