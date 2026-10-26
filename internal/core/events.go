package core

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
)

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	CachedTokens     int `json:"-"`
}
type Completion struct {
	Text, Reasoning, FinishReason string
	Tools                         []ToolCall
	Usage                         Usage
}
type Delta struct {
	Text, Reasoning string
	ToolIndex       int
	Tool            *ToolCall
	Arguments       string
}
type eventParser struct {
	out     Completion
	emit    func(Delta) error
	names   map[string]string
	blocks  map[int]int
	ignored map[int]bool
	partial bool
	result  bool
	handoff bool
	failure error
	records int
	broken  int
	single  bool
	stop    stopFilter
	effort  bool
	queued  bool
	queueAt time.Time
	// queueElapsed is how long the CLI reports the model has been waiting.
	queueElapsed time.Duration
	queueCh      chan struct{}
}

func newEventParser(names map[string]string, emit func(Delta) error) *eventParser {
	return &eventParser{emit: emit, names: names, blocks: map[int]int{}, ignored: map[int]bool{}, broken: -1, queueCh: make(chan struct{})}
}
func (p *eventParser) Handoff() bool { return p.handoff }
func (p *eventParser) Feed(raw []byte) error {
	var e struct {
		Type       string          `json:"type"`
		Subtype    string          `json:"subtype"`
		Status     string          `json:"status"`
		IsError    bool            `json:"is_error"`
		Result     string          `json:"result"`
		Errors     []string        `json:"errors"`
		Error      json.RawMessage `json:"error"`
		StopReason string          `json:"stop_reason"`
		Usage      json.RawMessage `json:"usage"`
		// QueueWaitElapsed is the upstream queue wait the CLI reports.
		QueueWaitElapsed int64 `json:"queue_wait_elapsed_ms"`
		Message          struct {
			Content []struct{ Type, Text, Thinking string } `json:"content"`
		} `json:"message"`
		Event struct {
			Type         string `json:"type"`
			Index        int    `json:"index"`
			ContentBlock struct {
				Type, Text, Thinking, ID, Name string
				Input                          json.RawMessage `json:"input"`
			} `json:"content_block"`
			Delta struct {
				Type, Text, Thinking string
				PartialJSON          string `json:"partial_json"`
				StopReason           string `json:"stop_reason"`
			} `json:"delta"`
			Usage   json.RawMessage `json:"usage"`
			Message struct {
				Usage json.RawMessage `json:"usage"`
			} `json:"message"`
		} `json:"event"`
	}
	if json.Unmarshal(raw, &e) != nil {
		// The CLI cuts its last record when its stdout is closed early, so a
		// malformed line is expected rather than fatal. The record index is
		// kept so Finish() can tell a cut tail from a lost middle record.
		p.broken = p.records
		p.records++
		return nil
	}
	p.records++
	switch e.Type {
	case "stream_event":
		ev := e.Event
		switch ev.Type {
		case "message_start":
			p.setUsage(ev.Message.Usage)
		case "content_block_start":
			b := ev.ContentBlock
			if b.Type == "tool_use" {
				name, ok := p.names[b.Name]
				if !ok {
					// The CLI reports an unknown tool back to the model and
					// lets it retry, so a generation that recovers on its own
					// must not be discarded here.
					p.ignored[ev.Index] = true
					return nil
				}
				if p.single && len(p.out.Tools) > 0 {
					// The caller asked for serial tool calls, so the
					// remaining ones are dropped instead of returned.
					p.ignored[ev.Index] = true
					return nil
				}
				idx := len(p.out.Tools)
				call := ToolCall{ID: b.ID, Type: "function", Function: FunctionCall{Name: name}}
				if len(b.Input) > 0 && string(b.Input) != "{}" {
					call.Function.Arguments = string(b.Input)
				}
				p.blocks[ev.Index] = idx
				p.out.Tools = append(p.out.Tools, call)
				return p.emit(Delta{ToolIndex: idx, Tool: &call})
			}
		case "content_block_delta":
			switch ev.Delta.Type {
			case "text_delta":
				p.partial = true
				return p.emitText(ev.Delta.Text)
			case "thinking_delta":
				p.out.Reasoning += ev.Delta.Thinking
				return p.emit(Delta{Reasoning: ev.Delta.Thinking})
			case "input_json_delta":
				if p.ignored[ev.Index] {
					return nil
				}
				idx, ok := p.blocks[ev.Index]
				if !ok {
					return pluginabi.NewError("invalid_tool_delta", "Qoder returned tool arguments without a tool", 502)
				}
				p.out.Tools[idx].Function.Arguments += ev.Delta.PartialJSON
				return p.emit(Delta{ToolIndex: idx, Arguments: ev.Delta.PartialJSON})
			}
		case "message_delta":
			p.setUsage(ev.Usage)
			if ev.Delta.StopReason != "" {
				p.out.FinishReason = finishReason(ev.Delta.StopReason)
			}
			if ev.Delta.StopReason == "tool_use" && len(p.out.Tools) > 0 {
				p.handoff = true
			}
		}
	case "assistant":
		if !p.partial {
			for _, b := range e.Message.Content {
				if b.Type == "text" {
					if err := p.emitText(b.Text); err != nil {
						return err
					}
				}
			}
		}
	case "result":
		p.result = true
		p.setUsage(e.Usage)
		if e.IsError || strings.HasPrefix(e.Subtype, "error") {
			// Some failures carry their text only in the errors list.
			message := e.Result
			if message == "" && len(e.Errors) > 0 {
				message = strings.Join(e.Errors, "; ")
			}
			p.effort = effortRefused(message)
			p.failure = cliFailure(message)
			return p.failure
		}
		if p.out.Text == "" && p.stop.held == "" && e.Result != "" {
			if err := p.emitText(e.Result); err != nil {
				return err
			}
		}
		if e.StopReason != "" {
			p.out.FinishReason = finishReason(e.StopReason)
		}
	case "system":
		if e.Subtype == "model_queue_status" && e.Status == "queued" {
			if !p.queued {
				p.queued = true
				p.queueAt = time.Now()
				close(p.queueCh)
			}
			if elapsed := time.Duration(e.QueueWaitElapsed) * time.Millisecond; elapsed > p.queueElapsed {
				p.queueElapsed = elapsed
			}
		}
	case "error":
		p.failure = cliFailure("Qoder CLI reported an error")
		return p.failure
	}
	return nil
}

