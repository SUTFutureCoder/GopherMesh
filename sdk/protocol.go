package mesh

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const LaunchProtocolScheme = "gophermesh"

type LaunchProtocolOptions struct {
	Scheme            string
	DisplayName       string
	WindowsKeyName    string
	LinuxDesktopName  string
	MacOSBundleID     string
}

type LaunchProtocolRequest struct {
	Port       string
	ConfigPath string
}

func HandleLaunchProtocol(rawURL string, cfg Config) (LaunchProtocolRequest, error) {
	req, err := ParseLaunchProtocol(rawURL)
	if err != nil {
		return LaunchProtocolRequest{}, err
	}
	if err := ValidateLaunchPort(cfg, req.Port); err != nil {
		return LaunchProtocolRequest{}, err
	}
	return req, nil
}

func ParseLaunchProtocol(rawURL string) (LaunchProtocolRequest, error) {
	return ParseLaunchProtocolWithOptions(rawURL, LaunchProtocolOptions{})
}

func ParseLaunchProtocolWithOptions(rawURL string, options LaunchProtocolOptions) (LaunchProtocolRequest, error) {
	normalized := normalizeLaunchProtocolOptions(options)
	u, err := url.Parse(rawURL)
	if err != nil {
		return LaunchProtocolRequest{}, fmt.Errorf("parse protocol url: %w", err)
	}
	if !strings.EqualFold(u.Scheme, normalized.Scheme) {
		return LaunchProtocolRequest{}, fmt.Errorf("unsupported protocol %q", u.Scheme)
	}

	action := strings.Trim(strings.TrimSpace(u.Host), "/")
	if action == "" {
		action = strings.Trim(strings.TrimSpace(u.Path), "/")
	}
	if action == "" {
		action = strings.Trim(strings.TrimSpace(u.Opaque), "/")
		if idx := strings.IndexByte(action, '?'); idx >= 0 {
			action = action[:idx]
		}
	}
	if !strings.EqualFold(action, "launch") {
		return LaunchProtocolRequest{}, fmt.Errorf("unsupported protocol action %q", action)
	}

	port := strings.TrimSpace(u.Query().Get("port"))
	configPath := strings.TrimSpace(u.Query().Get("conf"))
	if configPath == "" {
		configPath = strings.TrimSpace(u.Query().Get("config"))
	}

	return LaunchProtocolRequest{
		Port:       port,
		ConfigPath: configPath,
	}, nil
}

func ValidateLaunchPort(cfg Config, port string) error {
	publicPort := strings.TrimSpace(port)
	if publicPort == "" {
		return nil
	}

	normalized, err := cfg.Normalize()
	if err != nil {
		return fmt.Errorf("normalize config: %w", err)
	}
	if _, ok := normalized.Routes[publicPort]; !ok {
		return fmt.Errorf("port %s is not declared in config.json", publicPort)
	}
	return nil
}

func IsPublicRouteHealthy(port string) bool {
	return isTCPEndpointHealthy(defaultLocalHost, strings.TrimSpace(port))
}

func IsDashboardHealthy(cfg Config) bool {
	normalized, err := cfg.Normalize()
	if err != nil {
		return false
	}

	host := strings.TrimSpace(normalized.DashboardHost)
	if host == "" || host == "0.0.0.0" {
		host = defaultLocalHost
	}
	return isTCPEndpointHealthy(host, normalized.DashboardPort)
}

func isTCPEndpointHealthy(host, port string) bool {
	if strings.TrimSpace(host) == "" || strings.TrimSpace(port) == "" {
		return false
	}

	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), 500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func RegisterLaunchProtocol() error {
	return RegisterLaunchProtocolWithOptions(LaunchProtocolOptions{})
}

func RegisterLaunchProtocolWithOptions(options LaunchProtocolOptions) error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}
	if isTransientGoRunExecutable(exePath) {
		return fmt.Errorf("skip protocol registration for transient go run binary %q; build a stable executable first", exePath)
	}
	return registerLaunchProtocolForExecutable(exePath, normalizeLaunchProtocolOptions(options))
}

