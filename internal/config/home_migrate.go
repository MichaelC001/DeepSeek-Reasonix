package config

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

const (
	homeMigrationMarkerName      = ".reasonix-home-migration.json"
	homeMigrationBackupDirName   = ".reasonix-home-migration-backups"
	homeMigrationConflictDirName = ".reasonix-home-migration-conflicts"
)

type homeMigrationMarker struct {
	Version     int    `json:"version"`
	From        string `json:"from"`
	To          string `json:"to"`
	CompletedAt string `json:"completed_at"`
	RunID       string `json:"run_id"`
}

// HomeMigrationOptions controls a full user-root migration. Without Apply it
// only scans and reports what would happen.
type HomeMigrationOptions struct {
	From  string
	To    string
	Apply bool

	// Now and RunID are test hooks. Production callers leave them zero.
	Now   time.Time
	RunID string
}

// HomeMigrationOperation is one planned or applied data movement.
type HomeMigrationOperation struct {
	Kind        string `json:"kind"`
	Source      string `json:"source,omitempty"`
	Destination string `json:"destination,omitempty"`
	Note        string `json:"note,omitempty"`
	Bytes       int64  `json:"bytes,omitempty"`
}

// HomeMigrationReport summarizes a dry-run or applied migration.
type HomeMigrationReport struct {
	From                    string                   `json:"from"`
	To                      string                   `json:"to"`
	Marker                  string                   `json:"marker"`
	Apply                   bool                     `json:"apply"`
	Needed                  bool                     `json:"needed"`
	AlreadyMigrated         bool                     `json:"already_migrated"`
	RunID                   string                   `json:"run_id,omitempty"`
	FilesCopied             int                      `json:"files_copied"`
	DirsCreated             int                      `json:"dirs_created"`
	FilesSkipped            int                      `json:"files_skipped"`
	ConfigsMerged           int                      `json:"configs_merged"`
	CredentialsMerged       int                      `json:"credentials_merged"`
	SessionConflictsRenamed int                      `json:"session_conflicts_renamed"`
	ConflictsArchived       int                      `json:"conflicts_archived"`
	BytesCopied             int64                    `json:"bytes_copied"`
	Warnings                []string                 `json:"warnings,omitempty"`
	Operations              []HomeMigrationOperation `json:"operations,omitempty"`
}

type homeMigrationEntry struct {
	rel  string
	path string
	info fs.FileInfo
}

// DefaultHomeMigrationRoots returns the built-in full-home migration roots. The
// automatic root pair is intentionally narrow: the macOS native app-data path
// that Go used historically, into the documented ~/.config/reasonix path.
func DefaultHomeMigrationRoots() (from, to string, ok bool) {
	if _, explicit := explicitReasonixRoot(); explicit {
		return "", "", false
	}
	if runtime.GOOS != "darwin" {
		return "", "", false
	}
	from = nativeUserRoot()
	to = macOSDocumentedUserRoot()
	if from == "" || to == "" || samePath(from, to) {
		return "", "", false
	}
	return from, to, true
}

// HomeMigrationMarkerPath returns the marker file that makes a completed home
// migration authoritative for future path resolution.
func HomeMigrationMarkerPath(root string) string {
	root = cleanUserRoot(root)
	if root == "" {
		return ""
	}
	return filepath.Join(root, homeMigrationMarkerName)
}

func completedHomeMigrationRoot() string {
	if _, explicit := explicitReasonixRoot(); explicit {
		return ""
	}
	if runtime.GOOS != "darwin" {
		return ""
	}
	root := macOSDocumentedUserRoot()
	if root == "" {
		return ""
	}
	marker, err := readHomeMigrationMarker(HomeMigrationMarkerPath(root))
	if err != nil || marker == nil {
		return ""
	}
	if samePath(marker.To, root) {
		return root
	}
	return ""
}

func readHomeMigrationMarker(path string) (*homeMigrationMarker, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var marker homeMigrationMarker
	if err := json.Unmarshal(data, &marker); err != nil {
		return nil, err
	}
	if marker.Version != 1 || strings.TrimSpace(marker.To) == "" {
		return nil, fmt.Errorf("invalid home migration marker %s", path)
	}
	return &marker, nil
}

