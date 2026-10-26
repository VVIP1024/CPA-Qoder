package core

import (
	"bytes"
	"encoding/json"
	"strings"
)

// stopFilter cuts the model output at the stop sequences the caller asked
// for. The CLI cannot stop on its own, so the bridge does it.
type stopFilter struct {
	stops []string
	held  string
	done  bool
}

// push returns the text that may be published and whether a stop sequence
// ended the output.
func (f *stopFilter) push(text string) (string, bool) {
	if f.done {
		return "", true
	}
	if len(f.stops) == 0 {
		return text, false
	}
	buffer := f.held + text
	cut := -1
	for _, stop := range f.stops {
		if stop == "" {
			continue
		}
		if index := strings.Index(buffer, stop); index >= 0 && (cut < 0 || index < cut) {
			cut = index
		}
	}
	if cut >= 0 {
		f.done = true
		f.held = ""
		return buffer[:cut], true
	}
	// The tail may still start a stop sequence, so it is held back.
	keep := 0
	for _, stop := range f.stops {
		if len(stop) > keep {
			keep = len(stop)
		}
	}
	if keep > 1 && len(buffer) > keep-1 {
		f.held = buffer[len(buffer)-(keep-1):]
		return buffer[:len(buffer)-(keep-1)], false
	}
	f.held = buffer
	return "", false
}

// flush returns the held tail once the output ended without a stop sequence.
func (f *stopFilter) flush() string {
	held := f.held
	f.held = ""
	return held
}

// dropUnsupportedParameters removes the request fields the transport does not
// understand. The model behind the CLI still answers them, so a client may
// send the standard OpenAI parameter set without the request being rejected.
func dropUnsupportedParameters(body map[string]json.RawMessage, supported map[string]bool) {
	for key := range body {
		if !supported[key] {
			delete(body, key)
		}
	}
}

// stopSequences reads the OpenAI stop field, which is a string or a list of
// strings.
func stopSequences(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return []string{text}, nil
	}
	var list []string
	if json.Unmarshal(raw, &list) != nil {
		return nil, badRequest("stop must be a string or a list of strings")
	}
	return list, nil
}

// reasoningEffortValue reads the OpenAI reasoning field, which is either an
// object carrying an effort or a bare effort string.
func reasoningEffortValue(raw json.RawMessage) (string, error) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, nil
	}
	var object struct {
		Effort string `json:"effort"`
	}
	if json.Unmarshal(raw, &object) != nil {
		return "", badRequest("reasoning must be a string or an object")
	}
	return object.Effort, nil
}

// mergeEffort combines the reasoning_effort and reasoning fields, which name
// the same setting in two shapes.
func mergeEffort(body map[string]json.RawMessage) (string, error) {
	effort := ""
	if raw := body["reasoning_effort"]; len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
		if json.Unmarshal(raw, &effort) != nil {
			return "", badRequest("reasoning_effort must be a string")
		}
	}
	if raw := body["reasoning"]; len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
		value, err := reasoningEffortValue(raw)
		if err != nil {
			return "", err
		}
		if value != "" {
			if effort != "" && effort != value {
				return "", badRequest("reasoning and reasoning_effort disagree")
			}
			effort = value
		}
	}
	return effort, nil
}

// thinkingMode reads the DeepSeek-style thinking configuration, which names
// the reasoning mode and its budget.
func thinkingMode(name string, raw json.RawMessage) (string, int, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", 0, nil
	}
	var thinking struct {
		Type         string `json:"type"`
		BudgetTokens int    `json:"budget_tokens"`
	}
	if json.Unmarshal(raw, &thinking) != nil {
		return "", 0, badRequest("invalid thinking configuration")
	}
	switch thinking.Type {
	case "", "auto", "adaptive", "enabled", "disabled":
	default:
		return "", 0, badRequest("unsupported " + name + " thinking mode")
	}
	if thinking.BudgetTokens < 0 {
		return "", 0, badRequest("thinking budget must not be negative")
	}
	return thinking.Type, thinking.BudgetTokens, nil
}

// checkOptionalParameters validates the OpenAI parameters the transport
// cannot honour. They are accepted and dropped instead of failing the
// request, because the model behind the CLI still answers them. The sampling
// keys named by the caller are the ones this transport drops as well.
func checkOptionalParameters(name string, body map[string]json.RawMessage, sampling ...string) error {
	for _, key := range sampling {
		if raw := body[key]; len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
			var value float64
			if json.Unmarshal(raw, &value) != nil {
				return badRequest(key + " must be a number")
			}
		}
	}
	if raw := body["logprobs"]; len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
		var enabled bool
		if json.Unmarshal(raw, &enabled) != nil {
			return badRequest("logprobs must be a boolean")
		}
		if enabled {
			return badRequest(name + " cannot return token log probabilities")
		}
	}
	if raw := body["top_logprobs"]; len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
		var count int
		if json.Unmarshal(raw, &count) != nil || count < 0 {
			return badRequest("top_logprobs must be a non-negative integer")
		}
	}
	if raw := body["seed"]; len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
		var seed int
		if json.Unmarshal(raw, &seed) != nil {
			return badRequest("seed must be an integer")
		}
	}
	if raw := body["logit_bias"]; len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
		var bias map[string]float64
		if json.Unmarshal(raw, &bias) != nil {
			return badRequest("logit_bias must be an object")
		}
	}
	if raw := body["service_tier"]; len(raw) > 0 && !bytes.Equal(raw, []byte("null")) {
		var tier string
		if json.Unmarshal(raw, &tier) != nil {
			return badRequest("service_tier must be a string")
		}
	}
	return nil
}

// jsonMode reports whether the caller asked for a JSON object response.
func jsonMode(name string, raw json.RawMessage) (bool, error) {
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return false, nil
	}
	var format struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &format) != nil {
		return false, badRequest("invalid response_format")
	}
	switch format.Type {
	case "", "text":
		return false, nil
	case "json_object":
		return true, nil
	default:
		return false, badRequest(name + " cannot enforce a JSON schema")
	}
}