// Queued reports that the upstream put the requested model in a queue.
func (p *eventParser) Queued() bool { return p.queued }

// QueuedCh is closed once the upstream reports a queued model.
func (p *eventParser) QueuedCh() <-chan struct{} { return p.queueCh }

// QueueWait reports how long the model has been waiting in the upstream queue.
func (p *eventParser) QueueWait() time.Duration {
	if !p.queued {
		return 0
	}
	wait := time.Since(p.queueAt)
	if p.queueElapsed > wait {
		wait = p.queueElapsed
	}
	return wait
}

// emitText publishes model text, truncated at the caller's stop sequences.
func (p *eventParser) emitText(text string) error {
	if text == "" {
		return nil
	}
	emitted, stopped := p.stop.push(text)
	if emitted != "" {
		p.out.Text += emitted
		if err := p.emit(Delta{Text: emitted}); err != nil {
			return err
		}
	}
	if stopped {
		p.out.FinishReason = "stop"
	}
	return nil
}

func (p *eventParser) setUsage(raw []byte) {
	var u struct {
		Input   *int `json:"input_tokens"`
		Output  *int `json:"output_tokens"`
		Cached  *int `json:"cache_read_input_tokens"`
		Created *int `json:"cache_creation_input_tokens"`
	}
	if json.Unmarshal(raw, &u) != nil {
		return
	}
	if u.Input != nil {
		p.out.Usage.PromptTokens = *u.Input
	}
	if u.Output != nil {
		p.out.Usage.CompletionTokens = *u.Output
	}
	if u.Cached != nil {
		p.out.Usage.CachedTokens = *u.Cached
	}
	// Anthropic input excludes cache reads and writes; OpenAI includes both.
	if u.Input != nil {
		if u.Cached != nil {
			p.out.Usage.PromptTokens += *u.Cached
		}
		if u.Created != nil {
			p.out.Usage.PromptTokens += *u.Created
		}
	}
	p.out.Usage.TotalTokens = p.out.Usage.PromptTokens + p.out.Usage.CompletionTokens
}
func (p *eventParser) Finish() (Completion, error) {
	if p.failure != nil {
		return p.out, p.failure
	}
	if tail := p.stop.flush(); tail != "" {
		p.out.Text += tail
		if err := p.emit(Delta{Text: tail}); err != nil {
			return p.out, err
		}
	}
	if !p.result && !p.handoff && !p.tailTruncated() {
		return p.out, pluginabi.NewError("truncated_generation", "Qoder CLI ended without a terminal result", 502)
	}
	for i := range p.out.Tools {
		args := p.out.Tools[i].Function.Arguments
		if args == "" {
			args = "{}"
			p.out.Tools[i].Function.Arguments = args
			if err := p.emit(Delta{ToolIndex: i, Arguments: args}); err != nil {
				return p.out, err
			}
		}
		var object map[string]any
		if json.Unmarshal([]byte(args), &object) != nil || object == nil {
			return p.out, pluginabi.NewError("invalid_tool_arguments", "Qoder returned invalid tool arguments", 502)
		}
	}
	if len(p.out.Tools) > 0 {
		p.out.FinishReason = "tool_calls"
	}
	if p.out.FinishReason == "" {
		p.out.FinishReason = "stop"
	}
	return p.out, nil
}

