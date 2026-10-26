package core

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

func qoderModelPrefix(variant string) (string, string) {
	if variant == "cn" {
		return "qodercn/", "QoderCN"
	}
	return "qoder/", "Qoder"
}

func nativeModel(variant, model string) (string, error) {
	prefix, _ := qoderModelPrefix(variant)
	if variant != "global" && variant != "cn" {
		return "", badRequest("unknown Qoder account variant")
	}
	switch {
	case strings.HasPrefix(model, prefix):
		model = strings.TrimPrefix(model, prefix)
	case strings.HasPrefix(model, "qoder/") || strings.HasPrefix(model, "qodercn/"):
		return "", badRequest("Qoder model prefix does not match the selected account")
	}
	if model == "" || strings.HasPrefix(model, "-") || strings.ContainsAny(model, "\x00\r\n/") {
		return "", badRequest("invalid Qoder model name")
	}
	return model, nil
}
func (p *Provider) ownsModel(name string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	name = strings.TrimPrefix(name, "qodercn/")
	name = strings.TrimPrefix(name, "qoder/")
	for _, catalog := range p.catalogs {
		for _, model := range catalog.names {
			if model == name {
				return true
			}
		}
	}
	return false
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
	a, err := p.resolveAccount(d)
	if err != nil {
		return pluginapi.ModelResponse{}, err
	}
	loggedIn, err := p.hasLogin(d)
	if err != nil {
		return pluginapi.ModelResponse{}, err
	}
	if !loggedIn {
		return pluginapi.ModelResponse{Provider: "qoder"}, nil
	}
	names, err := p.modelNames(ctx, d.Variant, descriptorKey(d), a)
	if err != nil {
		return pluginapi.ModelResponse{}, err
	}
	prefix, label := qoderModelPrefix(d.Variant)
	out := pluginapi.ModelResponse{Provider: "qoder"}
	for _, name := range names {
		out.Models = append(out.Models, pluginapi.ModelInfo{ID: prefix + name, Name: name, Object: "model", OwnedBy: "qoder", Type: "qoder", DisplayName: label + "/" + name, SupportedGenerationMethods: []string{"chat"}, SupportedInputModalities: []string{"text"}, SupportedOutputModalities: []string{"text"}, SupportedParameters: []string{"tools", "tool_choice", "max_tokens", "reasoning_effort"}})
	}
	return out, nil
}
func (p *Provider) modelNames(ctx context.Context, variant, key string, a AccountConfig) ([]string, error) {
	p.mu.Lock()
	entry, ok := p.catalogs[key]
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
		return nil, pluginabi.NewError("model_discovery_failed", fmt.Sprintf("Qoder model discovery failed for %s; check CLI login and connectivity", key), 502)
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
		if _, err := nativeModel("global", line); err != nil {
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
	p.catalogs[key] = catalogEntry{names: names, expires: time.Now().Add(5 * time.Minute)}
	p.mu.Unlock()
	return append([]string(nil), names...), nil
}
