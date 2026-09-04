package acp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Session is one ACP client connection to a custom agent command.
type Session struct {
	ID     string
	Title  string
	Mode   string // local | remote
	Agent  string
	Cwd    string
	Status string

	mu           sync.Mutex
	transport    Transport
	ctx          context.Context
	cancel       context.CancelFunc
	closed       atomic.Bool
	nextID       atomic.Int64
	pending      map[int64]chan rpcMessage
	perms        map[string]chan string
	agentSession string
	emit         func(Event)
	fileResolver FileResolver
}

// rpcWaitForever tells call to wait until the RPC returns or the session closes.
const rpcWaitForever time.Duration = -1

// FileResolver optionally serves fs/* client methods for the agent.
type FileResolver interface {
	ReadTextFile(path string, line, limit *int) (string, error)
	WriteTextFile(path, content string) error
}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func newSession(id, title, mode, agentName, cwd string, tr Transport, emit func(Event), files FileResolver) *Session {
	ctx, cancel := context.WithCancel(context.Background())
	s := &Session{
		ID:           id,
		Title:        title,
		Mode:         mode,
		Agent:        agentName,
		Cwd:          cwd,
		Status:       "starting",
		transport:    tr,
		ctx:          ctx,
		cancel:       cancel,
		pending:      make(map[int64]chan rpcMessage),
		perms:        make(map[string]chan string),
		emit:         emit,
		fileResolver: files,
	}
	go s.readLoop()
	go s.readStderr()
	go func() {
		_ = tr.Wait()
		if !s.closed.Load() {
			s.setStatus("exited")
			s.emit(Event{SessionID: s.ID, Type: "status", Status: "exited"})
			s.emit(Event{SessionID: s.ID, Type: "done"})
		}
		cancel()
	}()
	return s
}

func (s *Session) setStatus(st string) {
	s.mu.Lock()
	s.Status = st
	s.mu.Unlock()
}

func (s *Session) AgentSessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.agentSession
}

func (s *Session) readStderr() {
	if s.transport == nil || s.transport.Stderr() == nil {
		return
	}
	sc := bufio.NewScanner(s.transport.Stderr())
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		s.emit(Event{SessionID: s.ID, Type: "message", Role: "system", Content: line})
	}
}

func (s *Session) readLoop() {
	reader := bufio.NewReaderSize(s.transport.Stdout(), 1024*1024)
	for {
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			s.handleLine(strings.TrimRight(line, "\r\n"))
		}
		if err != nil {
			if err != io.EOF && !s.closed.Load() {
				s.emit(Event{SessionID: s.ID, Type: "error", Content: err.Error()})
			}
			return
		}
	}
}

func (s *Session) handleLine(line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	var msg rpcMessage
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		s.emit(Event{SessionID: s.ID, Type: "message", Role: "system", Content: line})
		return
	}

	// Request from agent -> client
	if msg.Method != "" && len(msg.ID) > 0 && string(msg.ID) != "null" {
		go s.handleIncomingRequest(msg)
		return
	}
	// Notification from agent -> client
	if msg.Method != "" {
		s.handleNotification(msg)
		return
	}
	// Response to our request
	if len(msg.ID) > 0 && string(msg.ID) != "null" {
		var id int64
		if err := json.Unmarshal(msg.ID, &id); err != nil {
			return
		}
		s.mu.Lock()
		ch := s.pending[id]
		if ch != nil {
			delete(s.pending, id)
		}
		s.mu.Unlock()
		if ch != nil {
			ch <- msg
		}
	}
}

func (s *Session) handleNotification(msg rpcMessage) {
	switch msg.Method {
	case "session/update":
		s.handleSessionUpdate(msg.Params)
	default:
		if len(msg.Params) > 0 {
			s.emit(Event{SessionID: s.ID, Type: "message", Role: "system", Content: msg.Method + " " + string(msg.Params)})
		}
	}
}

func (s *Session) handleSessionUpdate(params json.RawMessage) {
	var envelope struct {
		SessionID string          `json:"sessionId"`
		Update    json.RawMessage `json:"update"`
	}
	if err := json.Unmarshal(params, &envelope); err != nil {
		return
	}
	var update map[string]interface{}
	if err := json.Unmarshal(envelope.Update, &update); err != nil {
		return
	}
	kind, _ := update["sessionUpdate"].(string)
	switch kind {
	case "agent_message_chunk", "user_message_chunk":
		text, media := parseContentBlocks(update["content"])
		if text == "" && len(media) == 0 {
			return
		}
		role := "assistant"
		if kind == "user_message_chunk" {
			role = "user"
		}
		s.emit(Event{SessionID: s.ID, Type: "message", Role: role, Content: text, Media: media})
	case "agent_thought_chunk":
		text, media := parseContentBlocks(update["content"])
		if text == "" && len(media) == 0 {
			return
		}
		// Dedicated thought stream (not system notes). Text is primary; media rare.
		s.emit(Event{SessionID: s.ID, Type: "thought", Role: "assistant", Content: text, Media: media})
	case "tool_call", "tool_call_update":
		s.emitToolEvent(kind, update)
	case "plan":
		entries := parsePlanEntries(update["entries"])
		s.emit(Event{SessionID: s.ID, Type: "plan", PlanEntries: entries})
	case "available_commands_update":
		cmds := parseAvailableCommands(update["availableCommands"])
		s.emit(Event{SessionID: s.ID, Type: "commands", Commands: cmds})
	case "current_mode_update":
		modeID, _ := update["currentModeId"].(string)
		if modeID == "" {
			modeID, _ = update["currentModeID"].(string)
		}
		s.emit(Event{SessionID: s.ID, Type: "mode", CurrentModeID: modeID})
	case "usage_update":
		s.emit(parseUsageUpdateEvent(s.ID, update))
	default:
		if text, media := parseContentBlocks(update["content"]); text != "" || len(media) > 0 {
			s.emit(Event{SessionID: s.ID, Type: "message", Role: "assistant", Content: text, Media: media})
		}
	}
}

