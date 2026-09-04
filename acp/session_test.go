package acp

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSessionPromptAgainstMockAgent(t *testing.T) {
	root := findRepoRoot(t)
	agentJS := filepath.Join(root, "acp", "testdata", "mock_agent.js")
	if _, err := os.Stat(agentJS); err != nil {
		t.Fatalf("mock agent missing: %v", err)
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not available")
	}

	events := make(chan Event, 64)
	mgr := NewManager(func(ev Event) {
		select {
		case events <- ev:
		default:
		}
	})
	defer mgr.CloseAll()

	cwd := t.TempDir()
	sess, err := mgr.Create(CreateOptions{
		Launch: AgentLaunch{
			Name:    "mock",
			Command: "node",
			Args:    []string{agentJS},
		},
		Mode:  "local",
		Cwd:   cwd,
		Title: "mock",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	waitReadyAndCommands(t, events, sess.ID, 10*time.Second)

	done := make(chan error, 1)
	go func() {
		done <- mgr.Prompt(sess.ID, "hello from test")
	}()

	// Expect assistant chunks while prompt is in-flight; answer permissions.
	deadline := time.After(10 * time.Second)
	gotAssistant := false
	for !gotAssistant {
		select {
		case ev := <-events:
			if ev.SessionID == sess.ID && ev.Type == "message" && ev.Role == "assistant" && ev.Content != "" {
				gotAssistant = true
			}
			if ev.SessionID == sess.ID && ev.Type == "permission" {
				_ = mgr.RespondPermission(sess.ID, ev.RequestID, "allow")
			}
			if ev.SessionID == sess.ID && ev.Type == "error" {
				t.Fatalf("error event: %s", ev.Content)
			}
		case err := <-done:
			if err != nil {
				t.Fatalf("prompt finished with error before assistant text: %v", err)
			}
			// Prompt returned; keep waiting briefly for any trailing events.
		case <-deadline:
			t.Fatal("timeout waiting for assistant message")
		}
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("prompt: %v", err)
		}
	case <-time.After(5 * time.Second):
		// prompt may still be waiting on permission path; ok if assistant already arrived
	}
}

func waitReadyAndCommands(t *testing.T, events <-chan Event, id string, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	gotReady := false
	gotCommands := false
	for !gotReady || !gotCommands {
		select {
		case ev := <-events:
			if ev.SessionID != id {
				continue
			}
			if ev.Type == "error" {
				t.Fatalf("error before ready/commands: %s", ev.Content)
			}
			if ev.Type == "status" && ev.Status == "ready" {
				gotReady = true
			}
			if ev.Type == "commands" {
				if len(ev.Commands) == 0 {
					t.Fatal("commands event with empty list")
				}
				gotCommands = true
			}
		case <-deadline:
			t.Fatalf("timeout waiting ready=%v commands=%v", gotReady, gotCommands)
		}
	}
}

func findRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no caller")
	}
	dir := filepath.Dir(file)
	// acp/ -> repo root
	return filepath.Clean(filepath.Join(dir, ".."))
}

func TestParseUsageUpdateEvent(t *testing.T) {
	ev := parseUsageUpdateEvent("s1", map[string]interface{}{
		"used": float64(1234),
		"size": float64(100000),
		"cost": map[string]interface{}{"amount": 0.5, "currency": "USD"},
	})
	if ev.Type != "usage" || ev.SessionID != "s1" {
		t.Fatalf("bad event: %+v", ev)
	}
	if ev.UsageUsed != 1234 || ev.UsageSize != 100000 {
		t.Fatalf("used/size: %d/%d", ev.UsageUsed, ev.UsageSize)
	}
	if ev.UsageCost != 0.5 || ev.UsageCurrency != "USD" {
		t.Fatalf("cost: %v %s", ev.UsageCost, ev.UsageCurrency)
	}
}

func TestThoughtChunkNotSystem(t *testing.T) {
	var got []Event
	s := &Session{ID: "s-thought", emit: func(ev Event) { got = append(got, ev) }}
	params, err := json.Marshal(map[string]interface{}{
		"sessionId": "agent-1",
		"update": map[string]interface{}{
			"sessionUpdate": "agent_thought_chunk",
			"content":       map[string]interface{}{"type": "text", "text": "hmm"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	s.handleSessionUpdate(params)
	if len(got) != 1 || got[0].Type != "thought" || got[0].Content != "hmm" || got[0].Role != "assistant" {
		t.Fatalf("got=%+v", got)
	}
}

func TestParseContentBlocksImage(t *testing.T) {
	text, media := parseContentBlocks([]interface{}{
		map[string]interface{}{"type": "text", "text": "see "},
		map[string]interface{}{
			"type":     "image",
			"mimeType": "image/png",
			"data":     "aGVsbG8=", // "hello"
			"alt":      "demo",
		},
		map[string]interface{}{
			"type": "resource_link",
			"uri":  "https://example.com/pic.jpg",
			"name": "pic.jpg",
		},
		map[string]interface{}{
			"type": "resource_link",
			"uri":  "file:///etc/passwd", // rejected
			"name": "bad",
		},
	})
	if text != "see " {
		t.Fatalf("text=%q", text)
	}
	if len(media) != 2 {
		t.Fatalf("media len=%d %+v", len(media), media)
	}
	if media[0].Kind != "image" || !strings.HasPrefix(media[0].URL, "data:image/png;base64,") {
		t.Fatalf("media0=%+v", media[0])
	}
	if media[0].Alt != "demo" {
		t.Fatalf("alt=%q", media[0].Alt)
	}
	if media[1].Kind != "image" || media[1].URL != "https://example.com/pic.jpg" {
		t.Fatalf("media1=%+v", media[1])
	}
}

func TestParseContentBlocksRejectsSVGBase64(t *testing.T) {
	_, media := parseContentBlocks(map[string]interface{}{
		"type":     "image",
		"mimeType": "image/svg+xml",
		"data":     "PHN2Zz4=",
	})
	if len(media) != 0 {
		t.Fatalf("expected no svg data media, got %+v", media)
	}
}

func TestImageOnlyMessageChunk(t *testing.T) {
	var got []Event
	s := &Session{ID: "s-img", emit: func(ev Event) { got = append(got, ev) }}
	params, _ := json.Marshal(map[string]interface{}{
		"sessionId": "agent-1",
		"update": map[string]interface{}{
			"sessionUpdate": "agent_message_chunk",
			"content": map[string]interface{}{
				"type":     "image",
				"mimeType": "image/jpeg",
				"uri":      "https://cdn.example.com/a.jpg",
			},
		},
	})
	s.handleSessionUpdate(params)
	if len(got) != 1 || got[0].Type != "message" || got[0].Role != "assistant" {
		t.Fatalf("got=%+v", got)
	}
	if got[0].Content != "" || len(got[0].Media) != 1 || got[0].Media[0].Kind != "image" {
		t.Fatalf("media event=%+v", got[0])
	}
}

func TestUsageUpdateChunk(t *testing.T) {
	var got []Event
	s := &Session{ID: "s-usage", emit: func(ev Event) { got = append(got, ev) }}
	params, _ := json.Marshal(map[string]interface{}{
		"sessionId": "agent-1",
		"update": map[string]interface{}{
			"sessionUpdate": "usage_update",
			"used":          float64(42),
			"size":          float64(1000),
		},
	})
	s.handleSessionUpdate(params)
	if len(got) != 1 || got[0].Type != "usage" || got[0].UsageUsed != 42 || got[0].UsageSize != 1000 {
		t.Fatalf("got=%+v", got)
	}
}
