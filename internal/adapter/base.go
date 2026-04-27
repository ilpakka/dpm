package adapter

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"dpm.fi/dpm/internal/catalog"
)

const (
	dpmBlockStart = "# ===== DPM"

	dpmBlockMarker = "# ===== DPM Configuration"

	dpmBlockEnd = "# ===== End DPM Configuration ====="

	tempDirPrefix = "dpm-install-"

	// maxExtractFileSize is the maximum size we allow for a single file extracted
	// from an archive. This guards against zip/tar bombs.
	maxExtractFileSize = 512 * 1024 * 1024 // 512 MiB
)

type BaseAdapter struct {
	dpmRoot  string
	platform catalog.Platform
}

func (b *BaseAdapter) Platform() catalog.Platform {
	return b.platform
}

func (b *BaseAdapter) GetDPMRoot() string {
	return b.dpmRoot
}

func (b *BaseAdapter) GetTempDir(toolID string) (string, error) {
	dir, err := os.MkdirTemp("", tempDirPrefix+toolID+"-")
	if err != nil {
		return "", fmt.Errorf("adapter: GetTempDir: %w", err)
	}
	return dir, nil
}

func (b *BaseAdapter) CleanStaleTempDirs(maxAge time.Duration) error {
	tmp := os.TempDir()
	entries, err := os.ReadDir(tmp)
	if err != nil {
		return fmt.Errorf("adapter: CleanStaleTempDirs: read temp dir: %w", err)
	}

	var errs []error
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), tempDirPrefix) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if time.Since(info.ModTime()) > maxAge {
			if rmErr := os.RemoveAll(filepath.Join(tmp, entry.Name())); rmErr != nil {
				errs = append(errs, rmErr)
			}
		}
	}
	return errors.Join(errs...)
}

func (b *BaseAdapter) IsInPATH(dir string) bool {
	sep := string(os.PathListSeparator)
	for _, p := range strings.Split(os.Getenv("PATH"), sep) {
		if p == dir {
			return true
		}
	}
	// Also check if we already wrote the PATH block to the shell rc file,
	// since the current process won't see it until the shell is restarted.
	// Use dpmBlockMarker (the full header line) rather than dpmBlockStart so
	// that a user comment containing "# ===== DPM" doesn't produce a false positive.
	rcFile := b.shellRCFile()
	if rcFile != "" {
		if existing, err := os.ReadFile(rcFile); err == nil {
			if strings.Contains(string(existing), dpmBlockMarker) {
				return true
			}
		}
	}
	return false
}

func (b *BaseAdapter) BackupFile(path string) (string, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return "", nil
	}

	const maxBackupRetries = 1000
	base := path + ".dpm-backup-" + time.Now().Format("20060102")
	dest := base
	for i := 1; ; i++ {
		if _, err := os.Stat(dest); os.IsNotExist(err) {
			break
		}
		if i >= maxBackupRetries {
			return "", fmt.Errorf("adapter: BackupFile: too many backup collisions for %q", path)
		}
		dest = fmt.Sprintf("%s-%d", base, i)
	}

	if err := copyFile(path, dest); err != nil {
		return "", fmt.Errorf("adapter: BackupFile: %w", err)
	}
	return dest, nil
}

func (b *BaseAdapter) CreateDataDirs(dirs []string) error {
	for _, dir := range dirs {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			if err := os.MkdirAll(dir, 0700); err != nil {
				return fmt.Errorf("adapter: CreateDataDirs: %q: %w", dir, err)
			}
		}
	}
	return nil
}

func (b *BaseAdapter) ApplyDotfile(spec DotfileSpec, opts InstallOptions) (ApplyResult, error) {
	if opts.DryRun {
		log.Printf("[dry-run] ApplyDotfile: %s → %s (strategy: %s)",
			spec.SourcePath, spec.TargetPath, spec.MergeStrategy)
		return ApplyResult{Spec: spec}, nil
	}

	switch spec.MergeStrategy {
	case MergeBackup, "":
		return b.applyBackup(spec)
	case MergeAppend:
		return b.applyAppend(spec)
	case MergeSkip:
		return b.applySkip(spec)
	case MergeForce:
		return b.applyForce(spec, opts.ConfirmFunc)
	default:
		err := fmt.Errorf("adapter: ApplyDotfile: unknown strategy %q", spec.MergeStrategy)
		return ApplyResult{Spec: spec, Err: err}, err
	}
}

func (b *BaseAdapter) ApplyDotfiles(specs []DotfileSpec, opts InstallOptions) ([]ApplyResult, error) {
	results := make([]ApplyResult, 0, len(specs))
	for _, spec := range specs {
		r, _ := b.ApplyDotfile(spec, opts)
		results = append(results, r)
	}
	return results, nil
}