// MigrateHome copies and merges all known Reasonix user data from one root to
// another. It never deletes the source root and writes the completion marker only
// after every planned operation succeeds.
func MigrateHome(opts HomeMigrationOptions) (*HomeMigrationReport, error) {
	from, to, err := resolveHomeMigrationRoots(opts)
	if err != nil {
		return nil, err
	}
	report := &HomeMigrationReport{
		From:   from,
		To:     to,
		Marker: HomeMigrationMarkerPath(to),
		Apply:  opts.Apply,
		RunID:  homeMigrationRunID(opts),
	}
	if samePath(from, to) {
		return nil, fmt.Errorf("home migration source and destination are the same: %s", from)
	}
	if nestedPath(from, to) || nestedPath(to, from) {
		return nil, fmt.Errorf("home migration source and destination must not be nested: %s -> %s", from, to)
	}
	if marker, err := readHomeMigrationMarker(report.Marker); err == nil && marker != nil && samePath(marker.To, to) {
		report.AlreadyMigrated = true
		return report, nil
	} else if err != nil {
		return nil, err
	}
	if !pathExists(from) {
		report.Warnings = append(report.Warnings, "source root does not exist; nothing to migrate")
		return report, nil
	}
	report.Needed = true
	if opts.Apply {
		if err := os.MkdirAll(to, 0o755); err != nil {
			return report, fmt.Errorf("create destination root: %w", err)
		}
	}
	if err := migrateHomeConfig(report); err != nil {
		return report, err
	}
	if err := migrateHomeCredentials(report); err != nil {
		return report, err
	}
	if err := migrateHomeTree(report); err != nil {
		return report, err
	}
	if opts.Apply {
		if err := writeHomeMigrationMarker(report); err != nil {
			return report, err
		}
		report.Operations = append(report.Operations, HomeMigrationOperation{
			Kind:        "write-marker",
			Destination: report.Marker,
			Note:        "future runs use the destination root only",
		})
	}
	return report, nil
}

func resolveHomeMigrationRoots(opts HomeMigrationOptions) (string, string, error) {
	from := cleanUserRoot(opts.From)
	to := cleanUserRoot(opts.To)
	if from == "" && to == "" {
		var ok bool
		from, to, ok = DefaultHomeMigrationRoots()
		if !ok {
			return "", "", fmt.Errorf("no default home migration is available; pass --from and --to")
		}
	}
	if from == "" || to == "" {
		return "", "", fmt.Errorf("both --from and --to are required when overriding migration roots")
	}
	return from, to, nil
}

func homeMigrationRunID(opts HomeMigrationOptions) string {
	if id := strings.TrimSpace(opts.RunID); id != "" {
		return sanitizeMigrationID(id)
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	var b [4]byte
	if _, err := rand.Read(b[:]); err == nil {
		return sanitizeMigrationID(now.UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b[:]))
	}
	return sanitizeMigrationID(now.UTC().Format("20060102-150405"))
}

func sanitizeMigrationID(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "run"
	}
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return strings.Trim(b.String(), "-")
}

func writeHomeMigrationMarker(report *HomeMigrationReport) error {
	marker := homeMigrationMarker{
		Version:     1,
		From:        report.From,
		To:          report.To,
		CompletedAt: time.Now().UTC().Format(time.RFC3339),
		RunID:       report.RunID,
	}
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(report.Marker), 0o755); err != nil {
		return fmt.Errorf("create marker dir: %w", err)
	}
	if err := os.WriteFile(report.Marker, data, 0o644); err != nil {
		return fmt.Errorf("write home migration marker: %w", err)
	}
	return nil
}

