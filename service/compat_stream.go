package service

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"

	"codebuddy-gateway/global"

	"github.com/gin-gonic/gin"
)

type streamAdapter interface {
	start()
	setID(id string)
	setModel(model string)
	setUsage(u *parsedUsage)
	onReasoning(s string)
	onText(s string)
	onToolCall(tc AggregatedToolCall)
	onFinishReason(s string)
	finish() error
}

type toolStreamState struct {
	index       int
	outputIndex int
	itemID      string
	callID      string
	name        string
	args        string
	opened      bool
}

func newStreamAdapter(proto Protocol, w io.Writer, flusher http.Flusher, model string) streamAdapter {
	if proto.IsAnthropic() {
		return &anthropicAdapter{
			w:       w,
			flusher: flusher,
			id:      newID("msg_"),
			model:   model,
			created: time.Now().Unix(),
			tools:   map[int]*toolStreamState{},
		}
	}
	return &responsesAdapter{
		w:       w,
		flusher: flusher,
		id:      newID("resp_"),
		model:   model,
		created: time.Now().Unix(),
		tools:   map[int]*toolStreamState{},
	}
}

func writeSSEEvent(w io.Writer, flusher http.Flusher, event string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	if event != "" {
		buf.WriteString("event: ")
		buf.WriteString(event)
		buf.WriteByte('\n')
	}
	buf.WriteString("data: ")
	buf.Write(data)
	buf.WriteString("\n\n")
	if _, err := w.Write(buf.Bytes()); err != nil {
		return err
	}
	if flusher != nil {
		flusher.Flush()
	}
	return nil
}

func applyChunkToAdapter(em streamAdapter, chunk map[string]any) {
	if em == nil || chunk == nil {
		return
	}
	if id, _ := chunk["id"].(string); id != "" {
		em.setID(id)
	}
	if model, _ := chunk["model"].(string); model != "" {
		em.setModel(model)
	}
	choices, _ := chunk["choices"].([]any)
	for _, item := range choices {
		choice, _ := item.(map[string]any)
		if choice == nil {
			continue
		}
		if fr, ok := choice["finish_reason"].(string); ok && fr != "" {
			em.onFinishReason(fr)
		}
		delta, _ := choice["delta"].(map[string]any)
		if delta == nil {
			if msg, ok := choice["message"].(map[string]any); ok {
				delta = msg
			}
		}
		if delta == nil {
			continue
		}
		if s := deltaString(delta["reasoning_content"]); s != "" && global.CORE_CONFIG.Gateway.Passthrough {
			em.onReasoning(s)
		}
		if s := deltaString(delta["content"]); s != "" {
			em.onText(s)
		}
		if tcs, ok := delta["tool_calls"].([]any); ok {
			for _, tc := range tcs {
				em.onToolCall(parseToolCallDelta(tc))
			}
		}
	}
}

type responsesAdapter struct {
	w               io.Writer
	flusher         http.Flusher
	err             error
	id              string
	model           string
	created         int64
	seq             int
	out             int
	reasoningOpen   bool
	reasoningIdx    int
	reasoningItemID string
	reasoning       bytes.Buffer
	textOpen        bool
	textIdx         int
	textItemID      string
	text            bytes.Buffer
	tools           map[int]*toolStreamState
	toolOrder       []int
	usage           *parsedUsage
	finishReason    string
}

func (a *responsesAdapter) emit(typ string, payload map[string]any) {
	if a.err != nil {
		return
	}
	a.seq++
	payload["type"] = typ
	payload["sequence_number"] = a.seq
	a.err = writeSSEEvent(a.w, a.flusher, typ, payload)
}

func (a *responsesAdapter) start() {
	a.emit("response.created", map[string]any{"response": a.skeleton("in_progress")})
	a.emit("response.in_progress", map[string]any{"response": a.skeleton("in_progress")})
}

func (a *responsesAdapter) setID(id string) {
	if a.id == "" && id != "" {
		a.id = ensureID(id, "resp_")
	}
}

func (a *responsesAdapter) setModel(model string) {
	if model != "" {
		a.model = model
	}
}

func (a *responsesAdapter) setUsage(u *parsedUsage) {
	a.usage = u
}

func (a *responsesAdapter) onFinishReason(s string) {
	if s != "" {
		a.finishReason = s
	}
}

