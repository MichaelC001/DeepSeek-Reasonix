package boot

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"

	"reasonix/internal/config"
)

// Config can contain proxy passwords, headers, and service tokens. Keep its
// fingerprint process-local and keyed rather than exposing a password oracle.
var runtimeConfigKey = func() []byte {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(err)
	}
	return key
}()

// The extension graph omits host configuration. Its no-op/narrow plans may
// reuse a controller only when the effective configuration still matches the
// one that built it, including project overrides and caller-owned snapshots.
func canReuseRuntimeConfiguration(previous *BuildResult, opts Options) bool {
	if previous.configFingerprint == "" {
		return false
	}
	cfg, _, err := resolveBuildSelection(ResolveWorkspaceRoot(opts.WorkspaceRoot), opts)
	if err != nil {
		return false // Let full assembly report its ordinary configuration error.
	}
	fingerprint := runtimeConfigFingerprint(cfg)
	return fingerprint != "" && fingerprint == previous.configFingerprint
}

func withRuntimeConfiguration(res *BuildResult, cfg *config.Config) *BuildResult {
	res.configFingerprint = runtimeConfigFingerprint(cfg)
	return res
}

func runtimeConfigFingerprint(cfg *config.Config) string {
	// Provider credentials are unexported config fields. Their existing keyed digest
	// detects rotations without retaining another copy of the resolved secrets.
	data, err := json.Marshal(struct {
		Config      *config.Config
		ModelAccess string
	}{cfg, cfg.ModelRuntimeFingerprint("")})
	if err != nil {
		return "" // An unrepresentable snapshot must never authorize reuse.
	}
	mac := hmac.New(sha256.New, runtimeConfigKey)
	_, _ = mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
}
