package mesh

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func mustNormalizeConfig(t *testing.T, cfg Config) Config {
	t.Helper()

	normalized, err := cfg.Normalize()
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	return normalized
}

func waitForHTTPBody(t *testing.T, url string, want string) {
	t.Helper()

	client := &http.Client{Timeout: 300 * time.Millisecond}
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error

	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			body, readErr := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if readErr == nil && resp.StatusCode == http.StatusOK && string(body) == want {
				return
			}
			lastErr = fmt.Errorf("status=%d body=%q readErr=%v", resp.StatusCode, string(body), readErr)
		} else {
			lastErr = err
		}
		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("GET %s did not return %q before timeout: %v", url, want, lastErr)
}

func waitForHTTPFailure(t *testing.T, url string) {
	t.Helper()

	client := &http.Client{Timeout: 200 * time.Millisecond}
	deadline := time.Now().Add(3 * time.Second)

	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err != nil {
			return
		}
		_ = resp.Body.Close()
		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("GET %s still succeeds after listener should have been closed", url)
}

func waitForProcessRemoval(t *testing.T, engine *Engine, port string) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		engine.procMu.Lock()
		_, exists := engine.process[port]
		engine.procMu.Unlock()
		if !exists {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("process on port %s was not removed from engine.process before timeout", port)
}

func waitForBackendDormant(t *testing.T, engine *Engine, publicPort string, internalPort string) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		route, exists := engine.GetStatus()[publicPort]
		if exists {
			for _, backend := range route.Backends {
				if backend.InternalPort == internalPort && backend.Status == "Dormant" && backend.PID == 0 && backend.Uptime == "" {
					return
				}
			}
		}
		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("backend %s under route %s did not become dormant before timeout", internalPort, publicPort)
}

func TestEngineGetConfigJSONReturnsRoutesOnly(t *testing.T) {
	t.Parallel()

	engine := newTestEngine(mustNormalizeConfig(t, Config{
		DashboardHost:  "0.0.0.0",
		DashboardPort:  "9999",
		TrustedOrigins: []string{"https://console.example.com"},
		Routes: map[string]RouteConfig{
			"8082": {
				Name: "Reloaded Route",
				Backends: []BackendConfig{
					{
						Name:         "backend-1",
						Cmd:          "worker",
						InternalPort: "9082",
					},
				},
			},
		},
	}))

	data := engine.GetConfigJSON()
	if bytes.Contains(data, []byte("dashboard_host")) {
		t.Fatalf("GetConfigJSON() leaked dashboard settings: %s", string(data))
	}

	var routes map[string]RouteConfig
	if err := json.Unmarshal(data, &routes); err != nil {
		t.Fatalf("json.Unmarshal(GetConfigJSON()) error = %v", err)
	}

	route, ok := routes["8082"]
	if !ok {
		t.Fatalf("GetConfigJSON() missing route 8082: %#v", routes)
	}
	if route.Name != "Reloaded Route" {
		t.Fatalf("route name = %q, want %q", route.Name, "Reloaded Route")
	}
	if len(route.Backends) != 1 || route.Backends[0].InternalPort != "9082" {
		t.Fatalf("route backends = %#v, want one backend on 9082", route.Backends)
	}
}

func TestEngineReloadConfigRejectsInvalidJSON(t *testing.T) {
	t.Parallel()

	engine := newTestEngine(Config{
		TrustedOrigins: []string{"*"},
		Routes: map[string]RouteConfig{
			"8081": {
				Name: "Existing Route",
				Backends: []BackendConfig{
					{
						Name:         "backend-1",
						Cmd:          "worker",
						InternalHost: "127.0.0.1",
						InternalPort: "9081",
					},
				},
			},
		},
	})

	err := engine.ReloadConfig([]byte(`{invalid`))
	if err == nil {
		t.Fatalf("ReloadConfig() error = nil, want JSON parse failure")
	}
	if !strings.Contains(err.Error(), "parse JSON failed") {
		t.Fatalf("ReloadConfig() error = %q, want JSON parse failure prefix", err.Error())
	}
	cfg := engine.configSnapshot()
	if _, ok := cfg.Routes["8081"]; !ok || len(cfg.Routes) != 1 {
		t.Fatalf("engine.cfg.Routes mutated after invalid JSON: %#v", cfg.Routes)
	}
}

