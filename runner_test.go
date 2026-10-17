package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func fixtureCLI() {
	args := os.Args[1:]
	get := func(k string) string {
		for i, v := range args {
			if v == k && i+1 < len(args) {
				return args[i+1]
			}
		}
		return ""
	}
	cfg := get("--config-dir")
	input, _ := io.ReadAll(os.Stdin)
	report, _ := json.Marshal(map[string]any{"args": args, "stdin": string(input), "cwd": get("--cwd"), "env": os.Environ()})
	_ = os.WriteFile(filepath.Join(cfg, "invocation.json"), report, 0600)
	if _, err := os.Stat(filepath.Join(cfg, "block")); err == nil {
		waitFixtureSignal()
	}
	if _, err := os.Stat(filepath.Join(cfg, "tool")); err == nil {
		_, _ = io.WriteString(os.Stdout, `{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"call1","name":"mcp__cpa__weather","input":{}}}}`+"\n"+`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"city\":\"Beijing\"}"}}}`+"\n"+`{"type":"stream_event","event":{"type":"message_delta","delta":{"stop_reason":"tool_use"}}}`+"\n")
		waitFixtureSignal()
	}
	_, _ = io.WriteString(os.Stdout, `{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"OK"}}}`+"\n"+`{"type":"result","is_error":false,"subtype":"success","result":"OK","stop_reason":"end_turn","usage":{"input_tokens":4,"output_tokens":1}}`+"\n")
}
func runRequest() pluginapi.ExecutorRequest {
	return pluginapi.ExecutorRequest{Model: "qoder/Auto", StorageJSON: []byte(`{"type":"qoder","variant":"global"}`), Payload: []byte(`{"messages":[{"role":"user","content":"Hello"}]}`)}
}
func TestRunnerUsesExplicitStdinAndIsolatedWorkspace(t *testing.T) {
	p := testProvider(t)
	req := runRequest()
	var text string
	out, err := p.Run(context.Background(), req, func(d Delta) error { text += d.Text; return nil })
	if err != nil || out.Text != "OK" || text != "OK" {
		t.Fatalf("out %+v stream %q err %v", out, text, err)
	}
	raw, err := os.ReadFile(filepath.Join(p.cfg.Global.ConfigDir, "invocation.json"))
	if err != nil {
		t.Fatal(err)
	}
	var report struct {
		Args       []string
		Stdin, CWD string
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report.Stdin, "Hello") {
		t.Fatal("prompt lost")
	}
	for _, arg := range report.Args {
		if strings.Contains(arg, "Hello") {
			t.Fatal("prompt exposed in process arguments")
		}
	}
	if _, err := os.Stat(report.CWD); !os.IsNotExist(err) {
		t.Fatal("workspace not cleaned")
	}
	for _, flag := range []string{"--no-session-persistence", "--strict-mcp-config", "--tools", "--setting-sources"} {
		found := false
		for _, arg := range report.Args {
			if arg == flag {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing isolation flag %s", flag)
		}
	}
}
func TestRunnerReturnsNativeToolCallAndStopsChild(t *testing.T) {
	p := testProvider(t)
	exe, _ := os.Executable()
	p.cfg.Helper = exe
	if err := os.WriteFile(filepath.Join(p.cfg.Global.ConfigDir, "tool"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	req := runRequest()
	req.Payload = []byte(`{"messages":[{"role":"user","content":"Weather"}],"tools":[{"type":"function","function":{"name":"weather","parameters":{"type":"object"}}}]}`)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := p.Run(ctx, req, func(Delta) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if out.FinishReason != "tool_calls" || len(out.Tools) != 1 || out.Tools[0].Function.Name != "weather" {
		t.Fatalf("tool result: %+v", out)
	}
}
func TestRunnerCancelsSilentChild(t *testing.T) {
	p := testProvider(t)
	if err := os.WriteFile(filepath.Join(p.cfg.Global.ConfigDir, "block"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := p.Run(ctx, runRequest(), func(Delta) error { return nil }); done <- err }()
	// Wait on a concrete child-start side effect, never on an assumed startup delay.
	deadline := time.After(5 * time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(filepath.Join(p.cfg.Global.ConfigDir, "invocation.json")); err == nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("child never started")
		case <-ticker.C:
		}
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancellation returned success")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("silent child leaked")
	}
}

func waitFixtureSignal() { c := make(chan os.Signal, 1); signal.Notify(c, os.Interrupt); <-c }

func TestRequestCancellationIncludesModelDiscovery(t *testing.T) {
	p := testProvider(t)
	_ = os.WriteFile(filepath.Join(p.cfg.Global.ConfigDir, "model-block"), nil, 0600)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := runRequest()
	req.Headers = map[string][]string{requestIDHeader: {"discovery-request"}}
	done := make(chan error, 1)
	go func() { _, err := p.Run(ctx, req, func(Delta) error { return nil }); done <- err }()
	deadline := time.After(5 * time.Second)
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(filepath.Join(p.cfg.Global.ConfigDir, "discovery.started")); err == nil {
			break
		}
		select {
		case <-deadline:
			t.Fatal("discovery not started")
		case <-ticker.C:
		}
	}
	p.Cancel("discovery-request")
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled discovery succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("request cancellation did not cover model discovery")
	}
}

func TestConfiguredProxyReachesDiscoveryAndInference(t *testing.T) {
	p := testProvider(t)
	// An explicit plugin proxy must override inherited transport variables.
	t.Setenv("HTTPS_PROXY", "http://inherited.invalid:8080")
	p.cfg.ProxyURL = "http://proxy.invalid:3128"
	_, err := p.Run(context.Background(), runRequest(), func(Delta) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"discovery.env", "invocation.json"} {
		raw, err := os.ReadFile(filepath.Join(p.cfg.Global.ConfigDir, file))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), "HTTPS_PROXY=http://proxy.invalid:3128") || strings.Contains(string(raw), "inherited.invalid") {
			t.Fatalf("proxy not propagated to %s", file)
		}
	}
}