func migrateHomeConfig(report *HomeMigrationReport) error {
	src := filepath.Join(report.From, "config.toml")
	dst := filepath.Join(report.To, "config.toml")
	srcExists := fileExists(src)
	dstExists := fileExists(dst)
	switch {
	case !srcExists && !dstExists:
		return nil
	case srcExists && !dstExists:
		return copyHomeFile(report, src, dst, "copy-config")
	case !srcExists && dstExists:
		report.FilesSkipped++
		report.Operations = append(report.Operations, HomeMigrationOperation{Kind: "skip-config", Destination: dst, Note: "destination config already exists"})
		return nil
	}
	same, err := sameFileContent(src, dst)
	if err != nil {
		return err
	}
	if same {
		report.FilesSkipped++
		report.Operations = append(report.Operations, HomeMigrationOperation{Kind: "skip-config", Source: src, Destination: dst, Note: "identical"})
		return nil
	}
	if report.Apply {
		if err := backupHomeFile(report, src, "config.source.toml"); err != nil {
			return err
		}
		if err := backupHomeFile(report, dst, "config.destination.toml"); err != nil {
			return err
		}
	}
	cfg := Default()
	for _, path := range []string{src, dst} {
		if err := mergeFile(cfg, path); err != nil {
			return err
		}
	}
	plugins, err := mergeTOMLPlugins([]string{src, dst})
	if err != nil {
		return err
	}
	cfg.Plugins = plugins
	normalizePluginCommandLines(cfg)
	normalizeLegacyEffort(cfg)
	normalizeLegacyMCPTiers(cfg)
	normalizeLegacyProviderModels(cfg)
	normalizeDesktopOfficialProviderAccess(cfg)
	normalizeEffortConfig(cfg)
	if report.Apply {
		if err := writeConfigFile(dst, RenderTOMLForScope(cfg, RenderScopeUser)); err != nil {
			return err
		}
	}
	report.ConfigsMerged++
	report.Operations = append(report.Operations, HomeMigrationOperation{Kind: "merge-config", Source: src, Destination: dst, Note: "destination values win; raw source and destination configs were backed up"})
	return nil
}

func migrateHomeCredentials(report *HomeMigrationReport) error {
	src := filepath.Join(report.From, "credentials")
	dst := filepath.Join(report.To, "credentials")
	srcExists := fileExists(src)
	dstExists := fileExists(dst)
	switch {
	case !srcExists && !dstExists:
		return nil
	case srcExists && !dstExists:
		return copyHomeFile(report, src, dst, "copy-credentials")
	case !srcExists && dstExists:
		report.FilesSkipped++
		report.Operations = append(report.Operations, HomeMigrationOperation{Kind: "skip-credentials", Destination: dst, Note: "destination credentials already exist"})
		return nil
	}
	same, err := sameFileContent(src, dst)
	if err != nil {
		return err
	}
	if same {
		report.FilesSkipped++
		report.Operations = append(report.Operations, HomeMigrationOperation{Kind: "skip-credentials", Source: src, Destination: dst, Note: "identical"})
		return nil
	}
	srcData, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	dstData, err := os.ReadFile(dst)
	if err != nil {
		return err
	}
	merged := mergeCredentialData(srcData, dstData)
	if report.Apply {
		if err := backupHomeFile(report, src, "credentials.source"); err != nil {
			return err
		}
		if err := backupHomeFile(report, dst, "credentials.destination"); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, merged, 0o600); err != nil {
			return err
		}
	}
	report.CredentialsMerged++
	report.Operations = append(report.Operations, HomeMigrationOperation{Kind: "merge-credentials", Source: src, Destination: dst, Note: "destination keys win; source-only keys appended"})
	return nil
}

func mergeCredentialData(src, dst []byte) []byte {
	dstKeys := map[string]bool{}
	for _, line := range splitCredentialLines(dst) {
		if key, ok := credentialLineKey(line); ok {
			dstKeys[key] = true
		}
	}
	lines := splitCredentialLines(bytes.TrimRight(dst, "\n"))
	for _, line := range splitCredentialLines(src) {
		key, ok := credentialLineKey(line)
		if !ok || dstKeys[key] {
			continue
		}
		lines = append(lines, line)
		dstKeys[key] = true
	}
	if len(lines) == 0 {
		return nil
	}
	return append([]byte(strings.Join(lines, "\n")), '\n')
}