func (a *responsesAdapter) onReasoning(s string) {
	if s == "" {
		return
	}
	if !a.reasoningOpen {
		a.reasoningItemID = newID("rs_")
		a.reasoningIdx = a.out
		a.out++
		a.emit("response.output_item.added", map[string]any{
			"output_index": a.reasoningIdx,
			"item": map[string]any{
				"id":      a.reasoningItemID,
				"type":    "reasoning",
				"summary": []any{},
			},
		})
		a.emit("response.reasoning_summary_part.added", map[string]any{
			"item_id":       a.reasoningItemID,
			"output_index":  a.reasoningIdx,
			"summary_index": 0,
			"part":          map[string]any{"type": "summary_text", "text": ""},
		})
		a.reasoningOpen = true
	}
	a.reasoning.WriteString(s)
	a.emit("response.reasoning_summary_text.delta", map[string]any{
		"item_id":       a.reasoningItemID,
		"output_index":  a.reasoningIdx,
		"summary_index": 0,
		"delta":         s,
	})
}

func (a *responsesAdapter) closeReasoning() {
	if !a.reasoningOpen {
		return
	}
	text := a.reasoning.String()
	a.emit("response.reasoning_summary_text.done", map[string]any{
		"item_id":       a.reasoningItemID,
		"output_index":  a.reasoningIdx,
		"summary_index": 0,
		"text":          text,
	})
	a.emit("response.reasoning_summary_part.done", map[string]any{
		"item_id":       a.reasoningItemID,
		"output_index":  a.reasoningIdx,
		"summary_index": 0,
		"part":          map[string]any{"type": "summary_text", "text": text},
	})
	a.emit("response.output_item.done", map[string]any{
		"output_index": a.reasoningIdx,
		"item": map[string]any{
			"id":   a.reasoningItemID,
			"type": "reasoning",
			"summary": []any{
				map[string]any{"type": "summary_text", "text": text},
			},
		},
	})
	a.reasoningOpen = false
}

func (a *responsesAdapter) onText(s string) {
	if s == "" {
		return
	}
	a.closeReasoning()
	a.ensureTextItem()
	a.text.WriteString(s)
	a.emit("response.output_text.delta", map[string]any{
		"item_id":       a.textItemID,
		"output_index":  a.textIdx,
		"content_index": 0,
		"delta":         s,
	})
}

func (a *responsesAdapter) closeText() {
	if !a.textOpen {
		return
	}
	text := a.text.String()
	a.emit("response.output_text.done", map[string]any{
		"item_id":       a.textItemID,
		"output_index":  a.textIdx,
		"content_index": 0,
		"text":          text,
	})
	a.emit("response.content_part.done", map[string]any{
		"item_id":       a.textItemID,
		"output_index":  a.textIdx,
		"content_index": 0,
		"part":          map[string]any{"type": "output_text", "text": text},
	})
	a.emit("response.output_item.done", map[string]any{
		"output_index": a.textIdx,
		"item": map[string]any{
			"id":     a.textItemID,
			"type":   "message",
			"status": "completed",
			"role":   "assistant",
			"content": []any{
				map[string]any{"type": "output_text", "text": text},
			},
		},
	})
	a.textOpen = false
}

func (a *responsesAdapter) onToolCall(tc AggregatedToolCall) {
	a.closeReasoning()
	a.closeText()
	st, ok := a.tools[tc.Index]
	if !ok {
		st = &toolStreamState{index: tc.Index, outputIndex: a.out}
		a.out++
		a.tools[tc.Index] = st
		a.toolOrder = append(a.toolOrder, tc.Index)
	}
	if tc.ID != "" {
		st.callID = tc.ID
	}
	if tc.Name != "" {
		st.name = tc.Name
	}
	if !st.opened {
		if st.callID == "" {
			st.callID = newID("call_")
		}
		st.itemID = newID("fc_")
		a.emit("response.output_item.added", map[string]any{
			"output_index": st.outputIndex,
			"item": map[string]any{
				"id":        st.itemID,
				"type":      "function_call",
				"status":    "in_progress",
				"call_id":   st.callID,
				"name":      st.name,
				"arguments": "",
			},
		})
		st.opened = true
	}
	if tc.Arguments != "" {
		st.args += tc.Arguments
		a.emit("response.function_call_arguments.delta", map[string]any{
			"item_id":      st.itemID,
			"output_index": st.outputIndex,
			"delta":        tc.Arguments,
		})
	}
}

func (a *responsesAdapter) closeTools() {
	for _, idx := range a.toolOrder {
		st := a.tools[idx]
		if st == nil || !st.opened {
			continue
		}
		a.emit("response.function_call_arguments.done", map[string]any{
			"item_id":      st.itemID,
			"output_index": st.outputIndex,
			"arguments":    st.args,
		})
		a.emit("response.output_item.done", map[string]any{
			"output_index": st.outputIndex,
			"item": map[string]any{
				"id":        st.itemID,
				"type":      "function_call",
				"status":    "completed",
				"call_id":   st.callID,
				"name":      st.name,
				"arguments": st.args,
			},
		})
		st.opened = false
	}
}

