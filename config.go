package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginabi"
)

type AccountConfig struct {
	CLI       string `yaml:"cli"`
	ConfigDir string `yaml:"config-dir"`
	Disabled  bool   `yaml:"disabled"`
}
type Config struct {
	Global   AccountConfig `yaml:"global"`
	CN       AccountConfig `yaml:"cn"`
	Helper   string        `yaml:"helper"`
	ProxyURL string        `yaml:"proxy-url"`
}
type catalogEntry struct {
	names   []string
	expires time.Time
}
type Provider struct {
	cfg      Config
	mu       sync.Mutex
	catalogs map[string]catalogEntry
	active   map[string]map[uint64]context.CancelFunc
	nextID   uint64
	closed   bool
	workers  sync.WaitGroup
}

func NewProvider(cfg Config) (*Provider, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	for _, item := range []struct {
		a        *AccountConfig
		cli, dir string
	}{{&cfg.Global, "qodercli", ".qoder"}, {&cfg.CN, "qoderclicn", ".qoder-cn"}} {
		if item.a.CLI == "" {
			item.a.CLI = item.cli
		}
		if item.a.ConfigDir == "" {
			item.a.ConfigDir = filepath.Join(home, item.dir)
		}
		if strings.HasPrefix(item.a.ConfigDir, "~/") {
			item.a.ConfigDir = filepath.Join(home, item.a.ConfigDir[2:])
		}
		item.a.ConfigDir, err = filepath.Abs(item.a.ConfigDir)
		if err != nil {
			return nil, err
		}
	}
	return &Provider{cfg: cfg, catalogs: make(map[string]catalogEntry), active: make(map[string]map[uint64]context.CancelFunc)}, nil
}
func (p *Provider) account(variant string) (AccountConfig, error) {
	var a AccountConfig
	switch variant {
	case "global":
		a = p.cfg.Global
	case "cn":
		a = p.cfg.CN
	default:
		return a, badRequest("unknown Qoder account variant")
	}
	if a.Disabled {
		return a, pluginabi.NewError("account_disabled", "Qoder account is disabled", 503)
	}
	for _, name := range []string{"user", "machine_id"} {
		info, err := os.Stat(filepath.Join(a.ConfigDir, ".auth", name))
		if err != nil || info.IsDir() || info.Size() == 0 {
			return a, pluginabi.NewError("login_required", fmt.Sprintf("Qoder %s is not logged in; run %s login", variant, filepath.Base(a.CLI)), 401)
		}
	}
	return a, nil
}
func badRequest(message string) error { return pluginabi.NewError("invalid_request", message, 400) }
func (p *Provider) track(ctx context.Context, requestID string) (context.Context, func(), error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, nil, pluginabi.NewError("plugin_closed", "CPA-Qoder is shutting down", 503)
	}
	ctx, cancel := context.WithCancel(ctx)
	p.nextID++
	id := p.nextID
	if p.active[requestID] == nil {
		p.active[requestID] = make(map[uint64]context.CancelFunc)
	}
	p.active[requestID][id] = cancel
	p.workers.Add(1)
	var once sync.Once
	return ctx, func() {
		once.Do(func() {
			defer p.workers.Done()
			cancel()
			p.mu.Lock()
			delete(p.active[requestID], id)
			if len(p.active[requestID]) == 0 {
				delete(p.active, requestID)
			}
			p.mu.Unlock()
		})
	}, nil
}
func (p *Provider) Cancel(requestID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, cancel := range p.active[requestID] {
		cancel()
	}
}
func (p *Provider) Close() {
	p.mu.Lock()
	p.closed = true
	for _, calls := range p.active {
		for _, cancel := range calls {
			cancel()
		}
	}
	p.mu.Unlock()
	p.workers.Wait()
}
