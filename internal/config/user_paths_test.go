package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func isolateUserPathEnv(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AppData", filepath.Join(home, "AppData"))
	t.Setenv(reasonixHomeEnv, "")
	return home
}

func unsetForTest(t *testing.T, key string) {
	t.Helper()
	old, had := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(key, old)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

func writePathTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReasonixHomeOverridesConfigAndStateRoots(t *testing.T) {
	home := isolateUserPathEnv(t)
	root := filepath.Join(home, "custom-reasonix")
	t.Setenv(reasonixHomeEnv, root)

	if got := UserConfigRoot(); got != root {
		t.Fatalf("UserConfigRoot() = %q, want %q", got, root)
	}
	if got := UserConfigPath(); got != filepath.Join(root, "config.toml") {
		t.Fatalf("UserConfigPath() = %q", got)
	}
	if got := SessionDir(); got != filepath.Join(root, "sessions") {
		t.Fatalf("SessionDir() = %q", got)
	}
	if roots := UserConfigRoots(); len(roots) != 1 || roots[0] != root {
		t.Fatalf("UserConfigRoots() = %v, want [%s]", roots, root)
	}
}

func TestSandboxAllowWriteSurvivesProjectSandboxDefaults(t *testing.T) {
	home := isolateUserPathEnv(t)
	root := filepath.Join(home, "reasonix-home")
	t.Setenv(reasonixHomeEnv, root)
	writePathTestFile(t, UserConfigPath(), `[sandbox]
allow_write = ["/tmp/reasonix-extra"]
network = true
`)
	project := t.TempDir()
	writePathTestFile(t, filepath.Join(project, "reasonix.toml"), `[sandbox]
bash = "enforce"
`)

	cfg, err := LoadForRoot(project)
	if err != nil {
		t.Fatalf("LoadForRoot: %v", err)
	}
	if len(cfg.Sandbox.AllowWrite) != 1 || cfg.Sandbox.AllowWrite[0] != "/tmp/reasonix-extra" {
		t.Fatalf("allow_write = %v, want preserved user value", cfg.Sandbox.AllowWrite)
	}
}

func TestMacOSFreshInstallUsesDocumentedConfigRoot(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-specific path policy")
	}
	home := isolateUserPathEnv(t)
	want := filepath.Join(home, ".config", "reasonix")

	if got := UserConfigRoot(); got != want {
		t.Fatalf("UserConfigRoot() = %q, want %q", got, want)
	}
	if got := UserStateRoot(); got != want {
		t.Fatalf("UserStateRoot() = %q, want %q", got, want)
	}
}

func TestMacOSLoadsDocumentedConfigOverNativeAndKeepsNativeState(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-specific path policy")
	}
	home := isolateUserPathEnv(t)
	native := filepath.Join(home, "Library", "Application Support", "reasonix")
	documented := filepath.Join(home, ".config", "reasonix")
	writePathTestFile(t, filepath.Join(native, "config.toml"), `default_model = "native-model"`)
	writePathTestFile(t, filepath.Join(documented, "config.toml"), `default_model = "documented-model"`)

	cfg, err := LoadForRoot(t.TempDir())
	if err != nil {
		t.Fatalf("LoadForRoot: %v", err)
	}
	if cfg.DefaultModel != "documented-model" {
		t.Fatalf("default_model = %q, want documented-model", cfg.DefaultModel)
	}
	if got := UserConfigPath(); got != filepath.Join(documented, "config.toml") {
		t.Fatalf("UserConfigPath() = %q, want documented path", got)
	}
	if got := UserStateRoot(); got != native {
		t.Fatalf("UserStateRoot() = %q, want old native state root %q", got, native)
	}
}

func TestMacOSCredentialsLoadActiveThenNativeFallback(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-specific path policy")
	}
	home := isolateUserPathEnv(t)
	native := filepath.Join(home, "Library", "Application Support", "reasonix")
	documented := filepath.Join(home, ".config", "reasonix")
	writePathTestFile(t, filepath.Join(documented, "config.toml"), `default_model = "documented-model"`)
	writePathTestFile(t, filepath.Join(documented, "credentials"), "KEY_SHARED=active\nKEY_ACTIVE=1\n")
	writePathTestFile(t, filepath.Join(native, "credentials"), "KEY_SHARED=native\nKEY_FALLBACK=1\n")
	unsetForTest(t, "KEY_SHARED")
	unsetForTest(t, "KEY_ACTIVE")
	unsetForTest(t, "KEY_FALLBACK")

	loadDotEnvForRoot(t.TempDir())

	if got := os.Getenv("KEY_SHARED"); got != "active" {
		t.Fatalf("KEY_SHARED = %q, want active", got)
	}
	if got := os.Getenv("KEY_ACTIVE"); got != "1" {
		t.Fatalf("KEY_ACTIVE = %q, want 1", got)
	}
	if got := os.Getenv("KEY_FALLBACK"); got != "1" {
		t.Fatalf("KEY_FALLBACK = %q, want 1", got)
	}
	if paths := strings.Join(UserCredentialsPaths(), "\n"); !strings.Contains(paths, filepath.Join(native, "credentials")) {
		t.Fatalf("UserCredentialsPaths() missing native fallback:\n%s", paths)
	}
}

func TestMacOSDocumentedCredentialsSelectDocumentedWriteRoot(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS-specific path policy")
	}
	home := isolateUserPathEnv(t)
	native := filepath.Join(home, "Library", "Application Support", "reasonix")
	documented := filepath.Join(home, ".config", "reasonix")
	if err := os.MkdirAll(native, 0o755); err != nil {
		t.Fatal(err)
	}
	writePathTestFile(t, filepath.Join(documented, "credentials"), "KEY_DOCS=1\n")

	if got := UserCredentialsPath(); got != filepath.Join(documented, "credentials") {
		t.Fatalf("UserCredentialsPath() = %q, want documented credentials path", got)
	}
	if got := UserStateRoot(); got != native {
		t.Fatalf("UserStateRoot() = %q, want old native state root", got)
	}
}
