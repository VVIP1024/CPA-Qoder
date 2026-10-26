package core

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func TestRequestPreservesHistoryAndExternalTools(t *testing.T) {
	req := pluginapi.ExecutorRequest{Model: "Qwen3.8-Flash", Payload: []byte(`{"messages":[{"role":"system","content":"Be concise"},{"role":"user","content":"Weather?"},{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"weather","arguments":"{\"city\":\"Beijing\"}"}}]},{"role":"tool","tool_call_id":"c1","content":"Sunny"},{"role":"user","content":"Explain"}],"tools":[{"type":"function","function":{"name":"weather","description":"Weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}],"max_tokens":42,"reasoning_effort":"low"}`)}
	got, err := prepareRequest(req, "cn")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.System, "Be concise") || !strings.Contains(got.Prompt, "Sunny") || !strings.Contains(got.Prompt, "tool_call_id") || !strings.Contains(got.Prompt, "Explain") {
		t.Fatalf("lost context: %+v", got)
	}
	if got.MaxTokens != 42 || got.Effort != "low" || len(got.Tools) != 1 || got.Tools[0].Name != "weather" {
		t.Fatalf("bad request: %+v", got)
	}
	var transcript []map[string]any
	if err := json.Unmarshal([]byte(got.Prompt), &transcript); err != nil {
		t.Fatal(err)
	}
	if len(transcript) != 4 || transcript[1]["role"] != "assistant" {
		t.Fatalf("history roles lost: %s", got.Prompt)
	}
}
func TestSystemPromptOverridesTheCLIAgentFraming(t *testing.T) {
	req := pluginapi.ExecutorRequest{Model: "Auto", Payload: []byte(`{"messages":[{"role":"system","content":"Be concise"},{"role":"user","content":"Hi"}]}`)}
	got, err := prepareRequest(req, "global")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got.System, plainModelRole) {
		t.Fatalf("system prompt does not start with the plain model role: %q", got.System)
	}
	if !strings.Contains(got.System, "Be concise") {
		t.Fatalf("caller system message lost: %q", got.System)
	}
}

func TestUnsupportedRequestsFailExplicitly(t *testing.T) {
	for _, raw := range []string{
		`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"x"}}]}]}`,
		`{"messages":[{"role":"user","content":"x"}],"temperature":"hot"}`,
		`{"messages":[{"role":"user","content":"x"}],"n":2}`,
		`{"messages":[{"role":"user","content":"x"}],"tools":[{"type":"web_search"}]}`,
		`{"messages":[{"role":"user","content":"x"}],"max_tokens":-1}`,
		`{"messages":[{"role":"user","content":"x"}],"tool_choice":"required"}`,
		`{"messages":[]}`,
		`{"messages":[{"role":"user","content":"x"}],"response_format":{"type":"json_schema"}}`,
		`{"messages":[{"role":"user","content":"x"}],"logprobs":true}`,
		`{"messages":[{"role":"user","content":"x"}],"seed":"x"}`,
		`{"messages":[{"role":"user","content":"x"}],"stop":5}`,
		`{"messages":[{"role":"user","content":"x"}],"reasoning":{"effort":"ultra"}}`,
	} {
		if _, err := prepareRequest(pluginapi.ExecutorRequest{Model: "Auto", Payload: []byte(raw)}, "global"); err == nil {
			t.Fatalf("silently accepted %s", raw)
		}
	}
}

