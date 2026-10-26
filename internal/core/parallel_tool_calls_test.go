package core

import (
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func TestParallelToolCallsControlsToolUse(t *testing.T) {
	for _, tc := range []struct {
		payload string
		single  bool
	}{
		{`{"messages":[{"role":"user","content":"x"}]}`, false},
		{`{"messages":[{"role":"user","content":"x"}],"parallel_tool_calls":true}`, false},
		{`{"messages":[{"role":"user","content":"x"}],"parallel_tool_calls":false}`, true},
	} {
		got, err := prepareRequest(pluginapi.ExecutorRequest{Model: "Auto", Payload: []byte(tc.payload)}, "global")
		if err != nil || got.SingleTool != tc.single {
			t.Fatalf("single=%v err=%v for %s", got.SingleTool, err, tc.payload)
		}
	}
	if _, err := prepareRequest(pluginapi.ExecutorRequest{Model: "Auto", Payload: []byte(`{"messages":[{"role":"user","content":"x"}],"parallel_tool_calls":"maybe"}`)}, "global"); err == nil {
		t.Fatal("accepted a non boolean parallel_tool_calls")
	}
}

func TestSerialToolCallsReturnOnlyTheFirstCall(t *testing.T) {
	lines := []string{
		`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"c1","name":"mcp__cpa__clock","input":{}}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{}"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"c2","name":"mcp__cpa__weather","input":{}}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"city\":\"Beijing\"}"}}}`,
		`{"type":"stream_event","event":{"type":"message_delta","delta":{"stop_reason":"tool_use"}}}`,
	}
	for _, tc := range []struct {
		single bool
		want   int
	}{{false, 2}, {true, 1}} {
		p := newEventParser(map[string]string{"mcp__cpa__clock": "clock", "mcp__cpa__weather": "weather"}, func(Delta) error { return nil })
		p.single = tc.single
		for _, line := range lines {
			if err := p.Feed([]byte(line)); err != nil {
				t.Fatal(err)
			}
		}
		out, err := p.Finish()
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Tools) != tc.want {
			t.Fatalf("single=%v returned %d tool calls: %+v", tc.single, len(out.Tools), out.Tools)
		}
	}
}