// maxMediaBase64Chars caps embedded media payload size (~6MB binary at 8M chars).
const maxMediaBase64Chars = 8 * 1024 * 1024

func contentToText(v interface{}) string {
	text, _ := parseContentBlocks(v)
	return text
}

// parseContentBlocks extracts plain text and displayable media from ACP ContentBlock
// values (single block, array, or loose shapes used by agents).
func parseContentBlocks(v interface{}) (string, []MediaItem) {
	var text strings.Builder
	var media []MediaItem
	var walk func(interface{})
	walk = func(node interface{}) {
		switch x := node.(type) {
		case nil:
			return
		case string:
			if strings.TrimSpace(x) != "" {
				text.WriteString(x)
			}
		case []interface{}:
			for _, item := range x {
				walk(item)
			}
		case map[string]interface{}:
			typ, _ := x["type"].(string)
			typ = strings.ToLower(strings.TrimSpace(typ))
			switch typ {
			case "text", "":
				if t, ok := x["text"].(string); ok && t != "" {
					text.WriteString(t)
					return
				}
				if t, ok := x["content"].(string); ok && t != "" {
					text.WriteString(t)
					return
				}
				if inner, ok := x["content"]; ok && typ == "" {
					walk(inner)
				}
			case "image", "audio", "video":
				if m, ok := mediaFromBlock(typ, x); ok {
					media = append(media, m)
				}
			case "resource_link":
				if m, ok := mediaFromResourceLink(x); ok {
					media = append(media, m)
				}
			case "resource":
				if m, ok := mediaFromEmbeddedResource(x); ok {
					media = append(media, m)
				}
			default:
				// Unknown typed block: try nested text only.
				if t, ok := x["text"].(string); ok && t != "" {
					text.WriteString(t)
				}
			}
		}
	}
	walk(v)
	return text.String(), media
}

func mediaFromBlock(kind string, x map[string]interface{}) (MediaItem, bool) {
	mime, _ := x["mimeType"].(string)
	if mime == "" {
		mime, _ = x["mime_type"].(string)
	}
	mime = strings.TrimSpace(mime)
	name, _ := x["name"].(string)
	alt, _ := x["alt"].(string)
	if alt == "" {
		alt, _ = x["description"].(string)
	}
	if alt == "" {
		alt = name
	}

	url := ""
	if data, _ := x["data"].(string); strings.TrimSpace(data) != "" {
		url = dataURLFromBase64(mime, data)
	}
	if url == "" {
		if u, _ := x["uri"].(string); isSafeMediaURL(u) {
			url = strings.TrimSpace(u)
		}
	}
	if url == "" {
		if u, _ := x["url"].(string); isSafeMediaURL(u) {
			url = strings.TrimSpace(u)
		}
	}
	if url == "" {
		return MediaItem{}, false
	}
	k := mediaKindFrom(kind, mime, url)
	if mime == "" {
		mime = mimeFromKind(k)
	}
	return MediaItem{Kind: k, URL: url, MimeType: mime, Alt: strings.TrimSpace(alt), Name: strings.TrimSpace(name)}, true
}

func mediaFromResourceLink(x map[string]interface{}) (MediaItem, bool) {
	uri, _ := x["uri"].(string)
	uri = strings.TrimSpace(uri)
	if !isSafeMediaURL(uri) {
		return MediaItem{}, false
	}
	mime, _ := x["mimeType"].(string)
	if mime == "" {
		mime, _ = x["mime_type"].(string)
	}
	name, _ := x["name"].(string)
	alt, _ := x["description"].(string)
	if alt == "" {
		alt = name
	}
	k := mediaKindFrom("file", mime, uri)
	return MediaItem{Kind: k, URL: uri, MimeType: strings.TrimSpace(mime), Alt: strings.TrimSpace(alt), Name: strings.TrimSpace(name)}, true
}

