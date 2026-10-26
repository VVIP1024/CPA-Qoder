package core

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
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
func TestEventsStopAtTheCallersStopSequence(t *testing.T) {
	var text string
	p := newEventParser(nil, func(d Delta) error { text += d.Text; return nil })
	p.stop.stops = []string{"STOP"}
	for _, line := range []string{
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" STOP world"}}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"","stop_reason":"end_turn"}`,
	} {
		if err := p.Feed([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	out, err := p.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if out.Text != "Hello " || text != "Hello " {
		t.Fatalf("stop sequence not applied: %+v streamed=%q", out, text)
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
func TestUnregisteredToolNameIsSkippedAndRetried(t *testing.T) {
	p := newEventParser(map[string]string{"mcp__cpa__Read": "Read", "Read": "Read"}, func(Delta) error { return nil })
	for _, line := range []string{
		`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"bad","name":"Write"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"file_path\":\"/x\"}"}}}`,
		`{"type":"stream_event","event":{"type":"message_delta","delta":{"stop_reason":"tool_use"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"good","name":"Read"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"file_path\":\"/etc/hostname\"}"}}}`,
		`{"type":"stream_event","event":{"type":"message_delta","delta":{"stop_reason":"tool_use"}}}`,
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
	if len(out.Tools) != 1 || out.Tools[0].Function.Name != "Read" || out.Tools[0].Function.Arguments != `{"file_path":"/etc/hostname"}` {
		t.Fatalf("unregistered tool leaked or short name was dropped: %+v", out.Tools)
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

// The CLI cuts its last record when its stdout is closed early. The records
// before it are intact, so the content is returned instead of discarded.
func TestEventsKeepContentWhenTheTailRecordIsTruncated(t *testing.T) {
	p := newEventParser(nil, func(Delta) error { return nil })
	for _, line := range []string{
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}}`,
		`{"type":"result","subtype":"success","is_error":false,"result":"Hel`,
	} {
		if err := p.Feed([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	out, err := p.Finish()
	if err != nil {
		t.Fatalf("truncated tail rejected the content: %v", err)
	}
	if out.Text != "Hello" || out.FinishReason != "stop" {
		t.Fatalf("unexpected completion: %+v", out)
	}
}

// A malformed record in the middle means content was lost, not just a cut
// tail, so the generation is still rejected.
func TestEventsRejectMalformedMiddleRecord(t *testing.T) {
	p := newEventParser(nil, func(Delta) error { return nil })
	for _, line := range []string{
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}}`,
		`{"type":"stream_event","eve`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}}`,
	} {
		if err := p.Feed([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.Finish(); err == nil {
		t.Fatal("accepted a generation with a lost record")
	}
}

func TestCLIFatalErrorIsReportedWithItsMessage(t *testing.T) {
	err := cliFailure("An unexpected critical error occurred:Error: --thinking enabled requires --thinking-budget <tokens>.\n    at Fn (/$bunfs/root/chunk.js:5:891)")
	if err == nil || !strings.Contains(err.Error(), "requires --thinking-budget") {
		t.Fatalf("unexpected error: %v", err)
	}
	if cliFailure("429 rate limit") == nil || cliFailure("Error: 401 unauthorized") == nil {
		t.Fatal("expected failures")
	}
	if detail := cliArgumentError("An unexpected critical error occurred:Error: --thinking enabled requires --thinking-budget <tokens>.\n    at Fn"); detail != "--thinking enabled requires --thinking-budget <tokens>." {
		t.Fatalf("detail = %q", detail)
	}
	if detail := cliArgumentError("ordinary failure"); detail != "" {
		t.Fatalf("detail = %q", detail)
	}
}

func TestQueuedModelIsReportedAsRetryable(t *testing.T) {
	for _, message := range []string{
		"Queuing failed. Try again later or enter /model to switch models.",
		`Qoder API error: FORBIDDEN - {"code":"403","message":"{\"code\":\"10605\",\"message\":\"{\\\"isQueued\\\":true,\\\"modelKey\\\":\\\"gmodel\\\",\\\"subCode\\\":\\\"RESOURCE_LIMIT\\\"}\"}","subCode":"RESOURCE_LIMIT"}`,
	} {
		err := cliFailure(message)
		var apiErr *pluginabi.Error
		if !errors.As(err, &apiErr) {
			t.Fatalf("error %v is not a plugin error", err)
		}
		if apiErr.Code != "model_queued" || apiErr.HTTPStatus != 429 {
			t.Fatalf("queue failure mapped to %+v", apiErr)
		}
	}
	// A plain quota failure keeps its own meaning.
	err := cliFailure("403 quota exhausted")
	var apiErr *pluginabi.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "insufficient_quota" {
		t.Fatalf("quota failure mapped to %+v", err)
	}
}

func TestOversizedInputIsARequestError(t *testing.T) {
	for _, message := range []string{
		"The input is too long for this model. Please shorten it or start a new conversation.",
		"Input content too long, please simplify and retry",
		"Qoder API error: PAYLOAD_TOO_LARGE - {\"code\":\"provider_error\",\"details\":\"InternalError.Algo.InvalidParameter: Range of input length should be [1, 983616]\"}",
		"QoderCN inference request failed: 413 Request Entity Too Large",
	} {
		err := cliFailure(message)
		var apiErr *pluginabi.Error
		if !errors.As(err, &apiErr) {
			t.Fatalf("error %v is not a plugin error", err)
		}
		if apiErr.Code != "context_length_exceeded" || apiErr.HTTPStatus != 400 {
			t.Fatalf("oversized input mapped to %+v", apiErr)
		}
		if !strings.Contains(apiErr.Message, "context length") {
			t.Fatalf("message %q hides the cause", apiErr.Message)
		}
	}
}

func TestResultErrorsListCarriesTheFailure(t *testing.T) {
	// The CN build reports an oversized input only in the errors list.
	p := newEventParser(nil, func(Delta) error { return nil })
	err := p.Feed([]byte(`{"type":"result","subtype":"error_during_execution","is_error":true,"errors":["Input content too long, please simplify and retry"],"error_code":80411,"usage":{"input_tokens":0,"output_tokens":0}}`))
	var apiErr *pluginabi.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "context_length_exceeded" || apiErr.HTTPStatus != 400 {
		t.Fatalf("errors-list failure mapped to %+v", err)
	}
}

func TestQueueStatusEventsAreTracked(t *testing.T) {
	p := newEventParser(nil, func(Delta) error { return nil })
	if p.Queued() {
		t.Fatal("model reported as queued before any event")
	}
	if err := p.Feed([]byte(`{"type":"system","subtype":"model_queue_status","status":"queued","queue_wait_elapsed_ms":90000,"service_available":false}`)); err != nil {
		t.Fatal(err)
	}
	if !p.Queued() {
		t.Fatal("queued model not detected")
	}
	if p.QueueWait() < 90*time.Second {
		t.Fatalf("queue wait = %v", p.QueueWait())
	}
	select {
	case <-p.QueuedCh():
	default:
		t.Fatal("queue notification not published")
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