func TestStandardParametersAreAccepted(t *testing.T) {
	req := pluginapi.ExecutorRequest{Model: "Auto", Payload: []byte(`{"messages":[{"role":"user","content":"x"}],"temperature":0.2,"top_p":0.9,"top_k":40,"min_p":0.05,"presence_penalty":0,"frequency_penalty":0,"seed":7,"logprobs":false,"top_logprobs":1,"logit_bias":{"1":-100},"service_tier":"auto","stop":["END","\n\n"],"response_format":{"type":"json_object"},"reasoning":{"effort":"high"}}`)}
	got, err := prepareRequest(req, "global")
	if err != nil {
		t.Fatal(err)
	}
	if got.Effort != "high" {
		t.Fatalf("effort = %q", got.Effort)
	}
	if len(got.Stop) != 2 || got.Stop[0] != "END" {
		t.Fatalf("stop = %v", got.Stop)
	}
	if !got.JSONMode {
		t.Fatal("json mode not requested")
	}
	if !strings.Contains(got.System, "single JSON object") {
		t.Fatalf("json mode hint missing: %q", got.System)
	}
}
func TestEffortLevelsMatchTheCLI(t *testing.T) {
	for _, tc := range []struct {
		payload string
		effort  string
	}{
		{`{"messages":[{"role":"user","content":"x"}],"reasoning_effort":"none"}`, "none"},
		{`{"messages":[{"role":"user","content":"x"}],"reasoning_effort":"minimal"}`, "low"},
		{`{"messages":[{"role":"user","content":"x"}],"reasoning":{"effort":"minimal"}}`, "low"},
		{`{"messages":[{"role":"user","content":"x"}],"reasoning_effort":"low"}`, "low"},
		{`{"messages":[{"role":"user","content":"x"}],"reasoning_effort":"medium"}`, "medium"},
		{`{"messages":[{"role":"user","content":"x"}],"reasoning_effort":"high"}`, "high"},
		{`{"messages":[{"role":"user","content":"x"}],"reasoning_effort":"xhigh"}`, "xhigh"},
		{`{"messages":[{"role":"user","content":"x"}],"reasoning_effort":"max"}`, "max"},
	} {
		got, err := prepareRequest(pluginapi.ExecutorRequest{Model: "Auto", Payload: []byte(tc.payload)}, "global")
		if err != nil || got.Effort != tc.effort {
			t.Fatalf("effort=%q want %q err=%v", got.Effort, tc.effort, err)
		}
	}
	for _, payload := range []string{
		`{"messages":[{"role":"user","content":"x"}],"reasoning_effort":"ultra"}`,
	} {
		if _, err := prepareRequest(pluginapi.ExecutorRequest{Model: "Auto", Payload: []byte(payload)}, "global"); err == nil {
			t.Fatalf("accepted unsupported effort: %s", payload)
		}
	}
}
func TestThinkingModesAndBudget(t *testing.T) {
	for _, tc := range []struct {
		payload string
		mode    string
		budget  int
	}{
		{`{"messages":[{"role":"user","content":"x"}],"thinking":{"type":"auto"}}`, "auto", 0},
		{`{"messages":[{"role":"user","content":"x"}],"thinking":{"type":"adaptive"}}`, "adaptive", 0},
		{`{"messages":[{"role":"user","content":"x"}],"thinking":{"type":"enabled","budget_tokens":4096}}`, "enabled", 4096},
		{`{"messages":[{"role":"user","content":"x"}],"thinking":{"type":"disabled"}}`, "disabled", 0},
	} {
		got, err := prepareRequest(pluginapi.ExecutorRequest{Model: "Auto", Payload: []byte(tc.payload)}, "global")
		if err != nil || got.Thinking != tc.mode || got.ThinkingBudget != tc.budget {
			t.Fatalf("got mode=%q budget=%d err=%v", got.Thinking, got.ThinkingBudget, err)
		}
	}
	for _, payload := range []string{
		`{"messages":[{"role":"user","content":"x"}],"reasoning_effort":"none","thinking":{"type":"enabled"}}`,
		`{"messages":[{"role":"user","content":"x"}],"reasoning_effort":"high","thinking":{"type":"adaptive"}}`,
		`{"messages":[{"role":"user","content":"x"}],"thinking":{"type":"disabled","budget_tokens":100}}`,
		`{"messages":[{"role":"user","content":"x"}],"thinking":{"type":"other"}}`,
	} {
		if _, err := prepareRequest(pluginapi.ExecutorRequest{Model: "Auto", Payload: []byte(payload)}, "global"); err == nil {
			t.Fatalf("accepted conflicting thinking config: %s", payload)
		}
	}
}
func TestEnableThinkingAlias(t *testing.T) {
	for _, tc := range []struct {
		payload string
		mode    string
		effort  string
		budget  int
	}{
		{`{"messages":[{"role":"user","content":"x"}],"enable_thinking":true}`, "enabled", "", defaultThinkingBudget},
		{`{"messages":[{"role":"user","content":"x"}],"enable_thinking":false}`, "disabled", "", 0},
		{`{"messages":[{"role":"user","content":"x"}],"enable_thinking":"true"}`, "enabled", "", defaultThinkingBudget},
		{`{"messages":[{"role":"user","content":"x"}],"enable_thinking":true,"thinking":{"type":"enabled"}}`, "enabled", "", defaultThinkingBudget},
		{`{"messages":[{"role":"user","content":"x"}],"enable_thinking":true,"reasoning_effort":"high"}`, "", "high", 0},
		{`{"messages":[{"role":"user","content":"x"}],"thinking":{"type":"enabled"}}`, "enabled", "", defaultThinkingBudget},
		{`{"messages":[{"role":"user","content":"x"}],"thinking":{"type":"enabled","budget_tokens":4096}}`, "enabled", "", 4096},
		{`{"messages":[{"role":"user","content":"x"}],"thinking":{"type":"adaptive"}}`, "adaptive", "", 0},
		{`{"messages":[{"role":"user","content":"x"}],"enable_thinking":true,"thinking":{"type":"auto"}}`, "auto", "", 0},
	} {
		got, err := prepareRequest(pluginapi.ExecutorRequest{Model: "Auto", Payload: []byte(tc.payload)}, "global")
		if err != nil || got.Thinking != tc.mode || got.Effort != tc.effort || got.ThinkingBudget != tc.budget {
			t.Fatalf("mode=%q effort=%q budget=%d err=%v for %s", got.Thinking, got.Effort, got.ThinkingBudget, err, tc.payload)
		}
	}
	for _, payload := range []string{
		`{"messages":[{"role":"user","content":"x"}],"enable_thinking":false,"reasoning_effort":"high"}`,
		`{"messages":[{"role":"user","content":"x"}],"enable_thinking":true,"reasoning_effort":"none"}`,
		`{"messages":[{"role":"user","content":"x"}],"enable_thinking":false,"thinking":{"type":"enabled"}}`,
		`{"messages":[{"role":"user","content":"x"}],"enable_thinking":"maybe"}`,
	} {
		if _, err := prepareRequest(pluginapi.ExecutorRequest{Model: "Auto", Payload: []byte(payload)}, "global"); err == nil {
			t.Fatalf("accepted conflicting enable_thinking config: %s", payload)
		}
	}
}
func TestChildEnvironmentDoesNotOverrideLoginOrRegion(t *testing.T) {
	for _, key := range []string{"QODER_PERSONAL_ACCESS_TOKEN", "QODERCN_PERSONAL_ACCESS_TOKEN", "QODER_CONFIG_DIR", "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "NODE_OPTIONS", "NODE_TLS_REJECT_UNAUTHORIZED"} {
		t.Setenv(key, "sensitive")
	}
	for _, entry := range childEnvironment("cn", "") {
		for _, key := range []string{"QODER_PERSONAL_ACCESS_TOKEN", "QODERCN_PERSONAL_ACCESS_TOKEN", "QODER_CONFIG_DIR", "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "NODE_OPTIONS", "NODE_TLS_REJECT_UNAUTHORIZED"} {
			if strings.HasPrefix(entry, key+"=") {
				t.Fatalf("inherited %s", key)
			}
		}
	}
	dir, cleanup, err := emptyWorkDir()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatal("working directory not empty", err)
	}
}

func TestRequestStripsAccountPrefix(t *testing.T) {
	got, err := prepareRequest(pluginapi.ExecutorRequest{Model: "qodercn/Auto", Payload: []byte(`{"messages":[{"role":"user","content":"x"}]}`)}, "cn")
	if err != nil {
		t.Fatal(err)
	}
	if got.Model != "Auto" {
		t.Fatalf("model = %q", got.Model)
	}
}
