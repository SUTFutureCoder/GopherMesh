package mesh

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestParseLaunchProtocol(t *testing.T) {
	req, err := ParseLaunchProtocol("gophermesh://launch?port=8090")
	if err != nil {
		t.Fatalf("ParseLaunchProtocol() error = %v", err)
	}
	if req.Port != "8090" {
		t.Fatalf("Port = %q, want %q", req.Port, "8090")
	}
	if req.ConfigPath != "" {
		t.Fatalf("ConfigPath = %q, want blank", req.ConfigPath)
	}
}

func TestParseLaunchProtocolAllowsEmptyPort(t *testing.T) {
	req, err := ParseLaunchProtocol("gophermesh://launch")
	if err != nil {
		t.Fatalf("ParseLaunchProtocol() error = %v", err)
	}
	if req.Port != "" {
		t.Fatalf("Port = %q, want blank", req.Port)
	}
}

func TestParseLaunchProtocolSupportsPathForm(t *testing.T) {
	req, err := ParseLaunchProtocol("gophermesh:launch?port=8090")
	if err != nil {
		t.Fatalf("ParseLaunchProtocol() error = %v", err)
	}
	if req.Port != "8090" {
		t.Fatalf("Port = %q, want %q", req.Port, "8090")
	}
}

func TestParseLaunchProtocolSupportsConf(t *testing.T) {
	req, err := ParseLaunchProtocol("gophermesh://launch?port=18081&conf=sample/sample_config.json")
	if err != nil {
		t.Fatalf("ParseLaunchProtocol() error = %v", err)
	}
	if req.Port != "18081" {
		t.Fatalf("Port = %q, want %q", req.Port, "18081")
	}
	if req.ConfigPath != "sample/sample_config.json" {
		t.Fatalf("ConfigPath = %q, want %q", req.ConfigPath, "sample/sample_config.json")
	}
}

func TestParseLaunchProtocolRejectsUnsupportedAction(t *testing.T) {
	if _, err := ParseLaunchProtocol("gophermesh://status?port=8090"); err == nil {
		t.Fatal("ParseLaunchProtocol() error = nil, want unsupported action")
	}
}

func TestValidateLaunchPort(t *testing.T) {
	cfg := DefaultConfig()
	if err := ValidateLaunchPort(cfg, "8081"); err != nil {
		t.Fatalf("ValidateLaunchPort() error = %v", err)
	}
}

func TestValidateLaunchPortAllowsBlank(t *testing.T) {
	cfg := DefaultConfig()
	if err := ValidateLaunchPort(cfg, ""); err != nil {
		t.Fatalf("ValidateLaunchPort() error = %v, want nil for blank port", err)
	}
}

func TestValidateLaunchPortRejectsUnknownPort(t *testing.T) {
	cfg := Config{
		Routes: map[string]RouteConfig{
			"8090": {
				Name: "API",
				Backends: []BackendConfig{
					{InternalPort: "19090"},
				},
			},
		},
	}
	if err := ValidateLaunchPort(cfg, "9999"); err == nil {
		t.Fatal("ValidateLaunchPort() error = nil, want unknown port rejection")
	}
}

func TestQuoteDesktopExecArg(t *testing.T) {
	got := quoteDesktopExecArg(`C:\Program Files\gophermesh\gophermesh.exe`)
	want := `"C:\\Program Files\\gophermesh\\gophermesh.exe"`
	if got != want {
		t.Fatalf("quoteDesktopExecArg() = %q, want %q", got, want)
	}
}

func TestShellQuote(t *testing.T) {
	got := shellQuote(`/tmp/it's-mesh`)
	want := `'/tmp/it'"'"'s-mesh'`
	if got != want {
		t.Fatalf("shellQuote() = %q, want %q", got, want)
	}
}

func TestIsPublicRouteHealthy(t *testing.T) {
	listener, err := net.Listen("tcp", net.JoinHostPort(defaultLocalHost, "0"))
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	defer listener.Close()

	port := listener.Addr().(*net.TCPAddr).Port
	if !IsPublicRouteHealthy(fmt.Sprintf("%d", port)) {
		t.Fatal("IsPublicRouteHealthy() = false, want true")
	}
}

func TestIsPublicRouteHealthyReturnsFalseWhenPortIsClosed(t *testing.T) {
	listener, err := net.Listen("tcp", net.JoinHostPort(defaultLocalHost, "0"))
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("listener.Close() error = %v", err)
	}

	if IsPublicRouteHealthy(fmt.Sprintf("%d", port)) {
		t.Fatal("IsPublicRouteHealthy() = true, want false for unopened port")
	}
}

func TestIsDashboardHealthy(t *testing.T) {
	listener, err := net.Listen("tcp", net.JoinHostPort(defaultLocalHost, "0"))
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	defer listener.Close()

	port := fmt.Sprintf("%d", listener.Addr().(*net.TCPAddr).Port)
	cfg := Config{DashboardHost: defaultLocalHost, DashboardPort: port}
	if !IsDashboardHealthy(cfg) {
		t.Fatal("IsDashboardHealthy() = false, want true")
	}
}

func TestResolveLaunchConfigPathForExecutableUsesOverrideRelativeToExecutable(t *testing.T) {
	got := resolveLaunchConfigPathForExecutable("config.json", "sample/sample_config.json", `C:\mesh\gophermesh.exe`)
	want := filepath.Join(`C:\mesh`, "sample", "sample_config.json")
	if got != want {
		t.Fatalf("resolveLaunchConfigPathForExecutable() = %q, want %q", got, want)
	}
}

func TestResolveLaunchConfigPathForExecutableFallsBackToDefaultRelativeToExecutable(t *testing.T) {
	got := resolveLaunchConfigPathForExecutable("config.json", "", `C:\mesh\gophermesh.exe`)
	want := filepath.Join(`C:\mesh`, "config.json")
	if got != want {
		t.Fatalf("resolveLaunchConfigPathForExecutable() = %q, want %q", got, want)
	}
}

func TestIsTransientGoRunExecutable(t *testing.T) {
	tempDir := filepath.Clean(os.TempDir())
	path := filepath.Join(tempDir, "go-build123", "b001", "exe", "gophermesh.exe")
	if !isTransientGoRunExecutable(path) {
		t.Fatalf("isTransientGoRunExecutable(%q) = false, want true", path)
	}
}