func splitCredentialLines(data []byte) []string {
	if len(data) == 0 {
		return nil
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func credentialLineKey(line string) (string, bool) {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "export ")
	if line == "" || strings.HasPrefix(line, "#") {
		return "", false
	}
	key, _, ok := strings.Cut(line, "=")
	if !ok {
		return "", false
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", false
	}
	for _, r := range key {
		if !(r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return "", false
		}
	}
	return key, true
}

func migrateHomeTree(report *HomeMigrationReport) error {
	entries, err := collectHomeMigrationEntries(report.From)
	if err != nil {
		return err
	}
	skippedDirs := map[string]bool{}
	for _, e := range entries {
		if !e.info.IsDir() || shouldSkipHomeMigrationRel(e.rel) || isSessionCheckpointDirRel(e.rel) || isUnderSkippedDir(e.rel, skippedDirs) {
			continue
		}
		dst := filepath.Join(report.To, e.rel)
		if info, err := os.Lstat(dst); err == nil && !info.IsDir() {
			if err := archiveHomeConflict(report, e.path, e.rel); err != nil {
				return err
			}
			skippedDirs[e.rel] = true
			continue
		} else if err != nil && !os.IsNotExist(err) {
			return err
		}
		if report.Apply {
			if err := os.MkdirAll(dst, e.info.Mode().Perm()); err != nil {
				return err
			}
		}
		report.DirsCreated++
		report.Operations = append(report.Operations, HomeMigrationOperation{Kind: "create-dir", Source: e.path, Destination: dst})
	}

	sessionRenames := map[string]string{}
	for _, e := range entries {
		if e.info.IsDir() || shouldSkipHomeMigrationRel(e.rel) || isUnderSkippedDir(e.rel, skippedDirs) || !isSessionJSONLRel(e.rel) {
			continue
		}
		newRel, err := copyHomeSession(report, e)
		if err != nil {
			return err
		}
		if newRel != "" && newRel != e.rel {
			sessionRenames[filepath.ToSlash(e.rel)] = filepath.ToSlash(newRel)
		}
	}

	var sidecars []homeMigrationEntry
	for _, e := range entries {
		if e.info.IsDir() || shouldSkipHomeMigrationRel(e.rel) || isUnderSkippedDir(e.rel, skippedDirs) || isSessionJSONLRel(e.rel) {
			continue
		}
		if isSessionSidecarRel(e.rel) {
			sidecars = append(sidecars, e)
			continue
		}
		targetRel := mappedSessionArtifactRel(e.rel, sessionRenames)
		if targetRel == "" {
			targetRel = e.rel
		}
		if err := copyHomePath(report, e.path, targetRel, "copy-file"); err != nil {
			return err
		}
	}
	for _, e := range sidecars {
		if err := mergeSessionSidecar(report, e, sessionRenames); err != nil {
			return err
		}
	}
	return nil
}

func collectHomeMigrationEntries(root string) ([]homeMigrationEntry, error) {
	var entries []homeMigrationEntry
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		entries = append(entries, homeMigrationEntry{rel: rel, path: path, info: info})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		return filepath.ToSlash(entries[i].rel) < filepath.ToSlash(entries[j].rel)
	})
	return entries, nil
}

func shouldSkipHomeMigrationRel(rel string) bool {
	rel = filepath.ToSlash(filepath.Clean(rel))
	switch rel {
	case ".", "config.toml", "credentials", homeMigrationMarkerName, homeMigrationBackupDirName, homeMigrationConflictDirName:
		return true
	}
	first, _, _ := strings.Cut(rel, "/")
	return first == homeMigrationBackupDirName || first == homeMigrationConflictDirName
}

func isUnderSkippedDir(rel string, skipped map[string]bool) bool {
	rel = filepath.ToSlash(filepath.Clean(rel))
	for dir := range skipped {
		dir = filepath.ToSlash(filepath.Clean(dir))
		if rel == dir || strings.HasPrefix(rel, dir+"/") {
			return true
		}
	}
	return false
}

