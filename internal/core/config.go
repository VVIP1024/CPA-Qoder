package core

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

// defaultQueueWait caps how long a request waits for a model that the
// upstream put in a queue. The CLI keeps polling for minutes on its own,
// which turns a capacity problem into a client timeout.
const defaultQueueWait = 60 * time.Second

type AccountConfig struct {
	CLI       string `yaml:"cli"`
	ConfigDir string `yaml:"config-dir"`
	Disabled  bool   `yaml:"disabled"`
}
type NamedAccountConfig struct {
	ID        string `yaml:"id"`
	Variant   string `yaml:"variant"`
	CLI       string `yaml:"cli"`
	ConfigDir string `yaml:"config-dir"`
	Disabled  bool   `yaml:"disabled"`
}
type Config struct {
	Global    AccountConfig        `yaml:"global"`
	CN        AccountConfig        `yaml:"cn"`
	Accounts  []NamedAccountConfig `yaml:"accounts"`
	Helper    string               `yaml:"helper"`
	ProxyURL  string               `yaml:"proxy-url"`
	QueueWait string               `yaml:"queue-wait"`
}
type catalogEntry struct {
	names   []string
	expires time.Time
}
type Provider struct {
	cfg       Config
	mu        sync.Mutex
	catalogs  map[string]catalogEntry
	active    map[string]map[uint64]context.CancelFunc
	nextID    uint64
	closed    bool
	workers   sync.WaitGroup
	queueWait time.Duration
}

func NewProvider(cfg Config) (*Provider, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	queueWait := defaultQueueWait
	if cfg.QueueWait != "" {
		queueWait, err = time.ParseDuration(cfg.QueueWait)
		if err != nil || queueWait < 0 {
			return nil, badRequest("queue-wait must be a duration such as 60s")
		}
	}
	for _, item := range []struct {
		a        *AccountConfig
		cli, dir string
	}{{&cfg.Global, "qoder", ".qoder"}, {&cfg.CN, "qodercn", ".qoder-cn"}} {
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
	seen := map[string]bool{}
	seenConfigDirs := map[string]bool{cfg.Global.ConfigDir: true, cfg.CN.ConfigDir: true}
	for i := range cfg.Accounts {
		account := &cfg.Accounts[i]
		if !accountIDPattern.MatchString(account.ID) || (account.Variant != "global" && account.Variant != "cn") || seen[account.ID] {
			return nil, badRequest("additional Qoder accounts require unique safe IDs and variant global or cn")
		}
		seen[account.ID] = true
		if account.CLI == "" {
			if account.Variant == "cn" {
				account.CLI = "qodercn"
			} else {
				account.CLI = "qoder"
			}
		}
		if account.ConfigDir == "" {
			return nil, badRequest("additional Qoder accounts require an isolated config-dir")
		}
		if strings.HasPrefix(account.ConfigDir, "~/") {
			account.ConfigDir = filepath.Join(home, account.ConfigDir[2:])
		}
		account.ConfigDir, err = filepath.Abs(account.ConfigDir)
		if err != nil {
			return nil, err
		}
		if seenConfigDirs[account.ConfigDir] {
			return nil, badRequest("each Qoder account must use an isolated config-dir")
		}
		seenConfigDirs[account.ConfigDir] = true
	}
	return &Provider{cfg: cfg, catalogs: make(map[string]catalogEntry), active: make(map[string]map[uint64]context.CancelFunc), queueWait: queueWait}, nil
}
func (p *Provider) resolveAccount(d descriptor) (AccountConfig, error) {
	var a AccountConfig
	if d.AccountID != "" {
		for _, configured := range p.cfg.Accounts {
			if configured.ID == d.AccountID && configured.Variant == d.Variant {
				a = AccountConfig{CLI: configured.CLI, ConfigDir: configured.ConfigDir, Disabled: configured.Disabled}
				break
			}
		}
		if a.ConfigDir == "" {
			return a, badRequest("Qoder account_id is not configured")
		}
	} else {
		switch d.Variant {
		case "global":
			a = p.cfg.Global
		case "cn":
			a = p.cfg.CN
		default:
			return a, badRequest("unknown Qoder account variant")
		}
	}
	if a.Disabled {
		return a, pluginabi.NewError("account_disabled", "Qoder account is disabled", 503)
	}
	return a, nil
}

func (p *Provider) hasLogin(d descriptor) (bool, error) {
	a, err := p.resolveAccount(d)
	if err != nil {
		return false, err
	}
	for _, name := range []string{"user", "machine_id"} {
		info, err := os.Stat(filepath.Join(a.ConfigDir, ".auth", name))
		if err != nil || info.IsDir() || info.Size() == 0 {
			return false, nil
		}
	}
	return true, nil
}

func (p *Provider) loginCommand(d descriptor) string {
	a, err := p.resolveAccount(d)
	if err != nil {
		return "qoder login"
	}
	return filepath.Base(a.CLI) + " login"
}

func (p *Provider) account(d descriptor) (AccountConfig, error) {
	a, err := p.resolveAccount(d)
	if err != nil {
		return a, err
	}
	loggedIn, err := p.hasLogin(d)
	if err != nil {
		return a, err
	}
	if !loggedIn {
		label := d.AccountID
		if label == "" {
			label = d.Variant
		}
		return a, pluginabi.NewError("login_required", fmt.Sprintf("Qoder account %s is not logged in; run %s login", label, filepath.Base(a.CLI)), 401)
	}
	return a, nil
}
func descriptorKey(d descriptor) string {
	return d.Variant + ":" + d.AccountID
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
