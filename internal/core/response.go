package core

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
)

func newCompletionID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return "chatcmpl-qoder-" + hex.EncodeToString(b[:])
}
func completionJSON(model, id string, created int64, out Completion) []byte {
	message := map[string]any{"role": "assistant", "content": out.Text}
	if len(out.Tools) > 0 {
		message["tool_calls"] = out.Tools
		if out.Text == "" {
			message["content"] = nil
		}
	}
	if out.Reasoning != "" {
		message["reasoning_content"] = out.Reasoning
	}
	usage := map[string]any{"prompt_tokens": out.Usage.PromptTokens, "completion_tokens": out.Usage.CompletionTokens, "total_tokens": out.Usage.TotalTokens}
	if out.Usage.CachedTokens > 0 {
		usage["prompt_tokens_details"] = map[string]int{"cached_tokens": out.Usage.CachedTokens}
	}
	raw, _ := json.Marshal(map[string]any{"id": id, "object": "chat.completion", "created": created, "model": model, "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": out.FinishReason}}, "usage": usage})
	return raw
}
func streamFrame(model, id string, created int64, delta map[string]any, finish any, usage *Usage) []byte {
	body := map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
	if usage != nil {
		body["usage"] = usage
	}
	raw, _ := json.Marshal(body)
	return raw
}
func streamDelta(d Delta) map[string]any {
	out := map[string]any{}
	if d.Text != "" {
		out["content"] = d.Text
	}
	if d.Reasoning != "" {
		out["reasoning_content"] = d.Reasoning
	}
	if d.Tool != nil {
		out["tool_calls"] = []any{map[string]any{"index": d.ToolIndex, "id": d.Tool.ID, "type": "function", "function": map[string]any{"name": d.Tool.Function.Name, "arguments": d.Tool.Function.Arguments}}}
	} else if d.Arguments != "" {
		out["tool_calls"] = []any{map[string]any{"index": d.ToolIndex, "function": map[string]any{"arguments": d.Arguments}}}
	}
	return out
}
