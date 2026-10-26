package core

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	"gopkg.in/yaml.v3"
)

var current atomic.Pointer[Provider]
var logins = struct {
	sync.Mutex
	states map[string]loginState
}{states: map[string]loginState{}}

type loginState struct {
	account descriptor
	expires time.Time
}

func provider() *Provider {
	if p := current.Load(); p != nil {
		return p
	}
	p, err := NewProvider(Config{})
	if err != nil {
		return nil
	}
	if !current.CompareAndSwap(nil, p) {
		p.Close()
	}
	return current.Load()
}
func marshal(v any) ([]byte, error) { return json.Marshal(v) }
func decode(raw []byte, v any) error {
	if len(raw) == 0 {
		raw = []byte(`{}`)
	}
	if json.Unmarshal(raw, v) != nil {
		return badRequest("invalid plugin request JSON")
	}
	return nil
}
func dispatch(method string, raw []byte) ([]byte, error) {
	if method == "plugin.register" || method == "plugin.reconfigure" {
		var req struct {
			ConfigYAML []byte `json:"config_yaml"`
		}
		if err := decode(raw, &req); err != nil {
			return nil, err
		}
		var cfg Config
		if err := yaml.Unmarshal(req.ConfigYAML, &cfg); err != nil {
			return nil, badRequest("invalid CPA-Qoder configuration")
		}
		p, err := NewProvider(cfg)
		if err != nil {
			return nil, err
		}
		if old := current.Swap(p); old != nil {
			old.Close()
		}
		return marshal(map[string]any{"schema_version": pluginabi.SchemaVersion, "metadata": pluginapi.Metadata{Name: "Qoder", Version: "0.1.0", Author: "CPA-Qoder contributors", GitHubRepository: "https://github.com/VVIP1024/CPA-Qoder", ConfigFields: []pluginapi.ConfigField{{Name: "global", Type: pluginapi.ConfigFieldTypeObject, Description: "International CLI, config-dir and disabled settings"}, {Name: "cn", Type: pluginapi.ConfigFieldTypeObject, Description: "China CLI, config-dir and disabled settings"}, {Name: "proxy-url", Type: pluginapi.ConfigFieldTypeString, Description: "Explicit proxy for both CLI discovery and generation; otherwise inherit CLI environment"}, {Name: "queue-wait", Type: pluginapi.ConfigFieldTypeString, Description: "How long a request waits for a model the upstream put in a queue before failing with a retryable error; 0 waits forever"}, {Name: "helper", Type: pluginapi.ConfigFieldTypeString, Description: "Absolute path to cpa-qoder-helper for external function calling"}}}, "capabilities": map[string]any{"auth_provider": true, "model_provider": true, "executor": true, "executor_model_scope": "oauth", "executor_input_formats": []string{"chat-completions"}, "executor_output_formats": []string{transportFormat}, "response_translator": true, "request_interceptor": true, "request_lifecycle_plugin": true}})
	}
	p := provider()
	if p == nil {
		return nil, fmt.Errorf("cannot initialize CPA-Qoder")
	}
	switch method {
	case "plugin.quiesce", "plugin.shutdown":
		p.Close()
		return marshal(map[string]any{})
	case "auth.identifier", "executor.identifier":
		return marshal(map[string]string{"identifier": "qoder"})
	case "auth.parse":
		var req pluginapi.AuthParseRequest
		if err := decode(raw, &req); err != nil {
			return nil, err
		}
		out, err := p.ParseAuth(req)
		if err != nil {
			return nil, err
		}
		return marshal(out)
	case "model.static":
		return marshal(pluginapi.ModelResponse{Provider: "qoder"})
	case "model.for_auth":
		var req pluginapi.AuthModelRequest
		if err := decode(raw, &req); err != nil {
			return nil, err
		}
		out, err := p.ModelsForAuth(context.Background(), req.StorageJSON)
		if err != nil {
			return nil, err
		}
		return marshal(out)
	case "auth.refresh":
		var req pluginapi.AuthRefreshRequest
		if err := decode(raw, &req); err != nil {
			return nil, err
		}
		d, err := readDescriptor(req.StorageJSON)
		if err != nil {
			return nil, err
		}
		a, err := p.withAuthState(authData(d), d)
		if err != nil {
			return nil, err
		}
		a.ID = req.AuthID
		return marshal(pluginapi.AuthRefreshResponse{Auth: a, NextRefreshAfter: a.NextRefreshAfter})
	case "auth.login.start":
		var req pluginapi.AuthLoginStartRequest
		if err := decode(raw, &req); err != nil {
			return nil, err
		}
		account := descriptor{Type: "qoder"}
		account.Variant, _ = req.Metadata["variant"].(string)
		account.AccountID, _ = req.Metadata["account_id"].(string)
		if account.Variant == "" {
			account.Variant = "global"
		}
		if _, err := p.resolveAccount(account); err != nil {
			return nil, err
		}
		var b [24]byte
		if _, err := rand.Read(b[:]); err != nil {
			return nil, err
		}
		state := hex.EncodeToString(b[:])
		expires := time.Now().Add(5 * time.Minute)
		logins.Lock()
		for k, v := range logins.states {
			if time.Now().After(v.expires) {
				delete(logins.states, k)
			}
		}
		logins.states[state] = loginState{account, expires}
		logins.Unlock()
		return marshal(pluginapi.AuthLoginStartResponse{Provider: "qoder", State: state, ExpiresAt: expires, Metadata: map[string]any{"variant": account.Variant, "account_id": account.AccountID, "login_command": p.loginCommand(account)}})
	case "auth.login.poll":
		var req pluginapi.AuthLoginPollRequest
		if err := decode(raw, &req); err != nil {
			return nil, err
		}
		logins.Lock()
		state, ok := logins.states[req.State]
		if ok && time.Now().After(state.expires) {
			delete(logins.states, req.State)
			ok = false
		}
		logins.Unlock()
		if !ok {
			return nil, badRequest("Qoder import state is invalid or expired")
		}
		loggedIn, err := p.hasLogin(state.account)
		if err != nil {
			return nil, err
		}
		if !loggedIn {
			return marshal(pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusPending, Message: "Waiting for Qoder CLI login; run " + p.loginCommand(state.account)})
		}
		logins.Lock()
		delete(logins.states, req.State)
		logins.Unlock()
		a, err := p.withAuthState(authData(state.account), state.account)
		if err != nil {
			return nil, err
		}
		return marshal(pluginapi.AuthLoginPollResponse{Status: pluginapi.AuthLoginStatusSuccess, Message: "Existing CLI login imported", Auth: a})
	case pluginabi.MethodRequestInterceptBefore, pluginabi.MethodRequestInterceptAfter:
		var req pluginapi.RequestInterceptRequest
		if err := decode(raw, &req); err != nil {
			return nil, err
		}
		h := http.Header{}
		if p.ownsModel(req.Model) {
			h.Set(requestIDHeader, req.RequestID)
		}
		return marshal(pluginapi.RequestInterceptResponse{Headers: h})
	case "request.complete":
		var req pluginapi.RequestCompletion
		if err := decode(raw, &req); err != nil {
			return nil, err
		}
		p.Cancel(req.RequestID)
		return marshal(map[string]any{})
	case "executor.execute":
		var req pluginapi.ExecutorRequest
		if err := decode(raw, &req); err != nil {
			return nil, err
		}
		id := newCompletionID()
		created := time.Now().Unix()
		out, err := p.Run(context.Background(), req, func(Delta) error { return nil })
		if err != nil {
			return nil, err
		}
		return marshal(pluginapi.ExecutorResponse{Payload: completionJSON(req.Model, id, created, out), Headers: http.Header{"Content-Type": []string{"application/json"}}})
	case "response.translate":
		var req pluginapi.ResponseTransformRequest
		if err := decode(raw, &req); err != nil {
			return nil, err
		}
		out, err := translateResponse(req)
		if err != nil {
			return nil, err
		}
		return marshal(out)
	case "executor.execute_stream":
		return startStream(p, raw)
	case "executor.count_tokens":
		return nil, pluginabi.NewError("unsupported_operation", "Qoder CLI does not expose exact token counting", 501)
	case "executor.http_request":
		return nil, pluginabi.NewError("unsupported_operation", "Qoder CLI credentials cannot be used for arbitrary HTTP requests", 501)
	default:
		return nil, pluginabi.NewError("unknown_method", "unsupported CPA-Qoder method: "+method, 400)
	}
}
func startStream(p *Provider, raw []byte) ([]byte, error) {
	var req struct {
		pluginapi.ExecutorRequest
		StreamID string `json:"stream_id"`
	}
	if err := decode(raw, &req); err != nil {
		return nil, err
	}
	if req.StreamID == "" {
		return nil, badRequest("stream_id is required")
	}
	d, err := readDescriptor(req.StorageJSON)
	if err != nil {
		return nil, err
	}
	if _, err := prepareRequest(req.ExecutorRequest, d.Variant); err != nil {
		return nil, err
	}
	if _, err := p.account(d); err != nil {
		return nil, err
	}
	streamCtx, untrack, err := p.track(context.Background(), req.Headers.Get(requestIDHeader))
	if err != nil {
		return nil, err
	}
	delivery, err := openStreamDelivery(req.StreamID)
	if err != nil {
		untrack()
		return nil, err
	}
	go func() {
		defer untrack()
		defer streamDeliveries.Delete(req.StreamID)
		ctx, cancel := context.WithCancel(streamCtx)
		defer cancel()
		// The emitter gets its own context so a stalled downstream consumer
		// can be abandoned without tearing down the CLI reader.
		emitCtx, emitCancel := context.WithCancel(ctx)
		defer emitCancel()
		var errMu sync.Mutex
		errMessage := ""
		fail := func(message string) {
			errMu.Lock()
			if errMessage == "" {
				errMessage = message
			}
			errMu.Unlock()
		}
		var closeOnce sync.Once
		closeStream := func(message string) {
			closeOnce.Do(func() {
				_, _ = callHost("host.stream.close", map[string]any{"stream_id": req.StreamID, "error": message})
			})
		}
		done := make(chan struct{})
		heartbeatDone := make(chan struct{})
		watcherDone := make(chan struct{})
		go func() {
			defer close(watcherDone)
			select {
			case <-ctx.Done():
				errMu.Lock()
				message := errMessage
				errMu.Unlock()
				if message == "" {
					if ctx.Err() == context.DeadlineExceeded {
						message = "Qoder request timed out"
					} else {
						message = "Qoder request canceled"
					}
				}
				closeStream(message)
			case <-done:
			}
		}()
		queue := newFrameQueue()
		emitted := make(chan struct{})
		var emitMu sync.Mutex
		terminal := false
		emitChunk := func(payload []byte, final bool) error {
			emitMu.Lock()
			defer emitMu.Unlock()
			if terminal {
				return nil
			}
			terminal = final
			queue.push(payload, final)
			return nil
		}
		emit := func(payload []byte) error { return emitChunk(payload, false) }
		// The CLI reader must never wait on the downstream round trip: the
		// Qoder CLI abandons a generation whose stdout consumer stalls.
		go func() {
			defer close(emitted)
			for {
				frame, ok := queue.next()
				if !ok {
					return
				}
				if err := delivery.send(emitCtx, frame.payload, frame.final); err != nil {
					if emitCtx.Err() == nil {
						fail(err.Error())
					}
					cancel()
					return
				}
			}
		}()
		defer func() {
			if recovered := recover(); recovered != nil {
				fail("CPA-Qoder stream failed")
			}
			close(done)
			queue.close()
			select {
			case <-emitted:
			case <-ctx.Done():
			case <-time.After(streamDrainTimeout):
			}
			emitCancel()
			<-emitted
			errMu.Lock()
			message := errMessage
			errMu.Unlock()
			closeStream(message)
			cancel()
			<-heartbeatDone
			<-watcherDone
		}()
		id := newCompletionID()
		created := time.Now().Unix()
		// Empty deltas detect disconnected clients during upstream silence.
		// They share the same consumption window as content.
		go func() {
			defer close(heartbeatDone)
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-done:
					return
				case <-ctx.Done():
					return
				case <-ticker.C:
					_ = emit(streamFrame(req.Model, id, created, map[string]any{}, nil, nil))
				}
			}
		}()
		if err := emit(streamFrame(req.Model, id, created, map[string]any{"role": "assistant"}, nil, nil)); err != nil {
			return
		}
		out, err := p.Run(ctx, req.ExecutorRequest, func(d Delta) error {
			delta := streamDelta(d)
			if len(delta) == 0 {
				return nil
			}
			return emit(streamFrame(req.Model, id, created, delta, nil, nil))
		})
		if err != nil {
			fail(err.Error())
			return
		}
		if err := emitChunk(streamFrame(req.Model, id, created, map[string]any{}, out.FinishReason, &out.Usage), true); err != nil {
			return
		}
	}()
	return marshal(map[string]any{"headers": http.Header{"Content-Type": []string{"text/event-stream"}}})
}