func ResolveLaunchConfigPath(defaultConfigPath, overrideConfigPath string) (string, error) {
	exePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %w", err)
	}
	return resolveLaunchConfigPathForExecutable(defaultConfigPath, overrideConfigPath, exePath), nil
}

func resolveLaunchConfigPathForExecutable(defaultConfigPath, overrideConfigPath, exePath string) string {
	configPath := strings.TrimSpace(overrideConfigPath)
	if configPath == "" {
		configPath = strings.TrimSpace(defaultConfigPath)
	}
	if configPath == "" {
		configPath = "config.json"
	}
	if filepath.IsAbs(configPath) {
		return filepath.Clean(configPath)
	}
	return filepath.Join(filepath.Dir(exePath), filepath.FromSlash(configPath))
}

func registerLaunchProtocolForExecutable(exePath string, options LaunchProtocolOptions) error {
	switch runtime.GOOS {
	case "windows":
		return registerWindowsLaunchProtocol(exePath, options)
	case "linux":
		return registerLinuxLaunchProtocol(exePath, options)
	case "darwin":
		return registerDarwinLaunchProtocol(exePath, options)
	default:
		return nil
	}
}

func registerWindowsLaunchProtocol(exePath string, options LaunchProtocolOptions) error {
	commandValue := fmt.Sprintf(`"%s" -protocol-url "%%1"`, exePath)
	registryKey := fmt.Sprintf(`HKCU\Software\Classes\%s`, options.WindowsKeyName)
	if err := addRegistryValue(registryKey, "", "URL:"+options.DisplayName+" Protocol"); err != nil {
		return err
	}
	if err := addRegistryValue(registryKey, "URL Protocol", ""); err != nil {
		return err
	}
	if err := addRegistryValue(registryKey+`\shell\open\command`, "", commandValue); err != nil {
		return err
	}
	return nil
}

func registerLinuxLaunchProtocol(exePath string, options LaunchProtocolOptions) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home dir: %w", err)
	}

	slug := strings.ToLower(strings.TrimSpace(options.LinuxDesktopName))
	wrapperPath := filepath.Join(home, ".local", "share", slug, slug+"-open-url")
	if err := writeProtocolWrapper(wrapperPath, exePath); err != nil {
		return err
	}

	desktopFileName := slug + ".desktop"
	desktopPath := filepath.Join(home, ".local", "share", "applications", desktopFileName)
	desktopBody := fmt.Sprintf(`[Desktop Entry]
Name=GopherMesh
Exec=%s %%u
Type=Application
Terminal=false
NoDisplay=true
MimeType=x-scheme-handler/%s;
`, quoteDesktopExecArg(wrapperPath), options.Scheme)
	desktopBody = strings.Replace(desktopBody, "Name=GopherMesh", "Name="+options.DisplayName, 1)
	if err := writeExecutableFile(desktopPath, []byte(desktopBody), 0644); err != nil {
		return fmt.Errorf("write desktop entry: %w", err)
	}

	if _, err := runCommand("xdg-mime", "default", desktopFileName, "x-scheme-handler/"+options.Scheme); err != nil {
		return fmt.Errorf("set xdg-mime handler: %w", err)
	}

	_, _ = runCommand("update-desktop-database", filepath.Dir(desktopPath))
	return nil
}

