package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/pluginapi"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "--config-dir" {
		if strings.Contains(strings.Join(os.Args, " "), "--list-models") {
			cfg := os.Args[2]
			_ = os.WriteFile(filepath.Join(cfg, "discovery.env"), []byte(strings.Join(os.Environ(), "\n")), 0600)
			if _, err := os.Stat(filepath.Join(cfg, "model-block")); err == nil {
				_ = os.WriteFile(filepath.Join(cfg, "discovery.started"), nil, 0600)
				waitFixtureSignal()
			}
			_, _ = os.Stdout.WriteString("MODEL\nAuto\nQwen3.8-Flash\n")
			os.Exit(0)
		}
		fixtureCLI()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func testProvider(t *testing.T) *Provider {
	t.Helper()
	root := t.TempDir()
	for _, v := range []string{"global", "cn"} {
		dir := filepath.Join(root, v, ".auth")
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		for _, f := range []string{"user", "machine_id"} {
			if err := os.WriteFile(filepath.Join(dir, f), []byte("opaque"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewProvider(Config{Global: AccountConfig{CLI: exe, ConfigDir: filepath.Join(root, "global")}, CN: AccountConfig{CLI: exe, ConfigDir: filepath.Join(root, "cn")}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func TestDescriptorsIsolateAccounts(t *testing.T) {
	p := testProvider(t)
	for _, v := range []string{"global", "cn"} {
		out, err := p.ParseAuth(pluginapi.AuthParseRequest{RawJSON: []byte(`{"type":"qoder","variant":"` + v + `"}`)})
		if err != nil || !out.Handled {
			t.Fatalf("parse %s: %+v %v", v, out, err)
		}
		if out.Auth.Provider != "qoder" || out.Auth.Attributes["variant"] != v {
			t.Fatalf("bad auth: %+v", out.Auth)
		}
		if strings.Contains(string(out.Auth.StorageJSON), "opaque") {
			t.Fatal("credential leaked")
		}
		var storage map[string]any
		if err := json.Unmarshal(out.Auth.StorageJSON, &storage); err != nil {
			t.Fatal(err)
		}
		if len(storage) != 2 {
			t.Fatalf("descriptor contains unexpected storage: %s", out.Auth.StorageJSON)
		}
	}
	out, err := p.ParseAuth(pluginapi.AuthParseRequest{RawJSON: []byte(`{"type":"codex"}`)})
	if err != nil || out.Handled {
		t.Fatalf("foreign descriptor handled: %+v %v", out, err)
	}
	for _, raw := range []string{`{"type":"qoder"}`, `{"type":"qoder","variant":"other"}`} {
		if _, err := p.ParseAuth(pluginapi.AuthParseRequest{RawJSON: []byte(raw)}); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestModelsAreDiscoveredPerAccount(t *testing.T) {
	p := testProvider(t)
	for _, v := range []string{"global", "cn"} {
		out, err := p.ModelsForAuth(context.Background(), []byte(`{"type":"qoder","variant":"`+v+`"}`))
		if err != nil {
			t.Fatal(err)
		}
		prefix := "qoder/"
		if v == "cn" {
			prefix = "qoder-cn/"
		}
		if len(out.Models) != 2 || out.Models[0].ID != prefix+"Auto" || out.Models[1].ID != prefix+"Qwen3.8-Flash" {
			t.Fatalf("models: %+v", out)
		}
	}
}

func TestMissingCNDoesNotDisableGlobal(t *testing.T) {
	p := testProvider(t)
	p.cfg.CN.ConfigDir = filepath.Join(t.TempDir(), "missing")
	if _, err := p.ModelsForAuth(context.Background(), []byte(`{"type":"qoder","variant":"global"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ModelsForAuth(context.Background(), []byte(`{"type":"qoder","variant":"cn"}`)); err == nil {
		t.Fatal("missing credentials accepted")
	}
}

func TestRejectCrossRegionModel(t *testing.T) {
	for _, tc := range []struct{ variant, model string }{{"global", "qoder-cn/Auto"}, {"cn", "qoder/Auto"}, {"cn", "Auto"}, {"global", "qoder/--help"}, {"cn", "qoder-cn/"}} {
		if _, err := nativeModel(tc.variant, tc.model); err == nil {
			t.Fatalf("accepted %#v", tc)
		}
	}
	if got, err := nativeModel("cn", "qoder-cn/Qwen3.8-Flash"); err != nil || got != "Qwen3.8-Flash" {
		t.Fatalf("%q %v", got, err)
	}
}
