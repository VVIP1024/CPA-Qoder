package core

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
		wantID, wantLabel := "qoder", "Qoder"
		if v == "cn" {
			wantID, wantLabel = "qodercn", "QoderCN"
		}
		if out.Auth.ID != wantID || out.Auth.FileName != wantID+".json" || out.Auth.Label != wantLabel {
			t.Fatalf("unexpected auth identity: %+v", out.Auth)
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
		prefix, label := qoderModelPrefix(v)
		if len(out.Models) != 2 || out.Models[0].ID != prefix+"Auto" || out.Models[1].ID != prefix+"Qwen3.8-Flash" || out.Models[0].DisplayName != label+"/Auto" {
			t.Fatalf("models: %+v", out)
		}
	}
}

func TestAuthStateDoesNotRequireLogin(t *testing.T) {
	root := t.TempDir()
	p, err := NewProvider(Config{
		Global: AccountConfig{ConfigDir: filepath.Join(root, "global")},
		CN:     AccountConfig{ConfigDir: filepath.Join(root, "cn")},
	})
	if err != nil {
		t.Fatal(err)
	}
	d := descriptor{Type: "qoder", Variant: "global"}
	a, err := p.withAuthState(authData(d), d)
	if err != nil {
		t.Fatal(err)
	}
	if a.Metadata["authenticated"] != false {
		t.Fatalf("unexpected auth state: %+v", a.Metadata)
	}
	if _, err := p.account(d); err == nil {
		t.Fatal("executor auth accepted missing login")
	}
	models, err := p.ModelsForAuth(context.Background(), []byte(`{"type":"qoder","variant":"global"}`))
	if err != nil || len(models.Models) != 0 {
		t.Fatalf("models=%+v err=%v", models, err)
	}
}

func TestMissingCNDoesNotDisableGlobal(t *testing.T) {
	p := testProvider(t)
	p.cfg.CN.ConfigDir = filepath.Join(t.TempDir(), "missing")
	if _, err := p.ModelsForAuth(context.Background(), []byte(`{"type":"qoder","variant":"global"}`)); err != nil {
		t.Fatal(err)
	}
	out, err := p.ModelsForAuth(context.Background(), []byte(`{"type":"qoder","variant":"cn"}`))
	if err != nil || len(out.Models) != 0 {
		t.Fatalf("missing login must yield an empty model list, got %+v err=%v", out, err)
	}
}

func TestNamedAccountsUseIsolatedConfigsAndCatalogs(t *testing.T) {
	p := testProvider(t)
	p.cfg.Accounts = []NamedAccountConfig{{ID: "work", Variant: "global", CLI: p.cfg.Global.CLI, ConfigDir: filepath.Join(t.TempDir(), "work")}}
	if err := os.MkdirAll(filepath.Join(p.cfg.Accounts[0].ConfigDir, ".auth"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"user", "machine_id"} {
		if err := os.WriteFile(filepath.Join(p.cfg.Accounts[0].ConfigDir, ".auth", name), []byte("opaque"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.ModelsForAuth(context.Background(), []byte(`{"type":"qoder","variant":"global"}`)); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"type":"qoder","variant":"global","account_id":"work"}`)
	models, err := p.ModelsForAuth(context.Background(), raw)
	if err != nil || len(models.Models) != 2 {
		t.Fatalf("models=%+v err=%v", models, err)
	}
	if p.catalogs[descriptorKey(descriptor{Variant: "global"})].names[0] != "Auto" || p.catalogs["global:work"].names[0] != "Auto" {
		t.Fatal("account catalogs were not stored separately")
	}
	auth := authData(descriptor{Type: "qoder", Variant: "global", AccountID: "work"})
	if auth.ID != "qoder-work" || auth.FileName != "qoder-work.json" || auth.Label != "Qoder (work)" {
		t.Fatalf("unexpected named account identity: %+v", auth)
	}
}

func TestNamedAccountsRequireDistinctConfigDirs(t *testing.T) {
	root := t.TempDir()
	if _, err := NewProvider(Config{Global: AccountConfig{ConfigDir: filepath.Join(root, "global")}, CN: AccountConfig{ConfigDir: filepath.Join(root, "cn")}, Accounts: []NamedAccountConfig{{ID: "copy", Variant: "global", ConfigDir: filepath.Join(root, "global")}}}); err == nil {
		t.Fatal("accepted account sharing a config-dir")
	}
}

func TestModelsUseAccountPrefix(t *testing.T) {
	for _, tc := range []struct{ variant, model string }{{"global", "qoder-cn/Auto"}, {"cn", "qoder/Auto"}, {"cn", "qoder-cn/Auto"}, {"cn", "-help"}, {"cn", ""}, {"other", "Auto"}} {
		if _, err := nativeModel(tc.variant, tc.model); err == nil {
			t.Fatalf("accepted %#v", tc)
		}
	}
	for _, tc := range []struct{ variant, model, want string }{
		{"global", "qoder/Qwen3.8-Flash", "Qwen3.8-Flash"},
		{"cn", "qodercn/Qwen3.8-Flash", "Qwen3.8-Flash"},
		{"cn", "Qwen3.8-Flash", "Qwen3.8-Flash"},
	} {
		if got, err := nativeModel(tc.variant, tc.model); err != nil || got != tc.want {
			t.Fatalf("%q: %q %v", tc.model, got, err)
		}
	}
}

func TestOwnsModelAcceptsAccountPrefix(t *testing.T) {
	p := &Provider{catalogs: map[string]catalogEntry{"global": {names: []string{"Auto"}}}}
	for _, model := range []string{"Auto", "qoder/Auto", "qodercn/Auto"} {
		if !p.ownsModel(model) {
			t.Fatalf("did not own %q", model)
		}
	}
	if p.ownsModel("other/Auto") {
		t.Fatal("owned unrelated model")
	}
}
