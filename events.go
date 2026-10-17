package main

import (
	"encoding/json"
	"fmt"
	"strings"

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
	partial bool
	result  bool
	handoff bool
	failure error
}

func newEventParser(names map[string]string, emit func(Delta) error) *eventParser {
	return &eventParser{emit: emit, names: names, blocks: map[int]int{}}
}
func (p *eventParser) Handoff() bool { return p.handoff }
func (p *eventParser) Feed(raw []byte) error {
	var e struct {
		Type       string          `json:"type"`
		Subtype    string          `json:"subtype"`
		IsError    bool            `json:"is_error"`
		Result     string          `json:"result"`
		Error      json.RawMessage `json:"error"`
		StopReason string          `json:"stop_reason"`
		Usage      json.RawMessage `json:"usage"`
		Message    struct {
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
		return pluginabi.NewError("invalid_cli_output", "Qoder returned malformed stream JSON", 502)
	}
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
					return pluginabi.NewError("unexpected_tool", "Qoder attempted an undeclared tool", 502)
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
				p.out.Text += ev.Delta.Text
				return p.emit(Delta{Text: ev.Delta.Text})
			case "thinking_delta":
				p.out.Reasoning += ev.Delta.Thinking
				return p.emit(Delta{Reasoning: ev.Delta.Thinking})
			case "input_json_delta":
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
					p.out.Text += b.Text
					if err := p.emit(Delta{Text: b.Text}); err != nil {
						return err
					}
				}
			}
		}
	case "result":
		p.result = true
		p.setUsage(e.Usage)
		if e.IsError || strings.HasPrefix(e.Subtype, "error") {
			p.failure = cliFailure(e.Result)
			return p.failure
		}
		if p.out.Text == "" && e.Result != "" {
			p.out.Text = e.Result
			if err := p.emit(Delta{Text: e.Result}); err != nil {
				return err
			}
		}
		if e.StopReason != "" {
			p.out.FinishReason = finishReason(e.StopReason)
		}
	case "error":
		p.failure = cliFailure("Qoder CLI reported an error")
		return p.failure
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
	if !p.result && !p.handoff {
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
func cliFailure(message string) error {
	lower := strings.ToLower(message)
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
