package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef struct {
	void* ptr;
	size_t len;
} cliproxy_buffer;

typedef int (*cliproxy_host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_host_free_fn)(void*, size_t);

typedef struct {
	uint32_t abi_version;
	void* host_ctx;
	cliproxy_host_call_fn call;
	cliproxy_host_free_fn free_buffer;
} cliproxy_host_api;

typedef int (*cliproxy_plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);

typedef struct {
	uint32_t abi_version;
	cliproxy_plugin_call_fn call;
	cliproxy_plugin_free_fn free_buffer;
	cliproxy_plugin_shutdown_fn shutdown;
} cliproxy_plugin_api;

extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);

static const cliproxy_host_api* stored_host;

static void store_host_api(const cliproxy_host_api* host) {
	stored_host = host;
}

static int call_host_api(const char* method, const uint8_t* request, size_t request_len, cliproxy_buffer* response) {
	if (stored_host == NULL || stored_host->call == NULL) {
		return 1;
	}
	return stored_host->call(stored_host->host_ctx, method, request, request_len, response);
}

static void free_host_buffer(void* ptr, size_t len) {
	if (stored_host != NULL && stored_host->free_buffer != NULL && ptr != NULL) {
		stored_host->free_buffer(ptr, len);
	}
}
*/
import "C"

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"unsafe"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
)

func main() {
	if len(os.Args) == 3 && os.Args[1] == "--mcp-schema" {
		if err := serveMCP(os.Args[2], os.Stdin, os.Stdout); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, "CPA-Qoder tool schema helper failed")
			os.Exit(1)
		}
		return
	}
	_, _ = fmt.Fprintln(os.Stderr, "CPA-Qoder is a native CPA plugin; this executable only serves --mcp-schema")
}

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if plugin == nil || host == nil || uint32(host.abi_version) != pluginabi.ABIVersion {
		return 1
	}
	C.store_host_api(host)
	plugin.abi_version = C.uint32_t(pluginabi.ABIVersion)
	plugin.call = C.cliproxy_plugin_call_fn(C.cliproxyPluginCall)
	plugin.free_buffer = C.cliproxy_plugin_free_fn(C.cliproxyPluginFree)
	plugin.shutdown = C.cliproxy_plugin_shutdown_fn(C.cliproxyPluginShutdown)
	return 0
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, requestLen C.size_t, response *C.cliproxy_buffer) (status C.int) {
	if response == nil {
		return 1
	}
	response.ptr = nil
	response.len = 0
	defer func() {
		if recover() != nil {
			raw, _ := pluginabi.NewErrorEnvelope("plugin_panic", "CPA-Qoder request failed", 500)
			writeResponse(response, raw)
			status = 1
		}
	}()
	if method == nil || requestLen > 32<<20 {
		raw, _ := pluginabi.NewErrorEnvelope("invalid_request", "invalid plugin request", 400)
		writeResponse(response, raw)
		return 1
	}
	var body []byte
	if request != nil && requestLen > 0 {
		body = C.GoBytes(unsafe.Pointer(request), C.int(requestLen))
	}
	result, err := dispatch(C.GoString(method), body)
	if err != nil {
		code := 500
		var sc interface{ StatusCode() int }
		if errors.As(err, &sc) && sc.StatusCode() > 0 {
			code = sc.StatusCode()
		}
		raw, _ := pluginabi.NewErrorEnvelope("qoder_error", err.Error(), code)
		writeResponse(response, raw)
		return 1
	}
	raw, err := json.Marshal(pluginabi.Envelope{OK: true, Result: result})
	if err != nil {
		return 1
	}
	writeResponse(response, raw)
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(ptr unsafe.Pointer, _ C.size_t) {
	if ptr != nil {
		C.free(ptr)
	}
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() {
	if p := current.Load(); p != nil {
		p.Close()
	}
}

func writeResponse(response *C.cliproxy_buffer, raw []byte) {
	if response == nil || len(raw) == 0 {
		return
	}
	response.ptr = C.CBytes(raw)
	response.len = C.size_t(len(raw))
}

var callHost = nativeCallHost

func nativeCallHost(method string, payload any) (json.RawMessage, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	cMethod := C.CString(method)
	defer C.free(unsafe.Pointer(cMethod))
	ptr := C.CBytes(raw)
	defer C.free(ptr)
	var response C.cliproxy_buffer
	rc := C.call_host_api(cMethod, (*C.uint8_t)(ptr), C.size_t(len(raw)), &response)
	if response.ptr == nil {
		return nil, fmt.Errorf("CPA host callback unavailable")
	}
	defer C.free_host_buffer(response.ptr, response.len)
	if response.len > 32<<20 {
		return nil, fmt.Errorf("CPA host callback response too large")
	}
	data := C.GoBytes(response.ptr, C.int(response.len))
	var envelope pluginabi.Envelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return nil, err
	}
	if !envelope.OK || rc != 0 {
		if envelope.Error != nil {
			return nil, envelope.Error
		}
		return nil, fmt.Errorf("CPA host callback failed")
	}
	return envelope.Result, nil
}