func (b *BaseAdapter) RunScript(scriptPath string, env []string) error {
	shell, err := exec.LookPath("bash")
	if err != nil {
		shell, err = exec.LookPath("sh")
		if err != nil {
			return fmt.Errorf("adapter: RunScript: no shell found: %w", err)
		}
	}
	cmd := exec.Command(shell, scriptPath)
	cmd.Dir = filepath.Dir(scriptPath)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("adapter: RunScript: %q failed: %w\n%s", scriptPath, err, out)
	}
	return nil
}

func (b *BaseAdapter) CreateSymlink(target, linkPath string) error {
	info, err := os.Lstat(linkPath)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			existing, _ := os.Readlink(linkPath)
			if existing == target {
				return nil
			}
			if err := os.Remove(linkPath); err != nil {
				return fmt.Errorf("adapter: CreateSymlink: remove stale symlink: %w", err)
			}
		} else {
			return fmt.Errorf("adapter: CreateSymlink: %q exists and is not a symlink", linkPath)
		}
	}
	if err := ensureParentDir(linkPath); err != nil {
		return fmt.Errorf("adapter: CreateSymlink: ensure parent dir: %w", err)
	}
	if err := os.Symlink(target, linkPath); err != nil {
		return fmt.Errorf("adapter: CreateSymlink: %w", err)
	}
	return nil
}

func (b *BaseAdapter) AddToPATH(dir string) error {
	rcFile := b.shellRCFile()

	if existing, err := os.ReadFile(rcFile); err == nil {
		// Use dpmBlockMarker (the full header line) rather than the broad
		// dpmBlockStart so that a user comment containing "# ===== DPM" does
		// not falsely suppress writing the real PATH export block.
		if strings.Contains(string(existing), dpmBlockMarker) {
			return nil
		}
	}

	if err := ensureParentDir(rcFile); err != nil {
		return fmt.Errorf("adapter: AddToPATH: ensure parent dir: %w", err)
	}
	f, err := os.OpenFile(rcFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("adapter: AddToPATH: open %q: %w", rcFile, err)
	}
	defer f.Close()

	// Escape any single quotes in the directory path so the generated shell
	// line remains syntactically valid regardless of what the path contains.
	safeDir := strings.ReplaceAll(dir, "'", "'\\''")
	var exportLine string
	if strings.HasSuffix(rcFile, "dpm.fish") {
		exportLine = fmt.Sprintf("set -x PATH '%s' $PATH", safeDir)
	} else {
		exportLine = fmt.Sprintf("export PATH='%s':$PATH", safeDir)
	}
	block := fmt.Sprintf("\n%s (Added %s) =====\n%s\n%s\n",
		dpmBlockMarker,
		time.Now().Format("2006-01-02"),
		exportLine,
		dpmBlockEnd,
	)
	if _, err := f.WriteString(block); err != nil {
		return fmt.Errorf("adapter: AddToPATH: write: %w", err)
	}
	return f.Sync()
}

func (b *BaseAdapter) shellRCFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Printf("adapter: shellRCFile: could not determine home directory: %v", err)
		return ""
	}
	switch filepath.Base(os.Getenv("SHELL")) {
	case "zsh":
		return filepath.Join(home, ".zshrc")
	case "fish":
		return filepath.Join(home, ".config", "fish", "conf.d", "dpm.fish")
	case "bash":
		return filepath.Join(home, ".bashrc")
	default:
		return filepath.Join(home, ".profile")
	}
}

func dryRunInstallResult(bundle Bundle, finalDir, dpmBin string) *InstallResult {
	symlinks := make([]string, len(bundle.Binaries))
	for i, bin := range bundle.Binaries {
		src := filepath.Join(finalDir, "bin", bin)
		dst := filepath.Join(dpmBin, bin)
		symlinks[i] = dst
		log.Printf("[dry-run] InstallBundle: symlink %s → %s", src, dst)
	}
	log.Printf("[dry-run] InstallBundle: extract %q → %s", bundle.ArchivePath, finalDir)
	if bundle.InstallScript != "" {
		log.Printf("[dry-run] InstallBundle: run script %s", filepath.Join(finalDir, bundle.InstallScript))
	}
	for _, spec := range bundle.Dotfiles {
		log.Printf("[dry-run] InstallBundle: dotfile %s → %s (%s)", spec.SourcePath, spec.TargetPath, spec.MergeStrategy)
	}

	return &InstallResult{
		ToolID:     bundle.ToolID,
		Version:    bundle.Version,
		InstallDir: finalDir,
		Symlinks:   symlinks,
	}
}

