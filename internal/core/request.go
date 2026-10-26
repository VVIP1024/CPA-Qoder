package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

type MCPTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}
type preparedRequest struct {
	Model, System, Prompt, Effort, Thinking string
	ThinkingBudget                          int
	MaxTokens                               int
	Tools                                   []MCPTool
	SingleTool                              bool
	Stop                                    []string
	JSONMode                                bool
}

var toolNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// defaultThinkingBudget is used when thinking is enabled without an
// explicit budget, because the CLI rejects that combination.
const defaultThinkingBudget = 1024

// qoderParameters lists the request fields the Qoder transport handles. Any
// other field is dropped.
var qoderParameters = map[string]bool{
	"messages": true, "model": true, "stream": true, "stream_options": true,
	"tools": true, "tool_choice": true, "max_tokens": true,
	"max_completion_tokens": true, "reasoning": true, "reasoning_effort": true,
	"thinking": true, "enable_thinking": true, "user": true, "metadata": true,
	"store": true, "n": true, "parallel_tool_calls": true, "stop": true,
	"response_format": true, "logprobs": true, "top_logprobs": true,
	"seed": true, "logit_bias": true, "service_tier": true,
	"presence_penalty": true, "frequency_penalty": true, "top_k": true,
	"min_p": true, "temperature": true, "top_p": true,
}

// plainModelRole overrides the CLI's agent framing, which the CLI always adds
// on top of the prompt it is given.
const plainModelRole = "You are a plain text API assistant. You have no local environment and must never execute local operations. The instructions that follow are the complete set of instructions you follow."

