package main

import (
	"strings"
	"testing"
)

func TestEventsDoNotDuplicateSnapshots(t *testing.T) {
	var text string
	p := newEventParser(nil, func(d Delta) error { text += d.Text; return nil })
	for _, line := range []string{
		`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Hello"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"Hello","usage":{"input_tokens":12,"output_tokens":3},"stop_reason":"end_turn"}`,
	} {
		if err := p.Feed([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	out, err := p.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if out.Text != "Hello" || text != "Hello" || out.Usage.PromptTokens != 12 || out.Usage.CompletionTokens != 3 {
		t.Fatalf("duplicate or wrong usage: %+v streamed=%q", out, text)
	}
}
func TestNativeToolCallIsReturnedWithoutExecution(t *testing.T) {
	p := newEventParser(map[string]string{"mcp__cpa__weather": "weather"}, func(Delta) error { return nil })
	for _, line := range []string{
		`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call1","name":"mcp__cpa__weather","input":{}}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"city\":\"Beijing\"}"}}}`,
		`{"type":"stream_event","event":{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":7}}}`,
	} {
		if err := p.Feed([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	if !p.Handoff() {
		t.Fatal("tool handoff not detected")
	}
	out, err := p.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Tools) != 1 || out.Tools[0].Function.Name != "weather" || out.Tools[0].Function.Arguments != `{"city":"Beijing"}` || out.FinishReason != "tool_calls" {
		t.Fatalf("bad tool output: %+v", out)
	}
}
func TestEventsRejectTruncatedOrFailedGeneration(t *testing.T) {
	for _, lines := range [][]string{
		{`{"type":"assistant","message":{"content":[{"type":"text","text":"partial"}]}}`},
		{`{"type":"result","is_error":true,"result":"quota exceeded"}`},
		{`not JSON`},
		{`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"c","name":"unexpected"}}}`},
	} {
		p := newEventParser(nil, func(Delta) error { return nil })
		var feedErr error
		for _, s := range lines {
			if feedErr = p.Feed([]byte(s)); feedErr != nil {
				break
			}
		}
		_, err := p.Finish()
		if feedErr == nil && err == nil {
			t.Fatalf("accepted bad output: %s", strings.Join(lines, "\n"))
		}
	}
}

func TestEmptyToolArgumentsAreValidInStream(t *testing.T) {
	var args string
	p := newEventParser(map[string]string{"mcp__cpa__ping": "ping"}, func(d Delta) error {
		if d.Tool != nil {
			args += d.Tool.Function.Arguments
		}
		args += d.Arguments
		return nil
	})
	for _, line := range []string{
		`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call1","name":"mcp__cpa__ping","input":{}}}}`,
		`{"type":"stream_event","event":{"type":"message_delta","delta":{"stop_reason":"tool_use"}}}`,
	} {
		if err := p.Feed([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	out, err := p.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if args != "{}" || args != out.Tools[0].Function.Arguments {
		t.Fatalf("streamed arguments %q, completed %+v", args, out.Tools)
	}
}