func prepareStagingDir(finalDir string) (stagingDir string, cleanup func(), err error) {
	toolVersionParent := filepath.Dir(finalDir)
	if err := os.MkdirAll(toolVersionParent, 0700); err != nil {
		return "", nil, fmt.Errorf("adapter: InstallBundle: create tools dir: %w", err)
	}

	stagingDir, err = os.MkdirTemp(toolVersionParent, ".dpm-staging-")
	if err != nil {
		return "", nil, fmt.Errorf("adapter: InstallBundle: create staging dir: %w", err)
	}
	cleanup = func() {
		_ = os.RemoveAll(stagingDir)
	}
	return stagingDir, cleanup, nil
}

func promoteStagingDir(stagingDir, finalDir string) error {
	if _, err := os.Stat(finalDir); err == nil {
		if err := os.RemoveAll(finalDir); err != nil {
			return fmt.Errorf("adapter: InstallBundle: remove existing install: %w", err)
		}
	}
	if err := os.Rename(stagingDir, finalDir); err != nil {
		return fmt.Errorf("adapter: InstallBundle: rename staging to final: %w", err)
	}
	return nil
}

func resolveBinDir(bundle Bundle) string {
	if bundle.BinSubdir != "" {
		return bundle.BinSubdir
	}
	return "bin"
}

func (b *BaseAdapter) createBundleSymlinks(bundle Bundle, finalDir, dpmBin string) ([]string, error) {
	binDir := resolveBinDir(bundle)
	symlinks := make([]string, 0, len(bundle.Binaries))
	for _, bin := range bundle.Binaries {
		target := filepath.Join(finalDir, binDir, bin)
		linkPath := filepath.Join(dpmBin, bin)
		if err := b.CreateSymlink(target, linkPath); err != nil {
			return nil, fmt.Errorf("adapter: InstallBundle: symlink %q: %w", bin, err)
		}
		symlinks = append(symlinks, linkPath)
	}
	return symlinks, nil
}

func (b *BaseAdapter) maybeRunInstallScript(bundle Bundle, finalDir string, opts InstallOptions) error {
	if bundle.InstallScript == "" {
		return nil
	}
	scriptPath := filepath.Join(finalDir, bundle.InstallScript)
	// Require explicit user confirmation before executing any script from an archive.
	// If no confirm function is provided we refuse rather than silently executing.
	prompt := fmt.Sprintf("Tool %q includes an install script (%s).\nAllow it to run?", bundle.ToolName, scriptPath)
	if opts.ConfirmFunc == nil || !opts.ConfirmFunc(prompt) {
		return fmt.Errorf("adapter: install script for %q was not confirmed — installation aborted", bundle.ToolName)
	}
	if err := b.RunScript(scriptPath, nil); err != nil {
		return fmt.Errorf("adapter: InstallBundle: install script: %w", err)
	}
	return nil
}

func (b *BaseAdapter) maybeAddPath(dir string) {
	if b.IsInPATH(dir) {
		return
	}
	if err := b.AddToPATH(dir); err != nil {
		log.Printf("adapter: InstallBundle: AddToPATH warning: %v", err)
	}
}

func (b *BaseAdapter) installBundle(bundle Bundle, opts InstallOptions, extractFn func(string, string) error) (*InstallResult, error) {
	dpmBin := filepath.Join(b.dpmRoot, "bin")
	finalDir := filepath.Join(b.dpmRoot, "tools", bundle.ToolID, bundle.Version)

	if opts.DryRun {
		return dryRunInstallResult(bundle, finalDir, dpmBin), nil
	}

	stagingDir, cleanup, err := prepareStagingDir(finalDir)
	if err != nil {
		return nil, err
	}
	cleanupNeeded := true
	defer func() {
		if cleanupNeeded {
			cleanup()
		}
	}()

	if err := extractFn(bundle.ArchivePath, stagingDir); err != nil {
		return nil, fmt.Errorf("adapter: InstallBundle: extract: %w", err)
	}

	if err := promoteStagingDir(stagingDir, finalDir); err != nil {
		return nil, err
	}
	cleanupNeeded = false

	if err := os.MkdirAll(dpmBin, 0700); err != nil {
		return nil, fmt.Errorf("adapter: InstallBundle: create bin dir: %w", err)
	}

	symlinks, err := b.createBundleSymlinks(bundle, finalDir, dpmBin)
	if err != nil {
		return nil, err
	}

	if err := b.maybeRunInstallScript(bundle, finalDir, opts); err != nil {
		// Rollback: remove symlinks and the promoted install directory so DPM
		// does not leave untracked executables behind on a failed install.
		for _, link := range symlinks {
			_ = os.Remove(link)
		}
		_ = os.RemoveAll(finalDir)
		return nil, err
	}

	dotfileResults, _ := b.ApplyDotfiles(bundle.Dotfiles, opts)

	if err := b.CreateDataDirs(bundle.DataDirs); err != nil {
		return nil, fmt.Errorf("adapter: InstallBundle: data dirs: %w", err)
	}

	b.maybeAddPath(dpmBin)

	return &InstallResult{
		ToolID:         bundle.ToolID,
		Version:        bundle.Version,
		InstallDir:     finalDir,
		Symlinks:       symlinks,
		DotfileResults: dotfileResults,
	}, nil
}

