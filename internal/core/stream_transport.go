package core

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator"
	_ "github.com/router-for-me/CLIProxyAPI/v8/sdk/translator/builtin"
)

const transportFormat = "cpa-qoder-chat"
const transportField = "_cpa_qoder_stream"

// Each stream has at most one chunk awaiting consumption. The response
// translator runs only after CPA takes that chunk from its executor queue.
// Waiting here, rather than inside a native callback, makes unload cancelable.
var streamDeliveries sync.Map

type streamReceipt struct {
	sequence uint64
	done     chan struct{}
	once     sync.Once
}

type streamDelivery struct {
	id               string
	sequence         uint64 // send is serialized by the stream's emit mutex
	pending          atomic.Pointer[streamReceipt]
	translateMu      sync.Mutex
	translationState any
}

type streamStamp struct {
	ID       string `json:"id"`
	Sequence uint64 `json:"sequence"`
	Final    bool   `json:"final,omitempty"`
}

func openStreamDelivery(id string) (*streamDelivery, error) {
	d := &streamDelivery{id: id}
	if _, exists := streamDeliveries.LoadOrStore(id, d); exists {
		return nil, badRequest("duplicate stream_id")
	}
	return d, nil
}

func (d *streamDelivery) send(ctx context.Context, payload []byte, final bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var frame map[string]json.RawMessage
	if err := json.Unmarshal(payload, &frame); err != nil {
		return err
	}
	d.sequence++
	receipt := &streamReceipt{sequence: d.sequence, done: make(chan struct{})}
	d.pending.Store(receipt)
	stamp, _ := json.Marshal(streamStamp{ID: d.id, Sequence: d.sequence, Final: final})
	frame[transportField] = stamp
	body, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	if _, err = callHost("host.stream.emit", map[string]any{"stream_id": d.id, "payload": body}); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-receipt.done:
		return nil
	}
}

func translateResponse(req pluginapi.ResponseTransformRequest) (pluginapi.PayloadResponse, error) {
	if req.FromFormat != transportFormat {
		return pluginapi.PayloadResponse{}, nil
	}
	target := sdktranslator.FromString(req.ToFormat)
	if req.ToFormat == "chat-completions" {
		target = sdktranslator.FormatOpenAI
	}
	if !req.Stream {
		body := req.Body
		if target != sdktranslator.FormatOpenAI {
			if !sdktranslator.HasNonStreamResponseTransformer(target, sdktranslator.FormatOpenAI) {
				return pluginapi.PayloadResponse{}, badRequest("unsupported response format")
			}
			var state any
			body = sdktranslator.TranslateNonStream(context.Background(), sdktranslator.FormatOpenAI, target, req.Model, req.OriginalRequest, req.TranslatedRequest, body, &state)
		}
		return pluginapi.PayloadResponse{Body: body}, nil
	}
	var frame map[string]json.RawMessage
	if err := json.Unmarshal(req.Body, &frame); err != nil {
		return pluginapi.PayloadResponse{}, badRequest("invalid stream frame")
	}
	var stamp streamStamp
	if err := json.Unmarshal(frame[transportField], &stamp); err != nil {
		return pluginapi.PayloadResponse{}, badRequest("missing stream receipt")
	}
	raw, ok := streamDeliveries.Load(stamp.ID)
	if !ok {
		return pluginapi.PayloadResponse{}, badRequest("stream is closed")
	}
	d := raw.(*streamDelivery)
	d.translateMu.Lock()
	defer d.translateMu.Unlock()
	receipt := d.pending.Load()
	if receipt == nil || receipt.sequence != stamp.Sequence {
		return pluginapi.PayloadResponse{}, badRequest("stale stream receipt")
	}
	defer receipt.once.Do(func() { close(receipt.done) })
	delete(frame, transportField)
	body, err := json.Marshal(frame)
	if err != nil {
		return pluginapi.PayloadResponse{}, err
	}
	if target != sdktranslator.FormatOpenAI {
		if !sdktranslator.HasStreamResponseTransformer(target, sdktranslator.FormatOpenAI) {
			return pluginapi.PayloadResponse{}, badRequest("unsupported stream response format")
		}
		input := append(append([]byte("data: "), body...), []byte("\n\n")...)
		frames := sdktranslator.TranslateStream(context.Background(), sdktranslator.FormatOpenAI, target, req.Model, req.OriginalRequest, req.TranslatedRequest, input, &d.translationState)
		if stamp.Final {
			frames = append(frames, sdktranslator.TranslateStream(context.Background(), sdktranslator.FormatOpenAI, target, req.Model, req.OriginalRequest, req.TranslatedRequest, []byte("data: [DONE]\n\n"), &d.translationState)...)
		}
		body = bytes.Join(frames, nil)
		// An empty translation is legitimate (e.g. a role-only frame). A
		// comment prevents the host's raw-payload fallback from exposing it.
		if len(body) == 0 {
			body = []byte(":\n\n")
		}
	}
	return pluginapi.PayloadResponse{Body: body}, nil
}