func mediaFromEmbeddedResource(x map[string]interface{}) (MediaItem, bool) {
	res, _ := x["resource"].(map[string]interface{})
	if res == nil {
		// Some agents nest fields directly on the block.
		res = x
	}
	mime, _ := res["mimeType"].(string)
	if mime == "" {
		mime, _ = res["mime_type"].(string)
	}
	name, _ := res["name"].(string)
	if name == "" {
		name, _ = x["name"].(string)
	}
	uri, _ := res["uri"].(string)

	if blob, _ := res["blob"].(string); strings.TrimSpace(blob) != "" {
		url := dataURLFromBase64(mime, blob)
		if url == "" {
			return MediaItem{}, false
		}
		k := mediaKindFrom("file", mime, url)
		return MediaItem{Kind: k, URL: url, MimeType: strings.TrimSpace(mime), Name: strings.TrimSpace(name), Alt: strings.TrimSpace(name)}, true
	}
	// Text resources stay as message text when useful; binary-ish links become media.
	if t, _ := res["text"].(string); strings.TrimSpace(t) != "" && !looksLikeBinaryMime(mime) {
		return MediaItem{}, false
	}
	uri = strings.TrimSpace(uri)
	if !isSafeMediaURL(uri) {
		return MediaItem{}, false
	}
	k := mediaKindFrom("file", mime, uri)
	return MediaItem{Kind: k, URL: uri, MimeType: strings.TrimSpace(mime), Name: strings.TrimSpace(name), Alt: strings.TrimSpace(name)}, true
}

func dataURLFromBase64(mime, data string) string {
	data = strings.TrimSpace(data)
	if data == "" {
		return ""
	}
	// Already a data URL.
	if strings.HasPrefix(strings.ToLower(data), "data:") {
		if isSafeMediaURL(data) {
			return data
		}
		return ""
	}
	if len(data) > maxMediaBase64Chars {
		return ""
	}
	mime = strings.TrimSpace(mime)
	if mime == "" {
		mime = "application/octet-stream"
	}
	// Only embed image/audio/video as data URLs (no arbitrary binary/script mimes).
	ml := strings.ToLower(mime)
	if !strings.HasPrefix(ml, "image/") && !strings.HasPrefix(ml, "audio/") && !strings.HasPrefix(ml, "video/") {
		return ""
	}
	if strings.Contains(ml, "svg") {
		// Inline SVG can carry script; skip rather than sanitize full XML.
		return ""
	}
	// Strip whitespace/newlines common in multiline base64.
	if strings.ContainsAny(data, " \n\r\t") {
		data = strings.Map(func(r rune) rune {
			if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
				return -1
			}
			return r
		}, data)
	}
	out := "data:" + mime + ";base64," + data
	if !isSafeMediaURL(out) {
		return ""
	}
	return out
}

func isSafeMediaURL(u string) bool {
	u = strings.TrimSpace(u)
	if u == "" {
		return false
	}
	lower := strings.ToLower(u)
	switch {
	case strings.HasPrefix(lower, "https://"):
		return true
	case strings.HasPrefix(lower, "http://"):
		return true
	case strings.HasPrefix(lower, "data:image/"),
		strings.HasPrefix(lower, "data:audio/"),
		strings.HasPrefix(lower, "data:video/"):
		// Reject data URLs that embed scripts via exotic mime or huge payloads.
		if len(u) > maxMediaBase64Chars+128 {
			return false
		}
		if strings.Contains(lower, "svg") && strings.Contains(lower, "script") {
			return false
		}
		return true
	default:
		return false
	}
}

func mediaKindFrom(hint, mime, url string) string {
	m := strings.ToLower(strings.TrimSpace(mime))
	h := strings.ToLower(strings.TrimSpace(hint))
	u := strings.ToLower(url)
	switch {
	case strings.HasPrefix(m, "image/"), h == "image", strings.HasPrefix(u, "data:image/"):
		return "image"
	case strings.HasPrefix(m, "video/"), h == "video", strings.HasPrefix(u, "data:video/"):
		return "video"
	case strings.HasPrefix(m, "audio/"), h == "audio", strings.HasPrefix(u, "data:audio/"):
		return "audio"
	default:
		// Guess from URL extension for http(s) links.
		if i := strings.Index(u, "?"); i >= 0 {
			u = u[:i]
		}
		switch {
		case strings.HasSuffix(u, ".png"), strings.HasSuffix(u, ".jpg"), strings.HasSuffix(u, ".jpeg"),
			strings.HasSuffix(u, ".gif"), strings.HasSuffix(u, ".webp"), strings.HasSuffix(u, ".bmp"),
			strings.HasSuffix(u, ".svg"):
			return "image"
		case strings.HasSuffix(u, ".mp4"), strings.HasSuffix(u, ".webm"), strings.HasSuffix(u, ".mov"),
			strings.HasSuffix(u, ".mkv"):
			return "video"
		case strings.HasSuffix(u, ".mp3"), strings.HasSuffix(u, ".wav"), strings.HasSuffix(u, ".ogg"),
			strings.HasSuffix(u, ".m4a"), strings.HasSuffix(u, ".flac"):
			return "audio"
		}
		if h == "image" || h == "video" || h == "audio" {
			return h
		}
		return "file"
	}
}