func (b *BaseAdapter) applyBackup(spec DotfileSpec) (ApplyResult, error) {
	r := ApplyResult{Spec: spec}

	backup, err := b.BackupFile(spec.TargetPath)
	if err != nil {
		r.Err = err
		return r, err
	}
	r.BackupPath = backup

	if err := ensureParentDir(spec.TargetPath); err != nil {
		r.Err = err
		return r, err
	}
	if err := copyFile(spec.SourcePath, spec.TargetPath); err != nil {
		r.Err = fmt.Errorf("adapter: applyBackup: %w", err)
		return r, r.Err
	}
	r.Applied = true
	return r, nil
}

func (b *BaseAdapter) applyAppend(spec DotfileSpec) (ApplyResult, error) {
	r := ApplyResult{Spec: spec}

	addition, err := os.ReadFile(spec.SourcePath)
	if err != nil {
		r.Err = fmt.Errorf("adapter: applyAppend: read source: %w", err)
		return r, r.Err
	}

	if existing, err := os.ReadFile(spec.TargetPath); err == nil {
		// Use dpmBlockMarker (the full header line) rather than the broad
		// dpmBlockStart so that a user comment containing "# ===== DPM" does
		// not falsely suppress a legitimate append.
		if strings.Contains(string(existing), dpmBlockMarker) {
			r.Skipped = true
			return r, nil
		}
	}

	if err := ensureParentDir(spec.TargetPath); err != nil {
		r.Err = err
		return r, err
	}

	f, err := os.OpenFile(spec.TargetPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		r.Err = fmt.Errorf("adapter: applyAppend: open target: %w", err)
		return r, r.Err
	}
	defer f.Close()

	block := fmt.Sprintf("\n%s (Added %s) =====\n%s\n%s\n",
		dpmBlockMarker,
		time.Now().Format("2006-01-02"),
		strings.TrimRight(string(addition), "\n"),
		dpmBlockEnd,
	)
	if _, err := f.WriteString(block); err != nil {
		r.Err = fmt.Errorf("adapter: applyAppend: write: %w", err)
		return r, r.Err
	}
	if err := f.Sync(); err != nil {
		r.Err = fmt.Errorf("adapter: applyAppend: sync: %w", err)
		return r, r.Err
	}
	r.Applied = true
	return r, nil
}

func (b *BaseAdapter) applySkip(spec DotfileSpec) (ApplyResult, error) {
	r := ApplyResult{Spec: spec}

	if _, err := os.Stat(spec.TargetPath); err == nil {
		r.Skipped = true
		return r, nil
	}

	if err := ensureParentDir(spec.TargetPath); err != nil {
		r.Err = err
		return r, err
	}
	if err := copyFile(spec.SourcePath, spec.TargetPath); err != nil {
		r.Err = fmt.Errorf("adapter: applySkip: %w", err)
		return r, r.Err
	}
	r.Applied = true
	return r, nil
}

func (b *BaseAdapter) applyForce(spec DotfileSpec, confirm func(string) bool) (ApplyResult, error) {
	r := ApplyResult{Spec: spec}

	if _, err := os.Stat(spec.TargetPath); err == nil {

		prompt := fmt.Sprintf("Overwrite %s?", spec.TargetPath)
		if confirm == nil || !confirm(prompt) {
			r.Skipped = true
			return r, nil
		}
	}

	if err := ensureParentDir(spec.TargetPath); err != nil {
		r.Err = err
		return r, err
	}
	if err := copyFile(spec.SourcePath, spec.TargetPath); err != nil {
		r.Err = fmt.Errorf("adapter: applyForce: %w", err)
		return r, r.Err
	}
	r.Applied = true
	return r, nil
}