func (a *responsesAdapter) finish() error {
	a.closeReasoning()
	if a.textOpen {
		a.closeText()
	} else if len(a.toolOrder) == 0 && a.reasoning.Len() == 0 {
		a.ensureTextItem()
		a.closeText()
	}
	a.closeTools()
	status := "completed"
	if a.finishReason == "length" {
		status = "incomplete"
	}
	a.emit("response.completed", map[string]any{"response": a.full(status)})
	return a.err
}

func (a *responsesAdapter) ensureTextItem() {
	if a.textOpen {
		return
	}
	a.textItemID = newID("msg_")
	a.textIdx = a.out
	a.out++
	a.emit("response.output_item.added", map[string]any{
		"output_index": a.textIdx,
		"item": map[string]any{
			"id":      a.textItemID,
			"type":    "message",
			"status":  "in_progress",
			"role":    "assistant",
			"content": []any{},
		},
	})
	a.emit("response.content_part.added", map[string]any{
		"item_id":       a.textItemID,
		"output_index":  a.textIdx,
		"content_index": 0,
		"part":          map[string]any{"type": "output_text", "text": ""},
	})
	a.textOpen = true
}

func (a *responsesAdapter) skeleton(status string) map[string]any {
	return map[string]any{
		"id":                 a.id,
		"object":             "response",
		"created_at":         a.created,
		"status":             status,
		"model":              a.model,
		"output":             []any{},
		"error":              nil,
		"incomplete_details": nil,
	}
}

func (a *responsesAdapter) full(status string) map[string]any {
	output := make([]any, 0, 2+len(a.toolOrder))
	if a.reasoning.Len() > 0 {
		output = append(output, map[string]any{
			"id":   a.reasoningItemID,
			"type": "reasoning",
			"summary": []any{
				map[string]any{"type": "summary_text", "text": a.reasoning.String()},
			},
		})
	}
	if a.text.Len() > 0 || len(a.toolOrder) == 0 {
		itemID := a.textItemID
		if itemID == "" {
			itemID = newID("msg_")
		}
		output = append(output, map[string]any{
			"id":     itemID,
			"type":   "message",
			"status": "completed",
			"role":   "assistant",
			"content": []any{
				map[string]any{"type": "output_text", "text": a.text.String()},
			},
		})
	}
	for _, idx := range a.toolOrder {
		st := a.tools[idx]
		if st == nil {
			continue
		}
		output = append(output, map[string]any{
			"id":        st.itemID,
			"type":      "function_call",
			"status":    "completed",
			"call_id":   st.callID,
			"name":      st.name,
			"arguments": st.args,
		})
	}
	usage := map[string]any{
		"input_tokens":  0,
		"output_tokens": 0,
		"total_tokens":  0,
	}
	if a.usage != nil {
		usage["input_tokens"] = a.usage.PromptTokens
		usage["output_tokens"] = a.usage.CompletionTokens
		usage["total_tokens"] = a.usage.TotalTokens
	}
	return map[string]any{
		"id":         a.id,
		"object":     "response",
		"created_at": a.created,
		"status":     status,
		"model":      a.model,
		"output":     output,
		"usage":      usage,
	}
}

type anthropicAdapter struct {
	w            io.Writer
	flusher      http.Flusher
	err          error
	id           string
	model        string
	created      int64
	block        int
	thinkingOpen bool
	thinkingIdx  int
	thinking     bytes.Buffer
	textOpen     bool
	textIdx      int
	text         bytes.Buffer
	tools        map[int]*toolStreamState
	toolOrder    []int
	usage        *parsedUsage
	finishReason string
}

func (a *anthropicAdapter) emit(typ string, payload map[string]any) {
	if a.err != nil {
		return
	}
	if _, ok := payload["type"]; !ok {
		payload["type"] = typ
	}
	a.err = writeSSEEvent(a.w, a.flusher, typ, payload)
}

func (a *anthropicAdapter) start() {
	input := 0
	if a.usage != nil {
		input = a.usage.PromptTokens
	}
	a.emit("message_start", map[string]any{
		"message": map[string]any{
			"id":            a.id,
			"type":          "message",
			"role":          "assistant",
			"content":       []any{},
			"model":         a.model,
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]any{
				"input_tokens":  input,
				"output_tokens": 0,
			},
		},
	})
}