func mimeFromKind(kind string) string {
	switch kind {
	case "image":
		return "image/*"
	case "video":
		return "video/*"
	case "audio":
		return "audio/*"
	default:
		return "application/octet-stream"
	}
}

func looksLikeBinaryMime(mime string) bool {
	m := strings.ToLower(strings.TrimSpace(mime))
	if m == "" {
		return false
	}
	return strings.HasPrefix(m, "image/") ||
		strings.HasPrefix(m, "audio/") ||
		strings.HasPrefix(m, "video/") ||
		m == "application/octet-stream" ||
		m == "application/pdf"
}

func (s *Session) handleIncomingRequest(msg rpcMessage) {
	var result interface{}
	var rpcErr *rpcError
	switch msg.Method {
	case "session/request_permission":
		result, rpcErr = s.handlePermissionRequest(msg.Params)
	case "fs/read_text_file":
		result, rpcErr = s.handleReadTextFile(msg.Params)
	case "fs/write_text_file":
		result, rpcErr = s.handleWriteTextFile(msg.Params)
	default:
		// Optional client methods we don't implement yet.
		rpcErr = &rpcError{Code: -32601, Message: "Method not found: " + msg.Method}
	}
	resp := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      rawID(msg.ID),
	}
	if rpcErr != nil {
		resp["error"] = rpcErr
	} else {
		if result == nil {
			result = map[string]interface{}{}
		}
		resp["result"] = result
	}
	_ = s.writeJSON(resp)
}

func rawID(raw json.RawMessage) interface{} {
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	return v
}

