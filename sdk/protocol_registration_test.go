package mesh

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRegisterWindowsLaunchProtocol(t *testing.T) {
	commandDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "reg.log")
	prependCommandPath(t, commandDir)
	setTestEnv(t, "LOGFILE", logPath)
	writeFakeCommand(t, commandDir, "reg")

	exePath := `C:\Program Files\gophermesh\gophermesh.exe`
	if err := registerWindowsLaunchProtocol(exePath, normalizeLaunchProtocolOptions(LaunchProtocolOptions{})); err != nil {
		t.Fatalf("registerWindowsLaunchProtocol() error = %v", err)
	}

	logBody := readTestFile(t, logPath)
	if count := strings.Count(logBody, "reg "); count != 3 {
		t.Fatalf("reg invocation count = %d, want 3\nlog:\n%s", count, logBody)
	}
	if !strings.Contains(logBody, `reg add HKCU\Software\Classes\gophermesh /f /ve /t REG_SZ /d "URL:GopherMesh Protocol"`) {
		t.Fatalf("missing root protocol registration\nlog:\n%s", logBody)
	}
	if !strings.Contains(logBody, `reg add HKCU\Software\Classes\gophermesh /f /v "URL Protocol" /t REG_SZ /d ""`) {
		t.Fatalf("missing URL Protocol registration\nlog:\n%s", logBody)
	}
	if !strings.Contains(logBody, `reg add HKCU\Software\Classes\gophermesh\shell\open\command /f /ve /t REG_SZ /d`) {
		t.Fatalf("missing open command registration\nlog:\n%s", logBody)
	}
	if !strings.Contains(logBody, `-protocol-url`) {
		t.Fatalf("missing protocol-url argument in registered command\nlog:\n%s", logBody)
	}
}

func TestRegisterLinuxLaunchProtocol(t *testing.T) {
	commandDir := t.TempDir()
	homeDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "linux.log")
	prependCommandPath(t, commandDir)
	setTestEnv(t, "LOGFILE", logPath)
	setUserHomeEnv(t, homeDir)
	writeFakeCommand(t, commandDir, "xdg-mime")
	writeFakeCommand(t, commandDir, "update-desktop-database")

	exePath := filepath.Join(homeDir, "bin", "gophermesh")
	if err := registerLinuxLaunchProtocol(exePath, normalizeLaunchProtocolOptions(LaunchProtocolOptions{})); err != nil {
		t.Fatalf("registerLinuxLaunchProtocol() error = %v", err)
	}

	wrapperPath := filepath.Join(homeDir, ".local", "share", "gophermesh", "gophermesh-open-url")
	desktopPath := filepath.Join(homeDir, ".local", "share", "applications", "gophermesh.desktop")

	wrapperBody := readTestFile(t, wrapperPath)
	wantWrapper := "#!/bin/sh\nexec " + shellQuote(exePath) + " -protocol-url \"$1\"\n"
	if wrapperBody != wantWrapper {
		t.Fatalf("wrapper body = %q, want %q", wrapperBody, wantWrapper)
	}

	desktopBody := readTestFile(t, desktopPath)
	if !strings.Contains(desktopBody, "Exec="+quoteDesktopExecArg(wrapperPath)+" %u") {
		t.Fatalf("desktop entry missing Exec line\nbody:\n%s", desktopBody)
	}
	if !strings.Contains(desktopBody, "MimeType=x-scheme-handler/"+LaunchProtocolScheme+";") {
		t.Fatalf("desktop entry missing scheme handler\nbody:\n%s", desktopBody)
	}

	logBody := readTestFile(t, logPath)
	if !strings.Contains(logBody, "xdg-mime default gophermesh.desktop x-scheme-handler/"+LaunchProtocolScheme) {
		t.Fatalf("missing xdg-mime registration\nlog:\n%s", logBody)
	}
	if !strings.Contains(logBody, "update-desktop-database "+filepath.Dir(desktopPath)) {
		t.Fatalf("missing desktop database refresh\nlog:\n%s", logBody)
	}
}

func TestRegisterDarwinLaunchProtocolWritesHandler(t *testing.T) {
	commandDir := t.TempDir()
	homeDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "darwin.log")
	prependCommandPath(t, commandDir)
	setTestEnv(t, "LOGFILE", logPath)
	setTestEnv(t, "DEFAULTS_READ_OUTPUT", "")
	setUserHomeEnv(t, homeDir)
	writeFakeCommand(t, commandDir, "defaults")

	exePath := filepath.Join(homeDir, "bin", "gophermesh")
	if err := registerDarwinLaunchProtocol(exePath, normalizeLaunchProtocolOptions(LaunchProtocolOptions{})); err != nil {
		t.Fatalf("registerDarwinLaunchProtocol() error = %v", err)
	}

	wrapperPath := filepath.Join(homeDir, "Library", "Application Support", "GopherMesh", "gophermesh-open-url.command")
	wrapperBody := readTestFile(t, wrapperPath)
	wantWrapper := "#!/bin/sh\nexec " + shellQuote(exePath) + " -protocol-url \"$1\"\n"
	if wrapperBody != wantWrapper {
		t.Fatalf("wrapper body = %q, want %q", wrapperBody, wantWrapper)
	}

	logBody := readTestFile(t, logPath)
	if !strings.Contains(logBody, "defaults read com.apple.LaunchServices/com.apple.launchservices.secure LSHandlers") {
		t.Fatalf("missing launch services read\nlog:\n%s", logBody)
	}
	if !strings.Contains(logBody, "defaults write com.apple.LaunchServices/com.apple.launchservices.secure LSHandlers -array-add {LSHandlerURLScheme="+LaunchProtocolScheme+";LSHandlerRoleAll=com.gophermesh.cli;}") {
		t.Fatalf("missing launch services write\nlog:\n%s", logBody)
	}
}