// tailTruncated reports whether the CLI cut its final record. The records
// before it arrived intact, so the content collected so far is the answer.
func (p *eventParser) tailTruncated() bool {
	if p.broken < 0 || p.broken != p.records-1 {
		return false
	}
	return p.out.Text != "" || len(p.out.Tools) > 0
}
func finishReason(reason string) string {
	switch reason {
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	default:
		return "stop"
	}
}

// effortRefused reports whether the CLI rejected the request because the
// selected model has no configurable reasoning effort.
func effortRefused(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "does not support reasoning effort") ||
		strings.Contains(lower, "does not support configurable reasoning effort") ||
		strings.Contains(lower, "unsupported_reasoning_effort")
}

func cliFailure(message string) error {
	lower := strings.ToLower(message)
	if detail := cliArgumentError(message); detail != "" {
		return pluginabi.NewError("invalid_cli_arguments", "Qoder CLI rejected the request: "+detail, 400)
	}
	if queuedUpstream(lower) {
		return modelQueued()
	}
	if inputTooLong(lower) {
		// The message keeps the words "context length" so LiteLLM classifies
		// it as ContextWindowExceededError and can fall back to a bigger model.
		return pluginabi.NewError("context_length_exceeded", "Qoder model context length exceeded; shorten the input or switch to a larger-context model", 400)
	}
	code := 502
	label := "upstream_error"
	switch {
	case strings.Contains(lower, "429") || strings.Contains(lower, "rate limit"):
		code = 429
		label = "rate_limit_exceeded"
	case strings.Contains(lower, "quota") || strings.Contains(lower, "credits") || strings.Contains(lower, "403"):
		code = 403
		label = "insufficient_quota"
	case strings.Contains(lower, "401") || strings.Contains(lower, "login") || strings.Contains(lower, "auth"):
		code = 401
		label = "login_required"
	}
	return pluginabi.NewError(label, fmt.Sprintf("Qoder generation failed (%s); check the selected CLI account", label), code)
}

// queuedUpstream reports whether the upstream refused the model because it was
// at capacity. The queue answer carries a 403 status, so it is matched before
// the quota rule.
func queuedUpstream(message string) bool {
	return strings.Contains(message, "queuing failed") ||
		strings.Contains(message, "model_queued") ||
		strings.Contains(message, "resource_limit") ||
		strings.Contains(message, "isqueued")
}

// modelQueued reports the upstream capacity condition. The request is not
// broken, so the caller is told to retry instead of seeing a hard failure.
func modelQueued() error {
	return pluginabi.NewError("model_queued", "Qoder model is queued upstream (service unavailable); retry shortly", 429)
}

// inputTooLong reports whether the upstream rejected the request because the
// input exceeded the model's context window. The two CLI builds word the
// refusal differently, so both families are matched.
func inputTooLong(message string) bool {
	if strings.Contains(message, "input is too long") ||
		strings.Contains(message, "input content too long") ||
		strings.Contains(message, "too long for this model") ||
		strings.Contains(message, "request entity too large") ||
		strings.Contains(message, "payload_too_large") ||
		strings.Contains(message, "context length") ||
		strings.Contains(message, "range of input length") {
		return true
	}
	return strings.Contains(message, "too long") &&
		(strings.Contains(message, "input") || strings.Contains(message, "model"))
}

// cliArgumentError extracts the CLI's fatal startup message, which is far
// more actionable than a generic upstream failure.
func cliArgumentError(message string) string {
	const marker = "unexpected critical error"
	index := strings.Index(strings.ToLower(message), marker)
	if index < 0 {
		return ""
	}
	detail := strings.TrimSpace(message[index+len(marker):])
	detail = strings.TrimSpace(strings.TrimPrefix(detail, "occurred:"))
	detail = strings.TrimSpace(strings.TrimPrefix(detail, "Error:"))
	if line, _, found := strings.Cut(detail, "\n"); found {
		detail = strings.TrimSpace(line)
	}
	if detail == "" {
		return ""
	}
	if len(detail) > 200 {
		detail = detail[:200]
	}
	return detail
}