func (s *Session) handlePermissionRequest(params json.RawMessage) (interface{}, *rpcError) {
	var req struct {
		SessionID string `json:"sessionId"`
		ToolCall  struct {
			ToolCallID string          `json:"toolCallId"`
			Title      string          `json:"title"`
			Kind       string          `json:"kind"`
			Status     string          `json:"status"`
			Content    json.RawMessage `json:"content"`
			RawInput   json.RawMessage `json:"rawInput"`
		} `json:"toolCall"`
		Options []struct {
			Kind     string `json:"kind"`
			Name     string `json:"name"`
			OptionID string `json:"optionId"`
		} `json:"options"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, &rpcError{Code: -32602, Message: "invalid params"}
	}

	options := make([]PermissionOption, 0, len(req.Options)+8)
	for _, o := range req.Options {
		id := strings.TrimSpace(o.OptionID)
		if id == "" {
			continue
		}
		name := strings.TrimSpace(o.Name)
		if name == "" {
			name = id
		}
		options = append(options, PermissionOption{OptionID: id, Name: name, Kind: o.Kind})
	}
	if len(options) == 0 && len(req.ToolCall.Content) > 0 {
		options = append(options, extractOptionsFromContent(req.ToolCall.Content)...)
	}

	prompt := permissionPromptText(req.ToolCall.Content)
	tool := req.ToolCall.Title
	if tool == "" {
		tool = req.ToolCall.Kind
	}
	if tool == "" {
		tool = req.ToolCall.ToolCallID
	}
	if tool == "" {
		tool = "permission"
	}

	// Timeline tool card while waiting for the user choice.
	if req.ToolCall.ToolCallID != "" {
		status := req.ToolCall.Status
		if status == "" {
			status = "pending"
		}
		input := ""
		if len(req.ToolCall.RawInput) > 0 {
			input = trimUI(strings.TrimSpace(string(req.ToolCall.RawInput)), 1500)
		}
		s.emit(Event{
			SessionID:  s.ID,
			Type:       "tool",
			ToolCallID: req.ToolCall.ToolCallID,
			ToolName:   tool,
			ToolKind:   req.ToolCall.Kind,
			Status:     status,
			Content:    prompt,
			ToolInput:  input,
		})
	}

	requestID := req.ToolCall.ToolCallID
	if requestID == "" {
		requestID = fmt.Sprintf("perm-%d", s.nextID.Add(1))
	}
	wait := s.registerPermission(requestID)

	input := ""
	if len(req.ToolCall.RawInput) > 0 {
		input = trimUI(strings.TrimSpace(string(req.ToolCall.RawInput)), 1500)
	}
	s.emit(Event{
		SessionID:  s.ID,
		Type:       "permission",
		RequestID:  requestID,
		ToolName:   tool,
		ToolCallID: req.ToolCall.ToolCallID,
		ToolKind:   req.ToolCall.Kind,
		Content:    prompt,
		ToolInput:  input,
		Options:    options,
	})

	optionID := ""
	select {
	case optionID = <-wait:
	case <-time.After(5 * time.Minute):
		optionID = ""
	}

	if optionID == "" {
		optionID = fallbackRejectOption(options)
	} else if optionID == "allow" || optionID == "reject" {
		if mapped := mapLegacyOption(optionID, options); mapped != "" {
			optionID = mapped
		}
	}
	if optionID == "" {
		optionID = "reject"
	}

	return map[string]interface{}{
		"outcome": map[string]interface{}{
			"outcome":  "selected",
			"optionId": optionID,
		},
	}, nil
}

func permissionPromptText(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	var v interface{}
	if err := json.Unmarshal(content, &v); err != nil {
		return ""
	}
	switch x := v.(type) {
	case []interface{}:
		var parts []string
		for _, item := range x {
			m, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			t, _ := m["type"].(string)
			if t == "text" {
				if tx, _ := m["text"].(string); strings.TrimSpace(tx) != "" {
					parts = append(parts, tx)
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		return contentToText(v)
	}
}

func extractOptionsFromContent(content json.RawMessage) []PermissionOption {
	var v interface{}
	if err := json.Unmarshal(content, &v); err != nil {
		return nil
	}
	var out []PermissionOption
	var walk func(interface{})
	walk = func(node interface{}) {
		switch x := node.(type) {
		case []interface{}:
			for _, item := range x {
				walk(item)
			}
		case map[string]interface{}:
			if id, ok := x["optionId"].(string); ok && strings.TrimSpace(id) != "" {
				name, _ := x["name"].(string)
				kind, _ := x["kind"].(string)
				if strings.TrimSpace(name) == "" {
					name = id
				}
				out = append(out, PermissionOption{OptionID: id, Name: name, Kind: kind})
				return
			}
			if inner, ok := x["content"]; ok {
				walk(inner)
			}
			if opts, ok := x["options"]; ok {
				walk(opts)
			}
		}
	}
	walk(v)
	seen := map[string]bool{}
	uniq := make([]PermissionOption, 0, len(out))
	for _, o := range out {
		if seen[o.OptionID] {
			continue
		}
		seen[o.OptionID] = true
		uniq = append(uniq, o)
	}
	return uniq
}

func mapLegacyOption(legacy string, options []PermissionOption) string {
	legacy = strings.ToLower(strings.TrimSpace(legacy))
	for _, o := range options {
		k := strings.ToLower(o.Kind)
		id := strings.ToLower(o.OptionID)
		name := strings.ToLower(o.Name)
		if legacy == "allow" {
			if strings.Contains(k, "allow") || id == "allow" || strings.Contains(name, "allow") {
				return o.OptionID
			}
		}
		if legacy == "reject" {
			if strings.Contains(k, "reject") || strings.Contains(k, "deny") || id == "reject" || id == "deny" || strings.Contains(name, "reject") {
				return o.OptionID
			}
		}
	}
	if legacy == "allow" && len(options) > 0 {
		return options[0].OptionID
	}
	if legacy == "reject" {
		return fallbackRejectOption(options)
	}
	return ""
}

func fallbackRejectOption(options []PermissionOption) string {
	for _, o := range options {
		k := strings.ToLower(o.Kind)
		id := strings.ToLower(o.OptionID)
		if strings.Contains(k, "reject") || strings.Contains(k, "deny") || id == "reject" || id == "deny" {
			return o.OptionID
		}
	}
	return ""
}

func (s *Session) registerPermission(requestID string) chan string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.perms == nil {
		s.perms = make(map[string]chan string)
	}
	ch := make(chan string, 1)
	s.perms[requestID] = ch
	return ch
}

func (s *Session) handleReadTextFile(params json.RawMessage) (interface{}, *rpcError) {
	var req struct {
		Path  string `json:"path"`
		Line  *int   `json:"line"`
		Limit *int   `json:"limit"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, &rpcError{Code: -32602, Message: "invalid params"}
	}
	path := s.resolvePath(req.Path)
	if s.fileResolver != nil {
		text, err := s.fileResolver.ReadTextFile(path, req.Line, req.Limit)
		if err != nil {
			return nil, &rpcError{Code: -32000, Message: err.Error()}
		}
		return map[string]interface{}{"content": text}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, &rpcError{Code: -32000, Message: err.Error()}
	}
	text := string(data)
	if req.Line != nil || req.Limit != nil {
		text = sliceLines(text, req.Line, req.Limit)
	}
	return map[string]interface{}{"content": text}, nil
}

func (s *Session) handleWriteTextFile(params json.RawMessage) (interface{}, *rpcError) {
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(params, &req); err != nil {
		return nil, &rpcError{Code: -32602, Message: "invalid params"}
	}
	path := s.resolvePath(req.Path)
	if s.fileResolver != nil {
		if err := s.fileResolver.WriteTextFile(path, req.Content); err != nil {
			return nil, &rpcError{Code: -32000, Message: err.Error()}
		}
		return map[string]interface{}{}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, &rpcError{Code: -32000, Message: err.Error()}
	}
	if err := os.WriteFile(path, []byte(req.Content), 0644); err != nil {
		return nil, &rpcError{Code: -32000, Message: err.Error()}
	}
	return map[string]interface{}{}, nil
}

func (s *Session) resolvePath(p string) string {
	if p == "" {
		return p
	}
	if filepath.IsAbs(p) {
		return p
	}
	if s.Cwd == "" {
		return p
	}
	return filepath.Join(s.Cwd, p)
}