func (a *anthropicAdapter) setID(id string) {
	if a.id == "" && id != "" {
		a.id = ensureID(id, "msg_")
	}
}

func (a *anthropicAdapter) setModel(model string) {
	if model != "" {
		a.model = model
	}
}

func (a *anthropicAdapter) setUsage(u *parsedUsage) {
	a.usage = u
}

func (a *anthropicAdapter) onFinishReason(s string) {
	if s != "" {
		a.finishReason = s
	}
}

func (a *anthropicAdapter) onReasoning(s string) {
	if s == "" {
		return
	}
	if !a.thinkingOpen {
		a.thinkingIdx = a.block
		a.block++
		a.emit("content_block_start", map[string]any{
			"index": a.thinkingIdx,
			"content_block": map[string]any{
				"type":     "thinking",
				"thinking": "",
			},
		})
		a.thinkingOpen = true
	}
	a.thinking.WriteString(s)
	a.emit("content_block_delta", map[string]any{
		"index": a.thinkingIdx,
		"delta": map[string]any{"type": "thinking_delta", "thinking": s},
	})
}

func (a *anthropicAdapter) closeThinking() {
	if !a.thinkingOpen {
		return
	}
	a.emit("content_block_stop", map[string]any{"index": a.thinkingIdx})
	a.thinkingOpen = false
}

func (a *anthropicAdapter) onText(s string) {
	if s == "" {
		return
	}
	a.closeThinking()
	if !a.textOpen {
		a.textIdx = a.block
		a.block++
		a.emit("content_block_start", map[string]any{
			"index": a.textIdx,
			"content_block": map[string]any{
				"type": "text",
				"text": "",
			},
		})
		a.textOpen = true
	}
	a.text.WriteString(s)
	a.emit("content_block_delta", map[string]any{
		"index": a.textIdx,
		"delta": map[string]any{"type": "text_delta", "text": s},
	})
}

func (a *anthropicAdapter) closeText() {
	if !a.textOpen {
		return
	}
	a.emit("content_block_stop", map[string]any{"index": a.textIdx})
	a.textOpen = false
}

func (a *anthropicAdapter) onToolCall(tc AggregatedToolCall) {
	a.closeThinking()
	a.closeText()
	st, ok := a.tools[tc.Index]
	if !ok {
		st = &toolStreamState{index: tc.Index, outputIndex: a.block}
		a.block++
		a.tools[tc.Index] = st
		a.toolOrder = append(a.toolOrder, tc.Index)
	}
	if tc.ID != "" {
		st.callID = tc.ID
	}
	if tc.Name != "" {
		st.name = tc.Name
	}
	if !st.opened {
		if st.callID == "" {
			st.callID = newID("toolu_")
		}
		a.emit("content_block_start", map[string]any{
			"index": st.outputIndex,
			"content_block": map[string]any{
				"type":  "tool_use",
				"id":    st.callID,
				"name":  st.name,
				"input": map[string]any{},
			},
		})
		st.opened = true
	}
	if tc.Arguments != "" {
		st.args += tc.Arguments
		a.emit("content_block_delta", map[string]any{
			"index": st.outputIndex,
			"delta": map[string]any{"type": "input_json_delta", "partial_json": tc.Arguments},
		})
	}
}

func (a *anthropicAdapter) closeTools() {
	for _, idx := range a.toolOrder {
		st := a.tools[idx]
		if st == nil || !st.opened {
			continue
		}
		a.emit("content_block_stop", map[string]any{"index": st.outputIndex})
		st.opened = false
	}
}

func (a *anthropicAdapter) finish() error {
	a.closeThinking()
	if a.textOpen {
		a.closeText()
	} else if len(a.toolOrder) == 0 && a.thinking.Len() == 0 {
		a.textIdx = a.block
		a.block++
		a.emit("content_block_start", map[string]any{
			"index":         a.textIdx,
			"content_block": map[string]any{"type": "text", "text": ""},
		})
		a.textOpen = true
		a.closeText()
	}
	a.closeTools()
	outTokens := 0
	if a.usage != nil {
		outTokens = a.usage.CompletionTokens
	}
	a.emit("message_delta", map[string]any{
		"delta": map[string]any{
			"stop_reason":   mapAnthropicStop(a.finishReason),
			"stop_sequence": nil,
		},
		"usage": map[string]any{"output_tokens": outTokens},
	})
	a.emit("message_stop", map[string]any{})
	return a.err
}