func copyHomeSession(report *HomeMigrationReport, e homeMigrationEntry) (string, error) {
	dst := filepath.Join(report.To, e.rel)
	if !fileExists(dst) {
		if err := copyHomePath(report, e.path, e.rel, "copy-session"); err != nil {
			return "", err
		}
		return e.rel, nil
	}
	same, err := sameFileContent(e.path, dst)
	if err != nil {
		return "", err
	}
	if same {
		report.FilesSkipped++
		report.Operations = append(report.Operations, HomeMigrationOperation{Kind: "skip-session", Source: e.path, Destination: dst, Note: "identical"})
		return e.rel, nil
	}
	newRel := uniqueMigratedSessionRel(report.To, e.rel, report.RunID)
	if err := copyHomePath(report, e.path, newRel, "rename-session-conflict"); err != nil {
		return "", err
	}
	report.SessionConflictsRenamed++
	return newRel, nil
}

func copyHomePath(report *HomeMigrationReport, src, targetRel, kind string) error {
	targetRel = filepath.Clean(targetRel)
	dst := filepath.Join(report.To, targetRel)
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if dstInfo, err := os.Lstat(dst); err == nil {
		if info.Mode()&os.ModeType == dstInfo.Mode()&os.ModeType {
			if info.Mode().IsRegular() && dstInfo.Mode().IsRegular() {
				same, err := sameFileContent(src, dst)
				if err != nil {
					return err
				}
				if same {
					report.FilesSkipped++
					report.Operations = append(report.Operations, HomeMigrationOperation{Kind: "skip-file", Source: src, Destination: dst, Note: "identical"})
					return nil
				}
			}
			if info.Mode()&os.ModeSymlink != 0 {
				srcTarget, srcErr := os.Readlink(src)
				dstTarget, dstErr := os.Readlink(dst)
				if srcErr == nil && dstErr == nil && srcTarget == dstTarget {
					report.FilesSkipped++
					report.Operations = append(report.Operations, HomeMigrationOperation{Kind: "skip-symlink", Source: src, Destination: dst, Note: "identical"})
					return nil
				}
			}
		}
		return archiveHomeConflict(report, src, targetRel)
	} else if !os.IsNotExist(err) {
		return err
	}
	if report.Apply {
		if err := copyPathPreserve(src, dst, info); err != nil {
			return err
		}
	}
	report.FilesCopied++
	report.BytesCopied += fileSize(info)
	report.Operations = append(report.Operations, HomeMigrationOperation{Kind: kind, Source: src, Destination: dst, Bytes: fileSize(info)})
	return nil
}

func copyHomeFile(report *HomeMigrationReport, src, dst, kind string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if report.Apply {
		if err := copyPathPreserve(src, dst, info); err != nil {
			return err
		}
	}
	report.FilesCopied++
	report.BytesCopied += fileSize(info)
	report.Operations = append(report.Operations, HomeMigrationOperation{Kind: kind, Source: src, Destination: dst, Bytes: fileSize(info)})
	return nil
}

func backupHomeFile(report *HomeMigrationReport, src, name string) error {
	dst := filepath.Join(report.To, homeMigrationBackupDirName, report.RunID, name)
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if err := copyPathPreserve(src, dst, info); err != nil {
		return err
	}
	report.Operations = append(report.Operations, HomeMigrationOperation{Kind: "backup", Source: src, Destination: dst, Bytes: fileSize(info)})
	return nil
}

func archiveHomeConflict(report *HomeMigrationReport, src, rel string) error {
	dst := filepath.Join(report.To, homeMigrationConflictDirName, report.RunID, filepath.Clean(rel))
	info, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if report.Apply {
		if err := copyPathPreserve(src, dst, info); err != nil {
			return err
		}
	}
	report.ConflictsArchived++
	report.BytesCopied += fileSize(info)
	report.Operations = append(report.Operations, HomeMigrationOperation{Kind: "archive-conflict", Source: src, Destination: dst, Bytes: fileSize(info)})
	return nil
}

func copyPathPreserve(src, dst string, info fs.FileInfo) error {
	if info.IsDir() {
		return copyDirTreePreserve(src, dst)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		return os.Symlink(target, dst)
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	return copyRegularFilePreserve(src, dst, info)
}

func copyDirTreePreserve(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := dst
		if rel != "." {
			target = filepath.Join(dst, rel)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			return os.Symlink(link, target)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		return copyRegularFilePreserve(path, target, info)
	})
}

