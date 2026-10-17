package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func TestRequestPreservesHistoryAndExternalTools(t *testing.T) {
	req := pluginapi.ExecutorRequest{Model: "qoder-cn/Qwen3.8-Flash", Payload: []byte(`{"messages":[{"role":"system","content":"Be concise"},{"role":"user","content":"Weather?"},{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"weather","arguments":"{\"city\":\"Beijing\"}"}}]},{"role":"tool","tool_call_id":"c1","content":"Sunny"},{"role":"user","content":"Explain"}],"tools":[{"type":"function","function":{"name":"weather","description":"Weather","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}],"max_tokens":42,"reasoning_effort":"low"}`)}
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
func TestUnsupportedRequestsFailExplicitly(t *testing.T) {
	for _, raw := range []string{
		`{"messages":[{"role":"user","content":[{"type":"image_url","image_url":{"url":"x"}}]}]}`,
		`{"messages":[{"role":"user","content":"x"}],"temperature":0.1}`,
		`{"messages":[{"role":"user","content":"x"}],"n":2}`,
		`{"messages":[{"role":"user","content":"x"}],"tools":[{"type":"web_search"}]}`,
		`{"messages":[{"role":"user","content":"x"}],"max_tokens":-1}`,
		`{"messages":[{"role":"user","content":"x"}],"tool_choice":"required"}`,
		`{"messages":[]}`,
		`{"messages":[{"role":"user","content":"x"}],"response_format":{"type":"json_schema"}}`,
	} {
		if _, err := prepareRequest(pluginapi.ExecutorRequest{Model: "qoder/Auto", Payload: []byte(raw)}, "global"); err == nil {
			t.Fatalf("silently accepted %s", raw)
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