func (p *Proxy) writeCompatJSON(c *gin.Context, resp *http.Response, meta *ChatRequestMeta) (*parsedUsage, error) {
	result, err := collectSSE(resp.Body, meta.RequestedModel)
	if err != nil {
		return nil, err
	}
	if result.Usage != nil && result.Usage.RequestID == "" {
		result.Usage.RequestID = resp.Header.Get("X-Request-Id")
	}
	var encoded []byte
	switch meta.Protocol {
	case ProtocolAnthropic:
		encoded, err = encodeAnthropicJSON(result)
		c.Header("anthropic-version", "2023-06-01")
	default:
		encoded, err = encodeResponsesJSON(result)
	}
	if err != nil {
		return result.Usage, err
	}
	c.Header("Content-Type", "application/json")
	c.Status(http.StatusOK)
	_, writeErr := c.Writer.Write(encoded)
	return result.Usage, writeErr
}

func (p *Proxy) writeCompatStream(c *gin.Context, resp *http.Response, meta *ChatRequestMeta) (*parsedUsage, error) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	if meta.Protocol.IsAnthropic() {
		c.Header("anthropic-version", "2023-06-01")
	}
	c.Status(http.StatusOK)
	flusher, _ := c.Writer.(http.Flusher)
	sink := newSSESink(c.Writer, flusher)

	em := newStreamAdapter(meta.Protocol, sink, sink, meta.RequestedModel)
	em.start()

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	streamStart := time.Now()
	var usage *parsedUsage
	var firstTokenMs int64
	reqID := resp.Header.Get("X-Request-Id")

	// 上游思考期间可能长时间不吐字，中间任何一层代理都可能按空闲超时掐连接。
	// 所以这里定期补一个 SSE 注释帧（心跳），保证链路上始终有字节流动。
	stopHeartbeat := startSSEHeartbeat(sink)
	defer stopHeartbeat()

	for scanner.Scan() {
		line := scanner.Text()
		if firstTokenMs == 0 && lineHasGeneratedToken(line) {
			firstTokenMs = time.Since(streamStart).Milliseconds()
		}
		done, chunk := parseChatSSELine(line)
		if done {
			break
		}
		if chunk == nil {
			continue
		}
		usage = mergeUsage(usage, extractUsage(chunk))
		applyChunkToAdapter(em, chunk)
	}
	if usage == nil {
		usage = &parsedUsage{}
	}
	usage.FirstTokenMs = firstTokenMs
	if reqID != "" && usage.RequestID == "" {
		usage.RequestID = reqID
	}
	em.setUsage(usage)
	finishErr := em.finish()
	if err := scanner.Err(); err != nil {
		return usage, err
	}
	return usage, finishErr
}

// sseHeartbeatInterval 是心跳间隔。取 15s 是为了留足余量：
// 常见反代/网关的空闲超时通常在 60s 上下，15s 能稳定「喂饱」它们。
const sseHeartbeatInterval = 15 * time.Second

// sseSink 把下游 SSE 的 Write/Flush 串行化。
// 心跳 goroutine 和主循环会同时写同一个 ResponseWriter，不锁就会把 event/data 帧撕开，
// 客户端（Codex）表现为「写着写着突然断了 / Conversation interrupted」。
type sseSink struct {
	mu      sync.Mutex
	w       io.Writer
	flusher http.Flusher
}

func newSSESink(w io.Writer, flusher http.Flusher) *sseSink {
	return &sseSink{w: w, flusher: flusher}
}

func (s *sseSink) Write(p []byte) (int, error) {
	if s == nil || s.w == nil {
		return 0, io.ErrClosedPipe
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.w.Write(p)
	if err == nil && s.flusher != nil {
		s.flusher.Flush()
	}
	return n, err
}

func (s *sseSink) Flush() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.flusher != nil {
		s.flusher.Flush()
	}
}

// startSSEHeartbeat 在上游静默期间持续发送 SSE 注释帧（": ping"）。
// 注释帧不会被任何符合规范的 SSE 客户端当成数据，只用于保活。
// 返回的 stop 函数会在结束后停止心跳并排空 goroutine。
func startSSEHeartbeat(w io.Writer) func() {
	return startSSEHeartbeatInterval(w, sseHeartbeatInterval)
}

func startSSEHeartbeatInterval(w io.Writer, interval time.Duration) func() {
	if interval <= 0 {
		interval = sseHeartbeatInterval
	}
	done := make(chan struct{})
	var once sync.Once
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
					return
				}
			}
		}
	}()
	return func() {
		once.Do(func() { close(done) })
	}
}
