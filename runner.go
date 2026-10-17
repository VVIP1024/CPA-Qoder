package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

const requestIDHeader = "X-Cpa-Qoder-Request-Id"

func (p *Provider) Run(ctx context.Context, req pluginapi.ExecutorRequest, emit func(Delta) error) (Completion, error) {
	ctx, untrack, err := p.track(ctx, req.Headers.Get(requestIDHeader))
	if err != nil {
		return Completion{}, err
	}
	defer untrack()

	d, err := readDescriptor(req.StorageJSON)
	if err != nil {
		return Completion{}, err
	}
	a, err := p.account(d.Variant)
	if err != nil {
		return Completion{}, err
	}
	prepared, err := prepareRequest(req, d.Variant)
	if err != nil {
		return Completion{}, err
	}
	names, err := p.modelNames(ctx, d.Variant, a)
	if err != nil {
		return Completion{}, err
	}
	if !slices.Contains(names, prepared.Model) {
		return Completion{}, pluginabi.NewError("model_not_found", "model is not available to the selected Qoder account", 404)
	}
	work, cleanup, err := emptyWorkDir()
	if err != nil {
		return Completion{}, err
	}
	defer cleanup()
	args := []string{"--config-dir", a.ConfigDir, "--cwd", work, "--setting-sources", "", "--settings", `{"disableAllHooks":true}`, "--strict-mcp-config", "--tools", "", "--permission-mode", "dont_ask", "--no-session-persistence", "--include-partial-messages", "--output-format", "stream-json", "--max-model-request-retries", "0", "--model", prepared.Model, "--system-prompt", prepared.System}
	mcp := map[string]any{"mcpServers": map[string]any{}}
	toolNames := map[string]string{}
	if len(prepared.Tools) > 0 {
		helper := p.cfg.Helper
		if helper == "" {
			helper, err = exec.LookPath("cpa-qoder-helper")
			if err != nil {
				return Completion{}, pluginabi.NewError("helper_missing", "set plugins.configs.cpa-qoder.helper to the built cpa-qoder-helper executable", 503)
			}
		}
		helper, err = filepath.Abs(helper)
		if err != nil {
			return Completion{}, err
		}
		info, err := os.Stat(helper)
		if err != nil || info.IsDir() {
			return Completion{}, pluginabi.NewError("helper_missing", "configured cpa-qoder-helper executable is missing", 503)
		}
		schemaPath := filepath.Join(work, "tools.json")
		schema, _ := json.Marshal(prepared.Tools)
		if err := os.WriteFile(schemaPath, schema, 0600); err != nil {
			return Completion{}, err
		}
		mcp = map[string]any{"mcpServers": map[string]any{"cpa": map[string]any{"command": helper, "args": []string{"--mcp-schema", schemaPath}}}}
		for _, tool := range prepared.Tools {
			toolNames["mcp__cpa__"+tool.Name] = tool.Name
		}
	}
	mcpRaw, _ := json.Marshal(mcp)
	args = append(args, "--mcp-config", string(mcpRaw))
	if prepared.MaxTokens > 0 {
		args = append(args, "--max-output-tokens", jsonNumber(prepared.MaxTokens))
	}
	if prepared.Effort != "" {
		if prepared.Effort == "none" {
			args = append(args, "--thinking", "disabled")
		} else {
			args = append(args, "--reasoning-effort", prepared.Effort)
		}
	}
	if prepared.Thinking != "" {
		args = append(args, "--thinking", prepared.Thinking)
	}
	args = append(args, "--print")
	processCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(processCtx, a.CLI, args...)
	cmd.Dir = work
	cmd.Stdin = strings.NewReader(prepared.Prompt)
	cmd.Env = childEnvironment(d.Variant, p.cfg.ProxyURL)
	configureProcess(cmd)
	stderr := &limitedBuffer{limit: 8192}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Completion{}, err
	}
	if err := cmd.Start(); err != nil {
		return Completion{}, pluginabi.NewError("cli_unavailable", "Qoder CLI could not be started; check the configured executable", 503)
	}
	parser := newEventParser(toolNames, emit)
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 8<<20)
	var parseErr error
	for scanner.Scan() {
		if len(strings.TrimSpace(scanner.Text())) == 0 {
			continue
		}
		if parseErr = parser.Feed(scanner.Bytes()); parseErr != nil {
			cancel()
			break
		}
		if parser.Handoff() {
			cancel()
			break
		}
	}
	if errScan := scanner.Err(); parseErr == nil && errScan != nil {
		parseErr = pluginabi.NewError("invalid_cli_output", "Qoder stream record is too large or unreadable", 502)
		cancel()
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return Completion{}, ctx.Err()
	}
	if parseErr != nil {
		return Completion{}, parseErr
	}
	if waitErr != nil && !parser.Handoff() {
		return Completion{}, cliFailure(stderr.String())
	}
	return parser.Finish()
}
func jsonNumber(n int) string { b, _ := json.Marshal(n); return string(b) }

type limitedBuffer struct {
	mu    sync.Mutex
	b     []byte
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	remaining := b.limit - len(b.b)
	if remaining > 0 {
		b.b = append(b.b, p[:min(len(p), remaining)]...)
	}
	return n, nil
}
func (b *limitedBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return string(b.b) }

var _ io.Writer = (*limitedBuffer)(nil)