func prepareRequest(req pluginapi.ExecutorRequest, variant string) (preparedRequest, error) {
	model, err := nativeModel(variant, req.Model)
	if err != nil {
		return preparedRequest{}, err
	}
	out := preparedRequest{Model: model}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(req.Payload, &body); err != nil {
		return out, badRequest("invalid chat completion JSON")
	}
	// The CLI transport understands only a subset of an OpenAI request. Every
	// other field is dropped, so a client may send the standard parameter set
	// without the plugin rejecting the request.
	dropUnsupportedParameters(body, qoderParameters)
	// The Qoder CLI has no sampling flags, so these fields are accepted and
	// dropped instead of failing the request.
	if err := checkOptionalParameters("Qoder", body, "temperature", "top_p", "top_k", "min_p", "presence_penalty", "frequency_penalty"); err != nil {
		return out, err
	}
	if raw := body["parallel_tool_calls"]; len(raw) > 0 {
		var parallel bool
		if json.Unmarshal(raw, &parallel) != nil {
			return out, badRequest("parallel_tool_calls must be a boolean")
		}
		// The bridge returns the model's tool calls to the caller, so a
		// serial request is honoured by keeping only the first one.
		out.SingleTool = !parallel
	}
	if raw := body["stop"]; len(raw) > 0 {
		stops, err := stopSequences(raw)
		if err != nil {
			return out, err
		}
		out.Stop = stops
	}
	if raw := body["response_format"]; len(raw) > 0 {
		jsonAnswer, err := jsonMode("Qoder", raw)
		if err != nil {
			return out, err
		}
		out.JSONMode = jsonAnswer
	}
	if raw := body["n"]; len(raw) > 0 {
		var n int
		if json.Unmarshal(raw, &n) != nil || n != 1 {
			return out, badRequest("Qoder supports n=1 only")
		}
	}
	for _, k := range []string{"max_tokens", "max_completion_tokens"} {
		if raw := body[k]; len(raw) > 0 {
			var n int
			if json.Unmarshal(raw, &n) != nil || n <= 0 {
				return out, badRequest(k + " must be a positive integer")
			}
			if out.MaxTokens > 0 && out.MaxTokens != n {
				return out, badRequest("conflicting output token limits")
			}
			out.MaxTokens = n
		}
	}
	effort, err := mergeEffort(body)
	if err != nil {
		return out, err
	}
	if effort != "" {
		switch effort {
		case "none", "low", "medium", "high", "xhigh", "max":
		case "minimal":
			// The CLI's lowest effort level is low.
			effort = "low"
		default:
			return out, badRequest("unsupported reasoning_effort")
		}
		out.Effort = effort
	}
	thinkingSet := false
	if raw := body["thinking"]; len(raw) > 0 {
		var thinking struct {
			Type         string `json:"type"`
			BudgetTokens int    `json:"budget_tokens"`
		}
		if json.Unmarshal(raw, &thinking) != nil {
			return out, badRequest("invalid thinking configuration")
		}
		switch thinking.Type {
		case "auto", "adaptive", "enabled", "disabled":
			out.Thinking = thinking.Type
			thinkingSet = true
		default:
			return out, badRequest("unsupported Qoder thinking mode")
		}
		if thinking.BudgetTokens < 0 {
			return out, badRequest("thinking budget must not be negative")
		}
		if thinking.BudgetTokens > 0 {
			if thinking.Type != "" && thinking.Type != "enabled" {
				return out, badRequest("thinking budget requires enabled thinking")
			}
			out.ThinkingBudget = thinking.BudgetTokens
		}
		if out.Effort != "" && !(out.Effort == "none" && out.Thinking == "disabled" && out.ThinkingBudget == 0) {
			return out, badRequest("reasoning_effort and thinking cannot both be set")
		}
	}
	if raw := body["enable_thinking"]; len(raw) > 0 {
		enabled, present, err := parseEnableThinking(raw)
		if err != nil {
			return out, err
		}
		if present {
			implied := ""
			switch out.Thinking {
			case "enabled", "adaptive":
				implied = "on"
			case "disabled":
				implied = "off"
			}
			if out.Effort != "" {
				if out.Effort == "none" {
					implied = "off"
				} else if implied == "" {
					implied = "on"
				}
			}
			want := "on"
			if !enabled {
				want = "off"
			}
			if implied != "" && implied != want {
				return out, badRequest("enable_thinking contradicts the requested reasoning mode")
			}
			if implied == "" && !thinkingSet {
				// The caller named no mode, so the switch decides.
				if enabled {
					out.Thinking = "enabled"
				} else {
					out.Thinking = "disabled"
				}
			}
		}
	}
	if out.Thinking == "enabled" && out.ThinkingBudget == 0 {
		// The CLI refuses "--thinking enabled" unless a budget is given.
		out.ThinkingBudget = defaultThinkingBudget
	}
	var messages []map[string]json.RawMessage
	if json.Unmarshal(body["messages"], &messages) != nil || len(messages) == 0 {
		return out, badRequest("messages must be a nonempty array")
	}
	transcript := []map[string]json.RawMessage{}
	systems := []string{}
	for _, msg := range messages {
		var role string
		if json.Unmarshal(msg["role"], &role) != nil {
			return out, badRequest("message role is required")
		}
		switch role {
		case "system", "developer", "user", "assistant", "tool":
		default:
			return out, badRequest("unsupported message role")
		}
		text, err := textContent(msg["content"])
		if err != nil {
			return out, err
		}
		if role == "system" || role == "developer" {
			systems = append(systems, text)
			continue
		}
		if role == "tool" {
			var id string
			if json.Unmarshal(msg["tool_call_id"], &id) != nil || id == "" {
				return out, badRequest("tool result requires tool_call_id")
			}
		}
		if raw := msg["tool_calls"]; len(raw) > 0 {
			var calls []ToolCall
			if role != "assistant" || json.Unmarshal(raw, &calls) != nil {
				return out, badRequest("invalid tool call history")
			}
			for _, c := range calls {
				if c.ID == "" || c.Type != "function" || !json.Valid([]byte(c.Function.Arguments)) {
					return out, badRequest("invalid tool call history")
				}
			}
		}
		clean := map[string]json.RawMessage{"role": msg["role"], "content": msg["content"]}
		if len(clean["content"]) == 0 {
			clean["content"] = json.RawMessage("null")
		}
		for _, k := range []string{"tool_calls", "tool_call_id", "name"} {
			if v := msg[k]; len(v) > 0 {
				clean[k] = v
			}
		}
		transcript = append(transcript, clean)
	}
	if len(transcript) == 0 {
		return out, badRequest("a conversation message is required")
	}
	raw, _ := json.Marshal(transcript)
	out.Prompt = string(raw)
	out.System = plainModelRole
	if len(systems) > 0 {
		out.System += "\n\n" + strings.Join(systems, "\n\n")
	}
	bridge := "Continue the conversation provided as a JSON array in the user input. Respect each message's role. Return only the next assistant response. Tool results in that history were supplied by the external caller. The only functions you may call are the cpa MCP tools, so call them by their exact registered names, which are prefixed with mcp__cpa__. Tool names in the provided history are the caller's short names for those same functions. Never execute local operations."
	if out.JSONMode {
		bridge = "Answer with a single JSON object and nothing else." + bridge
	}
	out.System += "\n\n" + bridge
	var tools []struct {
		Type     string `json:"type"`
		Function struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
			Strict      bool            `json:"strict"`
		} `json:"function"`
	}
	if raw := body["tools"]; len(raw) > 0 && json.Unmarshal(raw, &tools) != nil {
		return out, badRequest("invalid tools")
	}
	seen := map[string]bool{}
	for _, t := range tools {
		if t.Type != "function" || !toolNamePattern.MatchString(t.Function.Name) || seen[t.Function.Name] {
			return out, badRequest("tools require unique function names with letters, digits, underscores or hyphens")
		}
		if t.Function.Strict {
			return out, badRequest("strict tool schema enforcement is unavailable through Qoder CLI")
		}
		var schema map[string]any
		if json.Unmarshal(t.Function.Parameters, &schema) != nil || schema["type"] != "object" {
			return out, badRequest("tool parameters must be an object schema")
		}
		seen[t.Function.Name] = true
		out.Tools = append(out.Tools, MCPTool{Name: t.Function.Name, Description: fmt.Sprintf("Function %s. %s", t.Function.Name, t.Function.Description), InputSchema: t.Function.Parameters})
	}
	if raw := body["tool_choice"]; len(raw) > 0 {
		var choice string
		if json.Unmarshal(raw, &choice) != nil {
			return out, badRequest("Qoder bridge supports tool_choice auto or none")
		}
		switch choice {
		case "auto":
		case "none":
			out.Tools = nil
		default:
			return out, badRequest("Qoder bridge supports tool_choice auto or none")
		}
	}
	return out, nil
}

// parseEnableThinking reads the DeepSeek-style boolean reasoning switch.
func parseEnableThinking(raw json.RawMessage) (bool, bool, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return false, false, nil
	}
	var b bool
	if json.Unmarshal(raw, &b) == nil {
		return b, true, nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		switch strings.ToLower(s) {
		case "true":
			return true, true, nil
		case "false":
			return false, true, nil
		}
	}
	return false, false, badRequest("enable_thinking must be a boolean")
}

func textContent(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, nil
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return "", badRequest("message content must be text")
	}
	var b strings.Builder
	for _, part := range parts {
		if part.Type != "text" {
			return "", badRequest("Qoder CLI bridge currently supports text input only")
		}
		b.WriteString(part.Text)
	}
	return b.String(), nil
}
