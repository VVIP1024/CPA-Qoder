package core

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func TestDispatchRegistersAndParsesActualWireSchema(t *testing.T) {
	raw, err := dispatch("plugin.register", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var reg struct {
		SchemaVersion int            `json:"schema_version"`
		Capabilities  map[string]any `json:"capabilities"`
		Metadata      struct {
			Name string `json:"name"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(raw, &reg); err != nil {
		t.Fatal(err)
	}
	if reg.SchemaVersion != 6 || reg.Metadata.Name != "Qoder" || reg.Capabilities["auth_provider"] != true || reg.Capabilities["executor"] != true {
		t.Fatalf("bad registration: %s", raw)
	}
	data, _ := json.Marshal(map[string]any{"RawJSON": []byte(`{"type":"qoder","variant":"cn"}`)})
	raw, err = dispatch("auth.parse", data)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Handled bool
		Auth    struct {
			Provider   string
			Attributes map[string]string
		}
	}
	if json.Unmarshal(raw, &out) != nil || !out.Handled || out.Auth.Provider != "qoder" || out.Auth.Attributes["variant"] != "cn" {
		t.Fatalf("wire auth: %s", raw)
	}
}
func TestRequestCompletionCancelsActiveGeneration(t *testing.T) {
	p := testProvider(t)
	ctx, done, err := p.track(context.Background(), "req1")
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	old := current.Swap(p)
	defer current.Store(old)
	if _, err := dispatch("request.complete", []byte(`{"RequestID":"req1","Outcome":"canceled"}`)); err != nil {
		t.Fatal(err)
	}
	p.catalogs["cn"] = catalogEntry{names: []string{"Auto"}}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("generation not canceled")
	}
	raw, err := dispatch("request.intercept_after", []byte(`{"RequestID":"req2","Model":"Auto"}`))
	if err != nil {
		t.Fatal(err)
	}
	var out struct{ Headers map[string][]string }
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.Headers[requestIDHeader][0] != "req2" {
		t.Fatalf("correlation missing: %s", raw)
	}
}
func TestMCPAdvertisesSchemaAndNeverExecutesCalls(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tools.json")
	_ = os.WriteFile(path, []byte(`[{"name":"weather","description":"Weather","inputSchema":{"type":"object"}}]`), 0600)
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize"}` + "\n" + `{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n" + `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"weather"}}` + "\n"
	assertMCP(t, path, input)
}

func assertMCP(t *testing.T, path, input string) {
	t.Helper()
	var out bytes.Buffer
	if err := serveMCP(path, strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || !strings.Contains(out.String(), "weather") {
		t.Fatalf("MCP unexpectedly executed a tool: %s", out.String())
	}
}

func TestStreamChunksAreJSONForCPAFraming(t *testing.T) {
	raw := streamFrame("Auto", "id", 1, map[string]any{"content": "OK"}, nil, nil)
	if !json.Valid(raw) {
		t.Fatalf("CPA expects an unframed JSON chunk: %s", raw)
	}
}
func TestCloseWaitsForCanceledWorkers(t *testing.T) {
	p := testProvider(t)
	ctx, release, err := p.track(context.Background(), "closing")
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan struct{})
	go func() { p.Close(); close(closed) }()
	<-ctx.Done()
	select {
	case <-closed:
		t.Fatal("Close returned with an active worker")
	case <-time.After(30 * time.Millisecond):
	}
	release()
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not drain workers")
	}
}

func TestFinishChunkRemainsLastWithSlowHost(t *testing.T) {
	p := testProvider(t)
	original := callHost
	var sawFinish atomic.Bool
	closed := make(chan struct{})
	callHost = func(method string, payload any) (json.RawMessage, error) {
		if method == "host.stream.close" {
			close(closed)
			return nil, nil
		}
		if method == "host.stream.emit" {
			if sawFinish.Load() {
				t.Error("heartbeat emitted after finish chunk")
			}
			raw := payload.(map[string]any)["payload"].([]byte)
			if bytes.Contains(raw, []byte(`"finish_reason":"stop"`)) {
				sawFinish.Store(true)
				<-time.After(1100 * time.Millisecond)
			}
			_, err := translateResponse(pluginapi.ResponseTransformRequest{FromFormat: transportFormat, ToFormat: "openai", Stream: true, Body: raw})
			return nil, err
		}
		return nil, nil
	}
	defer func() { p.Close(); callHost = original }()
	req := runRequest()
	raw, _ := json.Marshal(struct {
		pluginapi.ExecutorRequest
		StreamID string `json:"stream_id"`
	}{req, "slow"})
	if _, err := startStream(p, raw); err != nil {
		t.Fatal(err)
	}
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("stream not closed")
	}
}

func TestAuthLifecycleWithoutLogin(t *testing.T) {
	root := t.TempDir()
	p, err := NewProvider(Config{
		Global: AccountConfig{ConfigDir: filepath.Join(root, "global")},
		CN:     AccountConfig{ConfigDir: filepath.Join(root, "cn")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if old := current.Swap(p); old != nil {
		defer old.Close()
	}
	defer current.Store(nil)
	defer p.Close()

	raw, err := dispatch("auth.login.start", []byte(`{"Metadata":{"variant":"global"}}`))
	if err != nil {
		t.Fatal(err)
	}
	var started pluginapi.AuthLoginStartResponse
	if err := json.Unmarshal(raw, &started); err != nil {
		t.Fatal(err)
	}
	poll, _ := json.Marshal(pluginapi.AuthLoginPollRequest{State: started.State})
	raw, err = dispatch("auth.login.poll", poll)
	if err != nil {
		t.Fatal(err)
	}
	var pending pluginapi.AuthLoginPollResponse
	if err := json.Unmarshal(raw, &pending); err != nil {
		t.Fatal(err)
	}
	if pending.Status != pluginapi.AuthLoginStatusPending {
		t.Fatalf("poll status = %s, want pending", pending.Status)
	}

	authDir := filepath.Join(p.cfg.Global.ConfigDir, ".auth")
	if err := os.MkdirAll(authDir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"user", "machine_id"} {
		if err := os.WriteFile(filepath.Join(authDir, name), []byte("opaque"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	raw, err = dispatch("auth.login.poll", poll)
	if err != nil {
		t.Fatal(err)
	}
	var success pluginapi.AuthLoginPollResponse
	if err := json.Unmarshal(raw, &success); err != nil {
		t.Fatal(err)
	}
	if success.Status != pluginapi.AuthLoginStatusSuccess || success.Auth.Metadata["authenticated"] != true {
		t.Fatalf("poll result = %+v", success)
	}
}