func sliceLines(text string, line, limit *int) string {
	lines := strings.Split(text, "\n")
	start := 0
	if line != nil && *line > 1 {
		start = *line - 1
		if start > len(lines) {
			start = len(lines)
		}
	}
	end := len(lines)
	if limit != nil && *limit >= 0 {
		end = start + *limit
		if end > len(lines) {
			end = len(lines)
		}
	}
	return strings.Join(lines[start:end], "\n")
}

func (s *Session) writeJSON(v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed.Load() || s.transport == nil {
		return fmt.Errorf("session closed")
	}
	_, err = s.transport.Stdin().Write(data)
	return err
}

func (s *Session) call(method string, params interface{}, timeout time.Duration) (json.RawMessage, error) {
	id := s.nextID.Add(1)
	ch := make(chan rpcMessage, 1)
	s.mu.Lock()
	s.pending[id] = ch
	s.mu.Unlock()

	req := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
	}
	if params != nil {
		req["params"] = params
	}
	if err := s.writeJSON(req); err != nil {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, err
	}

	failPending := func(err error) (json.RawMessage, error) {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, err
	}

	// timeout < 0: wait until response or session close (long prompts).
	// timeout == 0: default 30s. timeout > 0: explicit deadline.
	if timeout < 0 {
		select {
		case resp := <-ch:
			if resp.Error != nil {
				return nil, fmt.Errorf("%s", resp.Error.Message)
			}
			return resp.Result, nil
		case <-s.ctx.Done():
			return failPending(fmt.Errorf("rpc canceled: %s", method))
		}
	}
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case resp := <-ch:
		if resp.Error != nil {
			return nil, fmt.Errorf("%s", resp.Error.Message)
		}
		return resp.Result, nil
	case <-timer.C:
		return failPending(fmt.Errorf("rpc timeout: %s", method))
	case <-s.ctx.Done():
		return failPending(fmt.Errorf("rpc canceled: %s", method))
	}
}

