package main

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
)

// serveMCP advertises caller-owned functions. It never implements a function.
// A tools/call request is deliberately left pending until the CLI is stopped at
// the native tool-use boundary; the external CPA client executes the function.
func serveMCP(schemaPath string, input io.Reader, output io.Writer) error {
	raw, err := os.ReadFile(schemaPath)
	if err != nil {
		return err
	}
	var tools []MCPTool
	if err := json.Unmarshal(raw, &tools); err != nil {
		return err
	}
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 8<<20)
	encoder := json.NewEncoder(output)
	for scanner.Scan() {
		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
		}
		if json.Unmarshal(scanner.Bytes(), &req) != nil || len(req.ID) == 0 {
			continue
		}
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "cpa-qoder", "version": "0.1.0"}}
		case "tools/list":
			result = map[string]any{"tools": tools}
		case "tools/call":
			continue
		case "ping":
			result = map[string]any{}
		default:
			if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32601, "message": "method not found"}}); err != nil {
				return err
			}
			continue
		}
		if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result}); err != nil {
			return err
		}
	}
	return scanner.Err()
}
