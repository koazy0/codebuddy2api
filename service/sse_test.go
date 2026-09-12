package service

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSSEHeartbeatFrameIsEventNotComment(t *testing.T) {
	if strings.HasPrefix(strings.TrimSpace(sseHeartbeatFrame), ":") {
		t.Fatal("heartbeat must not be an SSE comment; Codex idle-timeout ignores comments")
	}
	if !strings.Contains(sseHeartbeatFrame, "event: ping") {
		t.Fatalf("missing event name: %q", sseHeartbeatFrame)
	}
	if !strings.Contains(sseHeartbeatFrame, `"type":"ping"`) {
		t.Fatalf("missing json type: %q", sseHeartbeatFrame)
	}
	if !strings.HasSuffix(sseHeartbeatFrame, "\n\n") {
		t.Fatalf("frame must end with blank line: %q", sseHeartbeatFrame)
	}
}

func TestSSESinkSerializesHeartbeatAndEvents(t *testing.T) {
	var buf safeBuffer
	sink := newSSESink(&buf, nil)

	stop := startSSEHeartbeatInterval(sink, 2*time.Millisecond)
	defer stop()

	var wg sync.WaitGroup
	const n = 80
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			frame := fmt.Sprintf("event: test\ndata: {\"i\":%d}\n\n", i)
			if _, err := sink.Write([]byte(frame)); err != nil {
				t.Errorf("write event: %v", err)
			}
		}(i)
	}
	wg.Wait()
	time.Sleep(15 * time.Millisecond)
	stop()

	raw := buf.String()
	if raw == "" {
		t.Fatal("no bytes written")
	}
	ping := strings.TrimRight(sseHeartbeatFrame, "\n")
	if strings.Count(raw, "event: ping") != strings.Count(raw, ping) {
		t.Fatalf("torn ping event:\n%s", raw)
	}

	for _, frame := range strings.Split(raw, "\n\n") {
		frame = strings.TrimRight(frame, "\n")
		if frame == "" {
			continue
		}
		if frame == ping || strings.HasPrefix(frame, "event: test\ndata: {\"i\":") {
			continue
		}
		t.Fatalf("interleaved/torn SSE frame %q\nfull:\n%s", frame, raw)
	}
}

func TestSSEHeartbeatStops(t *testing.T) {
	var buf safeBuffer
	sink := newSSESink(&buf, nil)
	stop := startSSEHeartbeatInterval(sink, time.Millisecond)
	time.Sleep(5 * time.Millisecond)
	stop()
	time.Sleep(8 * time.Millisecond)
	after := buf.Len()
	time.Sleep(8 * time.Millisecond)
	if buf.Len() != after {
		t.Fatalf("heartbeat kept writing after stop: %d -> %d", after, buf.Len())
	}
}

type safeBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func (s *safeBuffer) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Len()
}