// Initialize performs the official ACP handshake:
// initialize -> session/new
func (s *Session) Initialize(cwd string) error {
	s.setStatus("initializing")
	s.emit(Event{SessionID: s.ID, Type: "status", Status: "initializing"})

	initParams := map[string]interface{}{
		"protocolVersion": 1,
		"clientCapabilities": map[string]interface{}{
			"fs": map[string]interface{}{
				"readTextFile":  true,
				"writeTextFile": true,
			},
			"terminal": false,
		},
		"clientInfo": map[string]interface{}{
			"name":    "aimuxterm",
			"title":   "aimuxterm",
			"version": "0.1.0",
		},
	}
	if _, err := s.call("initialize", initParams, 20*time.Second); err != nil {
		// Still try session/new; some agents are loose. Surface error but continue if possible.
		s.emit(Event{SessionID: s.ID, Type: "message", Role: "system", Content: "initialize: " + err.Error()})
	}

	newParams := map[string]interface{}{
		"cwd":        cwd,
		"mcpServers": []interface{}{},
	}
	raw, err := s.call("session/new", newParams, 20*time.Second)
	if err != nil {
		s.setStatus("error")
		s.emit(Event{SessionID: s.ID, Type: "status", Status: "error"})
		s.emit(Event{SessionID: s.ID, Type: "error", Content: "session/new: " + err.Error()})
		return fmt.Errorf("session/new: %w", err)
	}
	var resp struct {
		SessionID string          `json:"sessionId"`
		Modes     json.RawMessage `json:"modes"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil || resp.SessionID == "" {
		s.setStatus("error")
		s.emit(Event{SessionID: s.ID, Type: "error", Content: "session/new missing sessionId"})
		return fmt.Errorf("session/new missing sessionId")
	}
	s.mu.Lock()
	s.agentSession = resp.SessionID
	s.mu.Unlock()

	s.setStatus("ready")
	s.emit(Event{SessionID: s.ID, Type: "status", Status: "ready"})
	s.emit(Event{SessionID: s.ID, Type: "message", Role: "system", Content: "ACP session ready (" + resp.SessionID + ")"})

	if len(resp.Modes) > 0 {
		var modesObj interface{}
		if err := json.Unmarshal(resp.Modes, &modesObj); err == nil {
			modes, current := parseSessionModes(modesObj)
			if len(modes) > 0 || current != "" {
				s.emit(Event{SessionID: s.ID, Type: "mode", Modes: modes, CurrentModeID: current})
			}
		}
	}
	return nil
}

func (s *Session) emitToolEvent(kind string, update map[string]interface{}) {
	id, _ := update["toolCallId"].(string)
	if id == "" {
		id, _ = update["toolCallID"].(string)
	}
	title := firstString(update, "title", "name", "toolName", "tool")
	status, _ := update["status"].(string)
	toolKind, _ := update["kind"].(string)
	content := uiValueText(update["content"])
	if content == "" {
		content = uiValueText(update["rawOutput"])
	}
	input := toolInputSummary(update)
	if title == "" {
		title = toolKind
	}
	if title == "" {
		title = "tool"
	}
	s.emit(Event{
		SessionID:  s.ID,
		Type:       "tool",
		ToolCallID: id,
		ToolName:   title,
		ToolKind:   toolKind,
		Status:     status,
		Content:    content,
		ToolInput:  input,
	})
	_ = kind
}

func firstString(values map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func uiValueText(v interface{}) string {
	if s := contentToText(v); s != "" {
		return s
	}
	if v == nil {
		return ""
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return ""
	}
	return string(data)
}

func toolInputSummary(update map[string]interface{}) string {
	for _, key := range []string{"rawInput", "input", "arguments", "params"} {
		if s := uiValueText(update[key]); s != "" {
			return trimUI(s, 2000)
		}
	}
	if locs, ok := update["locations"].([]interface{}); ok && len(locs) > 0 {
		var parts []string
		for _, loc := range locs {
			m, ok := loc.(map[string]interface{})
			if !ok {
				continue
			}
			path, _ := m["path"].(string)
			if path == "" {
				continue
			}
			if line, ok := m["line"].(float64); ok && line > 0 {
				parts = append(parts, fmt.Sprintf("%s:%d", path, int(line)))
			} else {
				parts = append(parts, path)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, ", ")
		}
	}
	return ""
}

func parseUsageUpdateEvent(sessionID string, update map[string]interface{}) Event {
	ev := Event{SessionID: sessionID, Type: "usage"}
	ev.UsageUsed = jsonInt64(update["used"])
	ev.UsageSize = jsonInt64(update["size"])
	if cost, ok := update["cost"].(map[string]interface{}); ok && cost != nil {
		ev.UsageCost = jsonFloat(cost["amount"])
		if c, ok := cost["currency"].(string); ok {
			ev.UsageCurrency = c
		}
	}
	return ev
}

func jsonInt64(v interface{}) int64 {
	switch x := v.(type) {
	case nil:
		return 0
	case int:
		return int64(x)
	case int64:
		return x
	case float64:
		return int64(x)
	case float32:
		return int64(x)
	case json.Number:
		i, _ := x.Int64()
		return i
	case string:
		var n int64
		fmt.Sscan(x, &n)
		return n
	default:
		return 0
	}
}

func jsonFloat(v interface{}) float64 {
	switch x := v.(type) {
	case nil:
		return 0
	case float64:
		return x
	case float32:
		return float64(x)
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case json.Number:
		f, _ := x.Float64()
		return f
	case string:
		var n float64
		fmt.Sscan(x, &n)
		return n
	default:
		return 0
	}
}

func parsePlanEntries(v interface{}) []PlanEntry {
	arr, ok := v.([]interface{})
	if !ok || len(arr) == 0 {
		return nil
	}
	out := make([]PlanEntry, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		content, _ := m["content"].(string)
		content = strings.TrimSpace(content)
		if content == "" {
			continue
		}
		status, _ := m["status"].(string)
		priority, _ := m["priority"].(string)
		out = append(out, PlanEntry{
			Content:  content,
			Status:   strings.TrimSpace(status),
			Priority: strings.TrimSpace(priority),
		})
	}
	return out
}

func parseSessionModes(v interface{}) (modes []SessionMode, current string) {
	if v == nil {
		return nil, ""
	}
	m, ok := v.(map[string]interface{})
	if !ok {
		return nil, ""
	}
	current, _ = m["currentModeId"].(string)
	if current == "" {
		current, _ = m["currentModeID"].(string)
	}
	raw := m["availableModes"]
	if raw == nil {
		raw = m["availableMode"]
	}
	arr, _ := raw.([]interface{})
	for _, item := range arr {
		em, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		id, _ := em["id"].(string)
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		name, _ := em["name"].(string)
		desc, _ := em["description"].(string)
		modes = append(modes, SessionMode{
			ID:          id,
			Name:        strings.TrimSpace(name),
			Description: strings.TrimSpace(desc),
		})
	}
	return modes, current
}

func trimUI(s string, n int) string {
	s = strings.TrimSpace(s)
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func parseAvailableCommands(v interface{}) []AvailableCommand {
	arr, ok := v.([]interface{})
	if !ok || len(arr) == 0 {
		return nil
	}
	out := make([]AvailableCommand, 0, len(arr))
	for _, item := range arr {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		desc, _ := m["description"].(string)
		hint := ""
		if input, ok := m["input"].(map[string]interface{}); ok {
			hint, _ = input["hint"].(string)
		}
		if hint == "" {
			hint, _ = m["inputHint"].(string)
		}
		out = append(out, AvailableCommand{
			Name:        name,
			Description: desc,
			InputHint:   hint,
		})
	}
	return out
}

// Prompt sends session/prompt with ContentBlock[] and streams session/update notifications.
func (s *Session) Prompt(text string) error {
	sessionID := s.AgentSessionID()
	if sessionID == "" {
		return fmt.Errorf("ACP session not initialized")
	}
	s.setStatus("running")
	s.emit(Event{SessionID: s.ID, Type: "status", Status: "running"})
	s.emit(Event{SessionID: s.ID, Type: "message", Role: "user", Content: text})

	params := map[string]interface{}{
		"sessionId": sessionID,
		"prompt": []map[string]interface{}{
			{
				"type": "text",
				"text": text,
			},
		},
	}
	// Prompt may run for hours; stream updates as notifications. No wall-clock
	// deadline — wait until the agent responds or the session is closed/canceled.
	raw, err := s.call("session/prompt", params, rpcWaitForever)
	if err != nil {
		s.setStatus("error")
		s.emit(Event{SessionID: s.ID, Type: "status", Status: "error"})
		s.emit(Event{SessionID: s.ID, Type: "error", Content: err.Error()})
		return err
	}
	var resp struct {
		StopReason string `json:"stopReason"`
		Usage      *struct {
			TotalTokens       int64  `json:"totalTokens"`
			InputTokens       int64  `json:"inputTokens"`
			OutputTokens      int64  `json:"outputTokens"`
			ThoughtTokens     *int64 `json:"thoughtTokens"`
			CachedReadTokens  *int64 `json:"cachedReadTokens"`
			CachedWriteTokens *int64 `json:"cachedWriteTokens"`
		} `json:"usage"`
	}
	_ = json.Unmarshal(raw, &resp)
	if resp.StopReason != "" && resp.StopReason != "end_turn" {
		s.emit(Event{SessionID: s.ID, Type: "message", Role: "system", Content: "stop: " + resp.StopReason})
	}
	if resp.Usage != nil && resp.Usage.TotalTokens > 0 {
		// PromptResponse.usage has totals only; keep size if UI already has it from usage_update.
		s.emit(Event{SessionID: s.ID, Type: "usage", UsageUsed: resp.Usage.TotalTokens})
	}
	s.setStatus("ready")
	s.emit(Event{SessionID: s.ID, Type: "status", Status: "ready"})
	return nil
}

// RespondPermission answers a pending permission request shown in UI.
func (s *Session) RespondPermission(requestID, optionID string) error {
	s.mu.Lock()
	ch := s.perms[requestID]
	if ch != nil {
		delete(s.perms, requestID)
	}
	s.mu.Unlock()
	if ch == nil {
		return fmt.Errorf("permission request not found")
	}
	select {
	case ch <- optionID:
	default:
	}
	return nil
}

// Cancel asks the agent to stop the current prompt turn (session/cancel).
// Does not close the session.
func (s *Session) Cancel() error {
	sessionID := s.AgentSessionID()
	if sessionID == "" {
		return fmt.Errorf("ACP session not initialized")
	}
	if s.closed.Load() {
		return fmt.Errorf("ACP session closed")
	}
	err := s.writeJSON(map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "session/cancel",
		"params":  map[string]interface{}{"sessionId": sessionID},
	})
	if err != nil {
		return err
	}
	s.emit(Event{SessionID: s.ID, Type: "message", Role: "system", Content: "已请求终止当前回合"})
	return nil
}

// SetMode switches the agent session mode (session/set_mode).
func (s *Session) SetMode(modeID string) error {
	modeID = strings.TrimSpace(modeID)
	if modeID == "" {
		return fmt.Errorf("mode id required")
	}
	sessionID := s.AgentSessionID()
	if sessionID == "" {
		return fmt.Errorf("ACP session not initialized")
	}
	raw, err := s.call("session/set_mode", map[string]interface{}{
		"sessionId": sessionID,
		"modeId":    modeID,
	}, 20*time.Second)
	if err != nil {
		return err
	}
	// Some agents echo the new mode in the result; always notify UI optimistically.
	current := modeID
	if len(raw) > 0 {
		var resp struct {
			Modes json.RawMessage `json:"modes"`
		}
		if json.Unmarshal(raw, &resp) == nil && len(resp.Modes) > 0 {
			var modesObj interface{}
			if json.Unmarshal(resp.Modes, &modesObj) == nil {
				modes, cur := parseSessionModes(modesObj)
				if cur != "" {
					current = cur
				}
				if len(modes) > 0 {
					s.emit(Event{SessionID: s.ID, Type: "mode", Modes: modes, CurrentModeID: current})
					return nil
				}
			}
		}
	}
	s.emit(Event{SessionID: s.ID, Type: "mode", CurrentModeID: current})
	return nil
}

func (s *Session) Close() {
	if s.closed.Swap(true) {
		return
	}
	// Best-effort cancel/close.
	if sid := s.AgentSessionID(); sid != "" {
		_ = s.writeJSON(map[string]interface{}{
			"jsonrpc": "2.0",
			"method":  "session/cancel",
			"params":  map[string]interface{}{"sessionId": sid},
		})
	}
	if s.cancel != nil {
		s.cancel()
	}
	if s.transport != nil {
		_ = s.transport.Close()
	}
	s.setStatus("closed")
	s.emit(Event{SessionID: s.ID, Type: "status", Status: "closed"})
}
