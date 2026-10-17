package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

// Model the original host: sixteen queued chunks, synchronous emits, and every
// callback rejected after native shutdown starts. No stream-close exception.
func TestStreamWaitsForConsumptionAndUnloadsWithClosedHost(t *testing.T) {
	p := testProvider(t)
	original := callHost
	queue := make(chan struct{}, 16)
	var stopped atomic.Bool
	var callbacks atomic.Int32
	var sent atomic.Int32
	first := make(chan struct{})
	release := make(chan struct{})
	callHost = func(method string, _ any) (json.RawMessage, error) {
		callbacks.Add(1)
		defer callbacks.Add(-1)
		if stopped.Load() {
			return nil, fmt.Errorf("host plugin callback instance is closed")
		}
		if method == "host.stream.close" {
			return nil, nil
		}
		if sent.Add(1) == 1 {
			close(first)
		}
		select {
		case queue <- struct{}{}:
		case <-release:
			return nil, fmt.Errorf("test cleanup")
		}
		return nil, nil
	}
	defer func() { close(release); p.Close(); callHost = original }()
	req := runRequest()
	raw, _ := json.Marshal(struct {
		pluginapi.ExecutorRequest
		StreamID string `json:"stream_id"`
	}{req, "unread-stream"})
	if _, err := startStream(p, raw); err != nil {
		t.Fatal(err)
	}
	select {
	case <-first:
	case <-time.After(time.Second):
		t.Fatal("stream never started")
	}
	// Old heartbeat/content output accumulated chunks without a consumer.
	time.Sleep(1200 * time.Millisecond)
	if n := sent.Load(); n != 1 {
		t.Errorf("unconsumed stream sent %d chunks, want one", n)
	}
	stopped.Store(true)
	done := make(chan struct{})
	go func() { p.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("unload blocked after host callbacks closed")
	}
	if callbacks.Load() != 0 {
		t.Fatal("native callback outlived provider")
	}
	if _, _, err := p.track(context.Background(), "late"); err == nil {
		t.Fatal("closed provider accepted work")
	}
}

func TestStreamReceiptsResumeOutputAndKeepWirePayloadClean(t *testing.T) {
	d, err := openStreamDelivery("many-chunks")
	if err != nil {
		t.Fatal(err)
	}
	defer streamDeliveries.Delete(d.id)
	original := callHost
	var count int
	callHost = func(method string, payload any) (json.RawMessage, error) {
		if method != "host.stream.emit" {
			t.Fatalf("unexpected callback %s", method)
		}
		body := payload.(map[string]any)["payload"].([]byte)
		out, err := translateResponse(pluginapi.ResponseTransformRequest{FromFormat: transportFormat, ToFormat: "openai", Stream: true, Body: body})
		if err != nil {
			return nil, err
		}
		var frame map[string]json.RawMessage
		if json.Unmarshal(out.Body, &frame) != nil || frame[transportField] != nil || frame["choices"] == nil {
			t.Error("invalid client frame")
		}
		count++
		return nil, nil
	}
	defer func() { callHost = original }()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for i := 0; i < 128; i++ {
		if err := d.send(ctx, streamFrame("test", "test-id", 1, map[string]any{"content": "x"}, nil, nil), false); err != nil {
			t.Fatal(err)
		}
	}
	if count != 128 {
		t.Fatal(count)
	}
}

func TestResponseTranslatorIgnoresOtherProtocols(t *testing.T) {
	out, err := translateResponse(pluginapi.ResponseTransformRequest{FromFormat: "other-provider", ToFormat: "openai", Body: []byte(`{"value":1}`)})
	if err != nil || len(out.Body) != 0 {
		t.Fatal("translator claimed another provider")
	}
}

func TestStreamTranslationRetainsStateAcrossFrames(t *testing.T) {
	d, err := openStreamDelivery("responses-stream")
	if err != nil {
		t.Fatal(err)
	}
	defer streamDeliveries.Delete(d.id)
	original := callHost
	var result []byte
	callHost = func(_ string, payload any) (json.RawMessage, error) {
		body := payload.(map[string]any)["payload"].([]byte)
		out, err := translateResponse(pluginapi.ResponseTransformRequest{FromFormat: transportFormat, ToFormat: "openai-response", Model: "test", Stream: true, Body: body})
		result = append(result, out.Body...)
		return nil, err
	}
	defer func() { callHost = original }()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for _, item := range []struct {
		delta  map[string]any
		finish any
		final  bool
	}{
		{map[string]any{"role": "assistant"}, nil, false},
		{map[string]any{"content": "OK"}, nil, false},
		{map[string]any{}, "stop", true},
	} {
		if err := d.send(ctx, streamFrame("test", "test-id", 1, item.delta, item.finish, nil), item.final); err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Contains(result, []byte("response.completed")) || !bytes.Contains(result, []byte("OK")) || bytes.Contains(result, []byte(transportField)) {
		t.Fatalf("invalid Responses stream: %s", result)
	}
}
