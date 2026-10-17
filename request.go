package main

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
	MaxTokens                               int
	Tools                                   []MCPTool
}

var toolNamePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

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
	allowed := map[string]bool{"messages": true, "model": true, "stream": true, "stream_options": true, "tools": true, "tool_choice": true, "max_tokens": true, "max_completion_tokens": true, "reasoning_effort": true, "thinking": true, "user": true, "metadata": true, "store": true, "n": true}
	for k, v := range body {
		if !allowed[k] && !bytes.Equal(v, []byte("null")) {
			return out, badRequest("Qoder CLI transport does not support parameter: " + k)
		}
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
	if raw := body["reasoning_effort"]; len(raw) > 0 {
		if json.Unmarshal(raw, &out.Effort) != nil {
			return out, badRequest("reasoning_effort must be a string")
		}
		switch out.Effort {
		case "none", "minimal", "low", "medium", "high", "xhigh", "max":
		default:
			return out, badRequest("unsupported reasoning_effort")
		}
	}
	if raw := body["thinking"]; len(raw) > 0 {
		var thinking struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(raw, &thinking) != nil {
			return out, badRequest("invalid thinking configuration")
		}
		switch thinking.Type {
		case "disabled", "adaptive":
			out.Thinking = thinking.Type
		default:
			return out, badRequest("Qoder bridge accepts disabled or adaptive thinking only")
		}
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
	out.System = strings.Join(systems, "\n\n")
	bridge := "Continue the conversation provided as a JSON array in the user input. Respect each message's role. Return only the next assistant response. Tool results in that history were supplied by the external caller. Available functions are exposed through the cpa MCP server; use them when appropriate. Never execute local operations."
	if out.System != "" {
		out.System += "\n\n"
	}
	out.System += bridge
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