func copyRegularFilePreserve(src, dst string, info fs.FileInfo) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".reasonix-migrate-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		return err
	}
	ok = true
	_ = os.Chtimes(dst, info.ModTime(), info.ModTime())
	return nil
}

func sameFileContent(a, b string) (bool, error) {
	ai, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	bi, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	if !ai.Mode().IsRegular() || !bi.Mode().IsRegular() {
		return false, nil
	}
	if ai.Size() != bi.Size() {
		return false, nil
	}
	af, err := os.Open(a)
	if err != nil {
		return false, err
	}
	defer af.Close()
	bf, err := os.Open(b)
	if err != nil {
		return false, err
	}
	defer bf.Close()
	ab, err := io.ReadAll(af)
	if err != nil {
		return false, err
	}
	bb, err := io.ReadAll(bf)
	if err != nil {
		return false, err
	}
	return bytes.Equal(ab, bb), nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func fileSize(info fs.FileInfo) int64 {
	if info == nil || !info.Mode().IsRegular() {
		return 0
	}
	return info.Size()
}

func isSessionJSONLRel(rel string) bool {
	rel = filepath.ToSlash(filepath.Clean(rel))
	return strings.HasSuffix(rel, ".jsonl") && (strings.HasPrefix(rel, "sessions/") || strings.Contains(rel, "/sessions/"))
}

func isSessionSidecarRel(rel string) bool {
	rel = filepath.ToSlash(filepath.Clean(rel))
	if !(strings.HasSuffix(rel, "/.titles.json") || strings.HasSuffix(rel, "/.display.json")) {
		return false
	}
	dir := strings.TrimSuffix(rel, "/"+filepath.Base(rel))
	return dir == "sessions" || strings.HasSuffix(dir, "/sessions") || strings.Contains(dir, "/sessions/")
}

func isSessionCheckpointDirRel(rel string) bool {
	rel = filepath.ToSlash(filepath.Clean(rel))
	return strings.HasSuffix(rel, ".ckpt") && (strings.HasPrefix(rel, "sessions/") || strings.Contains(rel, "/sessions/"))
}

func uniqueMigratedSessionRel(root, rel, runID string) string {
	dir := filepath.Dir(rel)
	base := filepath.Base(rel)
	stem := strings.TrimSuffix(base, ".jsonl")
	for i := 0; ; i++ {
		suffix := ".migrated-" + runID
		if i > 0 {
			suffix = fmt.Sprintf("%s-%d", suffix, i)
		}
		candidate := filepath.Join(dir, stem+suffix+".jsonl")
		if !pathExists(filepath.Join(root, candidate)) {
			return candidate
		}
	}
}

func mappedSessionArtifactRel(rel string, renames map[string]string) string {
	slash := filepath.ToSlash(filepath.Clean(rel))
	if strings.HasSuffix(slash, ".jsonl.meta") {
		base := strings.TrimSuffix(slash, ".meta")
		if renamed := renames[base]; renamed != "" {
			return filepath.FromSlash(renamed + ".meta")
		}
	}
	if strings.HasSuffix(slash, ".ckpt") {
		base := strings.TrimSuffix(slash, ".ckpt") + ".jsonl"
		if renamed := renames[base]; renamed != "" {
			return filepath.FromSlash(strings.TrimSuffix(renamed, ".jsonl") + ".ckpt")
		}
	}
	for oldRel, newRel := range renames {
		oldDir := strings.TrimSuffix(oldRel, ".jsonl") + ".ckpt/"
		if strings.HasPrefix(slash, oldDir) {
			return filepath.FromSlash(strings.TrimSuffix(newRel, ".jsonl") + ".ckpt/" + strings.TrimPrefix(slash, oldDir))
		}
	}
	return ""
}

func mergeSessionSidecar(report *HomeMigrationReport, e homeMigrationEntry, renames map[string]string) error {
	dst := filepath.Join(report.To, e.rel)
	srcData, err := os.ReadFile(e.path)
	if err != nil {
		return err
	}
	dstData, err := os.ReadFile(dst)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if len(dstData) > 0 && bytes.Equal(srcData, dstData) {
		report.FilesSkipped++
		report.Operations = append(report.Operations, HomeMigrationOperation{Kind: "skip-sidecar", Source: e.path, Destination: dst, Note: "identical"})
		return nil
	}
	var merged []byte
	if strings.HasSuffix(filepath.ToSlash(e.rel), "/.titles.json") {
		merged, err = mergeTitleSidecar(e.rel, srcData, dstData, renames)
	} else {
		merged, err = mergeDisplaySidecar(e.rel, srcData, dstData, renames)
	}
	if err != nil {
		return err
	}
	if report.Apply {
		if len(dstData) > 0 {
			if err := backupHomeFile(report, e.path, filepath.ToSlash(e.rel)+".source"); err != nil {
				return err
			}
			if err := backupHomeFile(report, dst, filepath.ToSlash(e.rel)+".destination"); err != nil {
				return err
			}
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(dst, merged, 0o644); err != nil {
			return err
		}
	}
	report.FilesCopied++
	report.Operations = append(report.Operations, HomeMigrationOperation{Kind: "merge-session-sidecar", Source: e.path, Destination: dst})
	return nil
}

func mergeTitleSidecar(rel string, srcData, dstData []byte, renames map[string]string) ([]byte, error) {
	src := map[string]string{}
	if err := json.Unmarshal(srcData, &src); err != nil {
		return nil, err
	}
	dst := map[string]string{}
	if len(dstData) > 0 {
		if err := json.Unmarshal(dstData, &dst); err != nil {
			return nil, err
		}
	}
	dir := filepath.ToSlash(filepath.Dir(rel))
	merged := map[string]string{}
	for key, value := range src {
		merged[remapSessionSidecarKey(dir, key, renames)] = value
	}
	for key, value := range dst {
		merged[key] = value
	}
	return marshalSidecarJSON(merged)
}

func mergeDisplaySidecar(rel string, srcData, dstData []byte, renames map[string]string) ([]byte, error) {
	src := map[string]map[string]string{}
	if err := json.Unmarshal(srcData, &src); err != nil {
		return nil, err
	}
	dst := map[string]map[string]string{}
	if len(dstData) > 0 {
		if err := json.Unmarshal(dstData, &dst); err != nil {
			return nil, err
		}
	}
	dir := filepath.ToSlash(filepath.Dir(rel))
	merged := map[string]map[string]string{}
	for key, value := range src {
		merged[remapSessionSidecarKey(dir, key, renames)] = value
	}
	for key, value := range dst {
		merged[key] = value
	}
	return marshalSidecarJSON(merged)
}

func remapSessionSidecarKey(dir, key string, renames map[string]string) string {
	oldRel := filepath.ToSlash(filepath.Join(filepath.FromSlash(dir), key))
	if renamed := renames[oldRel]; renamed != "" {
		return filepath.Base(filepath.FromSlash(renamed))
	}
	return key
}

func marshalSidecarJSON(v any) ([]byte, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func samePath(a, b string) bool {
	a = cleanUserRoot(a)
	b = cleanUserRoot(b)
	if a == "" || b == "" {
		return a == b
	}
	if aa, err := filepath.Abs(a); err == nil {
		a = aa
	}
	if bb, err := filepath.Abs(b); err == nil {
		b = bb
	}
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func nestedPath(parent, child string) bool {
	parent = cleanUserRoot(parent)
	child = cleanUserRoot(child)
	if parent == "" || child == "" || samePath(parent, child) {
		return false
	}
	parentAbs, err := filepath.Abs(parent)
	if err != nil {
		parentAbs = parent
	}
	childAbs, err := filepath.Abs(child)
	if err != nil {
		childAbs = child
	}
	parentAbs = filepath.Clean(parentAbs)
	childAbs = filepath.Clean(childAbs)
	if runtime.GOOS == "windows" {
		parentAbs = strings.ToLower(parentAbs)
		childAbs = strings.ToLower(childAbs)
	}
	rel, err := filepath.Rel(parentAbs, childAbs)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