func extractTarGz(archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("adapter: extractTarGz: open: %w", err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("adapter: extractTarGz: gzip reader: %w", err)
	}
	defer gz.Close()

	return extractTar(tar.NewReader(gz), destDir)
}

func extractTar(tr *tar.Reader, destDir string) error {
	destClean := filepath.Clean(destDir)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("adapter: extractTar: read header: %w", err)
		}

		if err := extractTarEntry(tr, header, destClean); err != nil {
			return err
		}
	}
	return nil
}

func extractTarEntry(tr *tar.Reader, header *tar.Header, destClean string) error {
	target, err := safeJoin(destClean, header.Name)
	if err != nil {
		return fmt.Errorf("adapter: extractTar: %w", err)
	}

	switch header.Typeflag {
	case tar.TypeDir:
		if err := os.MkdirAll(target, os.FileMode(header.Mode)|0700); err != nil {
			return fmt.Errorf("adapter: extractTar: mkdir %q: %w", target, err)
		}
		return nil
	case tar.TypeReg:
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return fmt.Errorf("adapter: extractTar: mkdir parent: %w", err)
		}

		if err := writeFile(target, tr, os.FileMode(header.Mode)&0777); err != nil {
			return fmt.Errorf("adapter: extractTar: write %q: %w", target, err)
		}
		return nil
	case tar.TypeSymlink:
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return fmt.Errorf("adapter: extractTar: mkdir parent: %w", err)
		}
		if err := validateTarSymlinkTarget(destClean, target, header.Linkname); err != nil {
			return err
		}
		if err := os.Symlink(header.Linkname, target); err != nil && !os.IsExist(err) {
			return fmt.Errorf("adapter: extractTar: symlink %q: %w", target, err)
		}
		return nil
	default:
		return nil
	}
}

func validateTarSymlinkTarget(destClean, target, linkname string) error {
	if filepath.IsAbs(linkname) {
		return fmt.Errorf("adapter: extractTar: absolute symlink target %q rejected", linkname)
	}
	resolved := filepath.Clean(filepath.Join(filepath.Dir(target), linkname))
	if resolved != destClean && !strings.HasPrefix(resolved, destClean+string(os.PathSeparator)) {
		return fmt.Errorf("adapter: extractTar: symlink target %q escapes extraction directory", linkname)
	}
	return nil
}

func extractZip(archivePath, destDir string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("adapter: extractZip: open: %w", err)
	}
	defer r.Close()

	destClean := filepath.Clean(destDir)

	for _, f := range r.File {
		target, err := safeJoin(destClean, f.Name)
		if err != nil {
			return fmt.Errorf("adapter: extractZip: %w", err)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0755); err != nil {
				return fmt.Errorf("adapter: extractZip: mkdir %q: %w", target, err)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return fmt.Errorf("adapter: extractZip: mkdir parent: %w", err)
		}

		src, err := f.Open()
		if err != nil {
			return fmt.Errorf("adapter: extractZip: open zip entry %q: %w", f.Name, err)
		}

		writeErr := writeFile(target, src, f.Mode()&0777)
		src.Close()
		if writeErr != nil {
			return fmt.Errorf("adapter: extractZip: write %q: %w", target, writeErr)
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("copyFile: open src %q: %w", src, err)
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("copyFile: create dst %q: %w", dst, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return fmt.Errorf("copyFile: copy %q → %q: %w", src, dst, err)
	}
	return out.Sync()
}

func writeFile(path string, r io.Reader, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("writeFile: create %q: %w", path, err)
	}
	defer f.Close()
	// Limit extraction size per file to guard against zip/tar bombs.
	// LimitReader returns EOF after maxExtractFileSize+1 bytes, so if we read
	// exactly that many bytes we know the real file is larger than the limit.
	limited := io.LimitReader(r, maxExtractFileSize+1)
	n, err := io.Copy(f, limited)
	if err != nil {
		return fmt.Errorf("writeFile: write %q: %w", path, err)
	}
	if n > maxExtractFileSize {
		_ = f.Close()
		_ = os.Remove(path)
		return fmt.Errorf("writeFile: %q exceeds maximum allowed size of %d bytes", path, maxExtractFileSize)
	}
	return f.Sync()
}

func ensureParentDir(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("adapter: ensureParentDir %q: %w", dir, err)
	}
	return nil
}

func safeJoin(base, name string) (string, error) {
	target := filepath.Clean(filepath.Join(base, name))
	if target != base && !strings.HasPrefix(target, base+string(os.PathSeparator)) {
		return "", fmt.Errorf("path traversal attempt in archive: %q", name)
	}
	return target, nil
}