func registerDarwinLaunchProtocol(exePath string, options LaunchProtocolOptions) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home dir: %w", err)
	}

	slug := strings.ToLower(strings.TrimSpace(options.DisplayName))
	wrapperPath := filepath.Join(home, "Library", "Application Support", options.DisplayName, slug+"-open-url.command")
	if err := writeProtocolWrapper(wrapperPath, exePath); err != nil {
		return err
	}

	lsregisterPath := "/System/Library/Frameworks/CoreServices.framework/Frameworks/LaunchServices.framework/Support/lsregister"
	if _, err := os.Stat(lsregisterPath); err == nil {
		_, _ = runCommand(lsregisterPath, "-f", exePath)
		_, _ = runCommand(lsregisterPath, "-f", wrapperPath)
	}

	if !launchServiceHandlerExists(options.Scheme) {
		handler := fmt.Sprintf("{LSHandlerURLScheme=%s;LSHandlerRoleAll=%s;}", options.Scheme, options.MacOSBundleID)
		if _, err := runCommand("defaults", "write", "com.apple.LaunchServices/com.apple.launchservices.secure", "LSHandlers", "-array-add", handler); err != nil {
			return fmt.Errorf("write launch services handler: %w", err)
		}
	}

	return nil
}

func addRegistryValue(keyPath, name, value string) error {
	args := []string{"add", keyPath, "/f"}
	if name == "" {
		args = append(args, "/ve")
	} else {
		args = append(args, "/v", name)
	}
	args = append(args, "/t", "REG_SZ", "/d", value)

	output, err := exec.Command("reg", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("reg add %s failed: %w: %s", keyPath, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func writeProtocolWrapper(wrapperPath, exePath string) error {
	script := "#!/bin/sh\nexec " + shellQuote(exePath) + " -protocol-url \"$1\"\n"
	if err := writeExecutableFile(wrapperPath, []byte(script), 0755); err != nil {
		return fmt.Errorf("write protocol wrapper: %w", err)
	}
	return nil
}

func writeExecutableFile(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, perm); err != nil {
		return err
	}
	return os.Chmod(path, perm)
}

func quoteDesktopExecArg(value string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + replacer.Replace(value) + `"`
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func launchServiceHandlerExists(scheme string) bool {
	output, err := exec.Command("defaults", "read", "com.apple.LaunchServices/com.apple.launchservices.secure", "LSHandlers").CombinedOutput()
	if err != nil {
		return false
	}
	normalizedOutput := strings.ToLower(string(output))
	normalizedOutput = strings.NewReplacer(
		" ", "",
		"\t", "",
		"\r", "",
		"\n", "",
		`"`, "",
		";", "",
	).Replace(normalizedOutput)
	want := "lshandlerurlscheme=" + strings.ToLower(strings.TrimSpace(scheme))
	return strings.Contains(normalizedOutput, want)
}

func normalizeLaunchProtocolOptions(options LaunchProtocolOptions) LaunchProtocolOptions {
	scheme := strings.TrimSpace(options.Scheme)
	if scheme == "" {
		scheme = LaunchProtocolScheme
	}
	displayName := strings.TrimSpace(options.DisplayName)
	if displayName == "" {
		displayName = "GopherMesh"
	}
	windowsKeyName := strings.TrimSpace(options.WindowsKeyName)
	if windowsKeyName == "" {
		windowsKeyName = scheme
	}
	linuxDesktopName := strings.TrimSpace(options.LinuxDesktopName)
	if linuxDesktopName == "" {
		linuxDesktopName = windowsKeyName
	}
	macOSBundleID := strings.TrimSpace(options.MacOSBundleID)
	if macOSBundleID == "" {
		macOSBundleID = "com.gophermesh.cli"
	}
	return LaunchProtocolOptions{
		Scheme:           scheme,
		DisplayName:      displayName,
		WindowsKeyName:   windowsKeyName,
		LinuxDesktopName: linuxDesktopName,
		MacOSBundleID:    macOSBundleID,
	}
}

func isTransientGoRunExecutable(exePath string) bool {
	clean := strings.ToLower(filepath.Clean(exePath))
	tempDir := strings.ToLower(filepath.Clean(os.TempDir()))
	return strings.Contains(clean, "go-build") && strings.HasPrefix(clean, tempDir)
}

func runCommand(name string, args ...string) ([]byte, error) {
	output, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("%s %s failed: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return output, nil
}