func TestEngineReloadConfigRejectsInvalidRoutesWithoutPersisting(t *testing.T) {
	t.Parallel()

	configPath := filepath.Join(t.TempDir(), "config.json")
	cfg := mustNormalizeConfig(t, Config{
		ConfigPath:     configPath,
		TrustedOrigins: []string{"*"},
		Routes: map[string]RouteConfig{
			"8081": {
				Name: "Existing Route",
				Backends: []BackendConfig{
					{
						Name:         "backend-1",
						Cmd:          "worker",
						InternalPort: "9081",
					},
				},
			},
		},
	})
	if err := SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", configPath, err)
	}

	engine := newTestEngine(cfg)
	err = engine.ReloadConfig([]byte(`{"8082":{"name":"broken"}}`))
	if err == nil {
		t.Fatalf("ReloadConfig() error = nil, want route validation failure")
	}
	if !strings.Contains(err.Error(), "failed to check new config") {
		t.Fatalf("ReloadConfig() error = %q, want route validation failure prefix", err.Error())
	}

	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", configPath, err)
	}
	if string(after) != string(before) {
		t.Fatalf("config file changed after failed reload\nbefore=%s\nafter=%s", string(before), string(after))
	}
	currentCfg := engine.configSnapshot()
	if _, ok := currentCfg.Routes["8081"]; !ok || len(currentCfg.Routes) != 1 {
		t.Fatalf("engine.cfg.Routes mutated after failed validation: %#v", currentCfg.Routes)
	}
}