func TestRegisterDarwinLaunchProtocolSkipsDuplicateHandler(t *testing.T) {
	commandDir := t.TempDir()
	homeDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "darwin-dup.log")
	prependCommandPath(t, commandDir)
	setTestEnv(t, "LOGFILE", logPath)
	setTestEnv(t, "DEFAULTS_READ_OUTPUT", "LSHandlerURLScheme = gophermesh")
	setUserHomeEnv(t, homeDir)
	writeFakeCommand(t, commandDir, "defaults")

	exePath := filepath.Join(homeDir, "bin", "gophermesh")
	if err := registerDarwinLaunchProtocol(exePath, normalizeLaunchProtocolOptions(LaunchProtocolOptions{})); err != nil {
		t.Fatalf("registerDarwinLaunchProtocol() error = %v", err)
	}

	logBody := readTestFile(t, logPath)
	if !strings.Contains(logBody, "defaults read com.apple.LaunchServices/com.apple.launchservices.secure LSHandlers") {
		t.Fatalf("missing launch services read\nlog:\n%s", logBody)
	}
	if strings.Contains(logBody, "defaults write com.apple.LaunchServices/com.apple.launchservices.secure LSHandlers -array-add") {
		t.Fatalf("unexpected duplicate launch services write\nlog:\n%s", logBody)
	}
}

func TestRegisterLinuxLaunchProtocolWithCustomOptions(t *testing.T) {
	commandDir := t.TempDir()
	homeDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "linux-custom.log")
	prependCommandPath(t, commandDir)
	setTestEnv(t, "LOGFILE", logPath)
	setUserHomeEnv(t, homeDir)
	writeFakeCommand(t, commandDir, "xdg-mime")
	writeFakeCommand(t, commandDir, "update-desktop-database")

	options := normalizeLaunchProtocolOptions(LaunchProtocolOptions{
		Scheme:           "etaiIotPlugin",
		DisplayName:      "etaiIotPlugin",
		LinuxDesktopName: "etaiiotplugin",
	})

	exePath := filepath.Join(homeDir, "bin", "etaiIotPlugin")
	if err := registerLinuxLaunchProtocol(exePath, options); err != nil {
		t.Fatalf("registerLinuxLaunchProtocol() error = %v", err)
	}

	desktopPath := filepath.Join(homeDir, ".local", "share", "applications", "etaiiotplugin.desktop")
	desktopBody := readTestFile(t, desktopPath)
	if !strings.Contains(desktopBody, "Name=etaiIotPlugin") {
		t.Fatalf("desktop entry missing custom name\nbody:\n%s", desktopBody)
	}
	if !strings.Contains(desktopBody, "MimeType=x-scheme-handler/etaiIotPlugin;") {
		t.Fatalf("desktop entry missing custom scheme\nbody:\n%s", desktopBody)
	}
}

func prependCommandPath(t *testing.T, dir string) {
	t.Helper()
	oldPath, ok := os.LookupEnv("PATH")
	newPath := dir
	if ok && oldPath != "" {
		newPath += string(os.PathListSeparator) + oldPath
	}
	if err := os.Setenv("PATH", newPath); err != nil {
		t.Fatalf("set PATH: %v", err)
	}
	t.Cleanup(func() {
		if ok {
			_ = os.Setenv("PATH", oldPath)
			return
		}
		_ = os.Unsetenv("PATH")
	})
}

func setUserHomeEnv(t *testing.T, homeDir string) {
	t.Helper()
	setTestEnv(t, "HOME", homeDir)
	setTestEnv(t, "USERPROFILE", homeDir)
}

func setTestEnv(t *testing.T, key, value string) {
	t.Helper()
	oldValue, ok := os.LookupEnv(key)
	if err := os.Setenv(key, value); err != nil {
		t.Fatalf("set %s: %v", key, err)
	}
	t.Cleanup(func() {
		if ok {
			_ = os.Setenv(key, oldValue)
			return
		}
		_ = os.Unsetenv(key)
	})
}

func writeFakeCommand(t *testing.T, dir, name string) {
	t.Helper()
	fileName := name
	body := "#!/bin/sh\n" +
		"printf '%s %s\\n' '" + name + "' \"$*\" >> \"$LOGFILE\"\n" +
		"if [ \"$1\" = \"read\" ] && [ -n \"$DEFAULTS_READ_OUTPUT\" ]; then\n" +
		"  printf '%s\\n' \"$DEFAULTS_READ_OUTPUT\"\n" +
		"fi\n"
	if runtime.GOOS == "windows" {
		fileName += ".cmd"
		body = "@echo off\r\n" +
			"echo " + name + " %*>> \"%LOGFILE%\"\r\n" +
			"if /I \"%1\"==\"read\" (\r\n" +
			"  if defined DEFAULTS_READ_OUTPUT echo %DEFAULTS_READ_OUTPUT%\r\n" +
			")\r\n" +
			"exit /b 0\r\n"
	}
	commandPath := filepath.Join(dir, fileName)
	if err := os.WriteFile(commandPath, []byte(body), 0755); err != nil {
		t.Fatalf("write fake command %s: %v", name, err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(commandPath, 0755); err != nil {
			t.Fatalf("chmod fake command %s: %v", name, err)
		}
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
