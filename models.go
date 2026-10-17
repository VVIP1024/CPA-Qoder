package main

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

func modelPrefix(variant string) string {
	if variant == "cn" {
		return "qoder-cn/"
	}
	return "qoder/"
}
func nativeModel(variant, model string) (string, error) {
	prefix := modelPrefix(variant)
	if variant != "global" && variant != "cn" || !strings.HasPrefix(model, prefix) {
		return "", badRequest("model namespace does not match the selected Qoder account")
	}
	name := strings.TrimPrefix(model, prefix)
	if name == "" || strings.HasPrefix(name, "-") || strings.ContainsAny(name, "\x00\r\n/") {
		return "", badRequest("invalid Qoder model name")
	}
	return name, nil
}
func (p *Provider) ModelsForAuth(ctx context.Context, raw []byte) (pluginapi.ModelResponse, error) {
	ctx, untrack, err := p.track(ctx, "")
	if err != nil {
		return pluginapi.ModelResponse{}, err
	}
	defer untrack()
	d, err := readDescriptor(raw)
	if err != nil {
		return pluginapi.ModelResponse{}, err
	}
	a, err := p.account(d.Variant)
	if err != nil {
		return pluginapi.ModelResponse{}, err
	}
	names, err := p.modelNames(ctx, d.Variant, a)
	if err != nil {
		return pluginapi.ModelResponse{}, err
	}
	out := pluginapi.ModelResponse{Provider: "qoder"}
	for _, name := range names {
		out.Models = append(out.Models, pluginapi.ModelInfo{ID: modelPrefix(d.Variant) + name, Name: name, Object: "model", OwnedBy: "qoder", Type: "qoder", DisplayName: "Qoder " + d.Variant + " / " + name, SupportedGenerationMethods: []string{"chat"}, SupportedInputModalities: []string{"text"}, SupportedOutputModalities: []string{"text"}, SupportedParameters: []string{"tools", "tool_choice", "max_tokens", "reasoning_effort"}})
	}
	return out, nil
}
func (p *Provider) modelNames(ctx context.Context, variant string, a AccountConfig) ([]string, error) {
	p.mu.Lock()
	entry, ok := p.catalogs[variant]
	p.mu.Unlock()
	if ok && time.Now().Before(entry.expires) {
		return append([]string(nil), entry.names...), nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	work, cleanup, err := emptyWorkDir()
	if err != nil {
		return nil, err
	}
	defer cleanup()
	cmd := exec.CommandContext(ctx, a.CLI, "--config-dir", a.ConfigDir, "--cwd", work, "--setting-sources", "", "--settings", `{"disableAllHooks":true}`, "--list-models")
	cmd.Dir = work
	cmd.Env = childEnvironment(variant, p.cfg.ProxyURL)
	configureProcess(cmd)
	raw, err := cmd.Output()
	if err != nil {
		return nil, pluginabi.NewError("model_discovery_failed", fmt.Sprintf("Qoder %s model discovery failed; check CLI login and connectivity", variant), 502)
	}
	names := []string{}
	seen := map[string]bool{}
	scanner := bufio.NewScanner(strings.NewReader(ansiEscape.ReplaceAllString(string(raw), "")))
	header := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "MODEL" {
			header = true
			continue
		}
		if !header || line == "" {
			continue
		}
		if _, err := nativeModel(variant, modelPrefix(variant)+line); err != nil {
			return nil, err
		}
		if !seen[line] {
			names = append(names, line)
			seen[line] = true
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, pluginabi.NewError("no_models", "Qoder CLI returned no available models", 503)
	}
	p.mu.Lock()
	p.catalogs[variant] = catalogEntry{names: names, expires: time.Now().Add(5 * time.Minute)}
	p.mu.Unlock()
	return append([]string(nil), names...), nil
}