func TestEngineReloadConfigRestoresPreviousRoutesWhenBindingNewListenersFails(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	oldPublicPort := freePort(t)
	oldInternalPort := freePort(t)
	conflictingListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen(conflicting port) error = %v", err)
	}
	t.Cleanup(func() {
		_ = conflictingListener.Close()
	})
	conflictingPort := fmt.Sprintf("%d", conflictingListener.Addr().(*net.TCPAddr).Port)

	cfg := mustNormalizeConfig(t, Config{
		ConfigPath:     configPath,
		TrustedOrigins: []string{"*"},
		Routes: map[string]RouteConfig{
			oldPublicPort: {
				Name: "Old Route",
				Backends: []BackendConfig{
					{
						Name: "helper",
						Cmd:  os.Args[0],
						Args: []string{
							"-test.run=^TestHelperProcess$",
							"-gophermesh-helper=http",
							"-gophermesh-port=" + oldInternalPort,
						},
						InternalPort: oldInternalPort,
					},
				},
			},
		},
	})
	if err := SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", configPath, err)
	}

	engine := newTestEngine(cfg)
	if err := engine.startRoutes(); err != nil {
		t.Fatalf("startRoutes() error = %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = engine.Shutdown(ctx)
	})

	waitForHTTPBody(t, "http://127.0.0.1:"+oldPublicPort+"/before", "helper GET /before")

	rawRoutes, err := json.Marshal(map[string]RouteConfig{
		conflictingPort: {
			Name: "Broken Route",
			Backends: []BackendConfig{
				{
					Name:         "backend-1",
					Cmd:          "worker",
					InternalPort: freePort(t),
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	err = engine.ReloadConfig(rawRoutes)
	if err == nil {
		t.Fatalf("ReloadConfig() error = nil, want bind failure")
	}
	if !strings.Contains(err.Error(), "bind new port failed") {
		t.Fatalf("ReloadConfig() error = %q, want bind new port failure prefix", err.Error())
	}

	waitForHTTPBody(t, "http://127.0.0.1:"+oldPublicPort+"/after", "helper GET /after")

	currentCfg := engine.configSnapshot()
	if _, ok := currentCfg.Routes[oldPublicPort]; !ok || len(currentCfg.Routes) != 1 {
		t.Fatalf("engine.cfg.Routes changed after failed bind rollback: %#v", currentCfg.Routes)
	}

	after, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", configPath, err)
	}
	if string(after) != string(before) {
		t.Fatalf("config file changed after failed bind rollback\nbefore=%s\nafter=%s", string(before), string(after))
	}
}

func TestEngineReloadConfigRollsBackWhenSaveConfigFails(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "missing", "config.json")
	oldPublicPort := freePort(t)
	oldInternalPort := freePort(t)
	newPublicPort := freePort(t)
	newInternalPort, shutdownNewBackend := startHTTPBackend(t, "reloaded-backend")
	defer shutdownNewBackend()

	cfg := mustNormalizeConfig(t, Config{
		ConfigPath:     configPath,
		TrustedOrigins: []string{"*"},
		Routes: map[string]RouteConfig{
			oldPublicPort: {
				Name: "Old Route",
				Backends: []BackendConfig{
					{
						Name: "helper",
						Cmd:  os.Args[0],
						Args: []string{
							"-test.run=^TestHelperProcess$",
							"-gophermesh-helper=http",
							"-gophermesh-port=" + oldInternalPort,
						},
						InternalPort: oldInternalPort,
					},
				},
			},
		},
	})

	engine := newTestEngine(cfg)
	if err := engine.startRoutes(); err != nil {
		t.Fatalf("startRoutes() error = %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = engine.Shutdown(ctx)
	})

	waitForHTTPBody(t, "http://127.0.0.1:"+oldPublicPort+"/before", "helper GET /before")

	rawRoutes, err := json.Marshal(map[string]RouteConfig{
		newPublicPort: {
			Name: "New Route",
			Backends: []BackendConfig{
				{
					Name:         "backend-1",
					Cmd:          "worker",
					InternalPort: newInternalPort,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	err = engine.ReloadConfig(rawRoutes)
	if err == nil {
		t.Fatalf("ReloadConfig() error = nil, want save failure")
	}
	if !strings.Contains(err.Error(), "save config failed") {
		t.Fatalf("ReloadConfig() error = %q, want save config failure prefix", err.Error())
	}

	waitForHTTPBody(t, "http://127.0.0.1:"+oldPublicPort+"/after", "helper GET /after")
	waitForHTTPFailure(t, "http://127.0.0.1:"+newPublicPort+"/after")

	currentCfg := engine.configSnapshot()
	if _, ok := currentCfg.Routes[oldPublicPort]; !ok || len(currentCfg.Routes) != 1 {
		t.Fatalf("engine.cfg.Routes changed after save rollback: %#v", currentCfg.Routes)
	}
}

func TestEngineReloadConfigUpdatesRoutesPersistsAndCleansRemovedProcess(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	oldPublicPort := freePort(t)
	oldInternalPort := freePort(t)
	newPublicPort := freePort(t)
	newInternalPort, shutdownNewBackend := startHTTPBackend(t, "reloaded-backend")
	defer shutdownNewBackend()

	cfg := mustNormalizeConfig(t, Config{
		ConfigPath:     configPath,
		TrustedOrigins: []string{"*"},
		Routes: map[string]RouteConfig{
			oldPublicPort: {
				Name: "Old Route",
				Backends: []BackendConfig{
					{
						Name: "helper",
						Cmd:  os.Args[0],
						Args: []string{
							"-test.run=^TestHelperProcess$",
							"-gophermesh-helper=http",
							"-gophermesh-port=" + oldInternalPort,
						},
						InternalPort: oldInternalPort,
					},
				},
			},
		},
	})

	engine := newTestEngine(cfg)
	if err := engine.startRoutes(); err != nil {
		t.Fatalf("startRoutes() error = %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = engine.Shutdown(ctx)
	})

	waitForHTTPBody(t, "http://127.0.0.1:"+oldPublicPort+"/before", "helper GET /before")

	engine.procMu.Lock()
	_, hadOldProcess := engine.process[oldInternalPort]
	engine.procMu.Unlock()
	if !hadOldProcess {
		t.Fatalf("expected cold-start helper process on port %s to be tracked", oldInternalPort)
	}

	rawRoutes, err := json.Marshal(map[string]RouteConfig{
		newPublicPort: {
			Name: "New Route",
			Backends: []BackendConfig{
				{
					Name:         "backend-1",
					Cmd:          "worker",
					InternalPort: newInternalPort,
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	if err := engine.ReloadConfig(rawRoutes); err != nil {
		t.Fatalf("ReloadConfig() error = %v", err)
	}

	waitForProcessRemoval(t, engine, oldInternalPort)
	waitForHTTPFailure(t, "http://127.0.0.1:"+oldPublicPort+"/after")
	waitForHTTPBody(t, "http://127.0.0.1:"+newPublicPort+"/after", "reloaded-backend /after")

	currentCfg := engine.configSnapshot()
	if _, ok := currentCfg.Routes[newPublicPort]; !ok {
		t.Fatalf("engine.cfg.Routes missing new route %s: %#v", newPublicPort, currentCfg.Routes)
	}
	if _, ok := currentCfg.Routes[oldPublicPort]; ok {
		t.Fatalf("engine.cfg.Routes still contains removed route %s: %#v", oldPublicPort, currentCfg.Routes)
	}

	savedCfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig(%q) error = %v", configPath, err)
	}
	if _, ok := savedCfg.Routes[newPublicPort]; !ok {
		t.Fatalf("saved config missing new route %s: %#v", newPublicPort, savedCfg.Routes)
	}
	if _, ok := savedCfg.Routes[oldPublicPort]; ok {
		t.Fatalf("saved config still contains removed route %s: %#v", oldPublicPort, savedCfg.Routes)
	}
}

func TestEngineKillProcessRemovesBackendFromStatusImmediately(t *testing.T) {
	publicPort := freePort(t)
	internalPort := freePort(t)

	cfg := mustNormalizeConfig(t, Config{
		TrustedOrigins: []string{"*"},
		Routes: map[string]RouteConfig{
			publicPort: {
				Name: "Killable Route",
				Backends: []BackendConfig{
					{
						Name: "helper",
						Cmd:  os.Args[0],
						Args: []string{
							"-test.run=^TestHelperProcess$",
							"-gophermesh-helper=http",
							"-gophermesh-port=" + internalPort,
						},
						InternalPort: internalPort,
					},
				},
			},
		},
	})

	engine := newTestEngine(cfg)
	state := newRouteState(engine, publicPort, cfg.Routes[publicPort])
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = engine.Shutdown(ctx)
	})

	req := httptest.NewRequest(http.MethodGet, "http://mesh.local/spawn", nil)
	rec := httptest.NewRecorder()
	state.handleRequest(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("initial request status = %d, want %d body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	if err := engine.KillProcess(internalPort); err != nil {
		t.Fatalf("KillProcess(%q) error = %v", internalPort, err)
	}

	waitForProcessRemoval(t, engine, internalPort)
	waitForBackendDormant(t, engine, publicPort, internalPort)
}
