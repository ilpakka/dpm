package engine

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"dpm.fi/dpm/internal/adapter"
	"dpm.fi/dpm/internal/dotfiles"
)

// Doctor runs the same set of checks as the `dpm doctor` CLI command and
// returns them as a structured report. The TUI presents this report in a
// dedicated view.
func (e *Engine) Doctor() (*DoctorReport, error) {
	report := &DoctorReport{
		Platform: string(e.Platform()),
		InPATH:   e.IsInPATH(),
		DPMRoot:  e.GetDPMRoot(),
	}

	// 1. ~/.dpm/bin in PATH.
	if report.InPATH {
		report.Checks = append(report.Checks, DoctorCheck{
			Name:     "PATH",
			OK:       true,
			Severity: "info",
			Message:  "~/.dpm/bin is in PATH",
		})
	} else {
		report.Checks = append(report.Checks, DoctorCheck{
			Name:     "PATH",
			OK:       false,
			Severity: "warn",
			Message:  "~/.dpm/bin is NOT in PATH — run 'dpm install <tool>' to add it automatically",
		})
	}

	// 2. Metadata integrity.
	records, err := e.InstalledRecords()
	if err != nil {
		report.Checks = append(report.Checks, DoctorCheck{
			Name:     "metadata",
			OK:       false,
			Severity: "error",
			Message:  fmt.Sprintf("could not load metadata: %v", err),
		})
	} else {
		orphans := 0
		for _, r := range records {
			if r.InstallDir != "" {
				if _, statErr := os.Stat(r.InstallDir); os.IsNotExist(statErr) {
					orphans++
				}
			}
		}
		if orphans == 0 {
			report.Checks = append(report.Checks, DoctorCheck{
				Name:     "metadata",
				OK:       true,
				Severity: "info",
				Message:  fmt.Sprintf("metadata OK (%d tool(s) installed)", len(records)),
			})
		} else {
			report.Checks = append(report.Checks, DoctorCheck{
				Name:     "metadata",
				OK:       false,
				Severity: "warn",
				Message:  fmt.Sprintf("metadata has %d orphaned record(s) — install dirs missing", orphans),
			})
		}
	}

	// 3. Cache size.
	cacheDir := filepath.Join(report.DPMRoot, "cache")
	cacheSize, _ := dirSize(cacheDir)
	report.Checks = append(report.Checks, DoctorCheck{
		Name:     "cache",
		OK:       true,
		Severity: "info",
		Message:  fmt.Sprintf("%s (%s)", cacheDir, formatBytes(cacheSize)),
	})

	// 4. Git available.
	if _, gitErr := exec.LookPath("git"); gitErr == nil {
		report.Checks = append(report.Checks, DoctorCheck{
			Name:     "git",
			OK:       true,
			Severity: "info",
			Message:  "git is available",
		})
	} else {
		report.Checks = append(report.Checks, DoctorCheck{
			Name:     "git",
			OK:       false,
			Severity: "warn",
			Message:  "git not found in PATH (required for dotfiles)",
		})
	}

	return report, nil
}

// BinaryPath returns the absolute path the tool's symlink resolves to,
// e.g. ~/.dpm/bin/<id> → ~/.dpm/tools/<id>/<ver>/<id>.
// Returns an error if the tool is not installed.
func (e *Engine) BinaryPath(toolID string) (string, error) {
	linkPath := filepath.Join(e.adapter.GetDPMRoot(), "bin", toolID)
	target, err := os.Readlink(linkPath)
	if err != nil {
		return "", fmt.Errorf("engine: binary path for %s: %w", toolID, err)
	}
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(linkPath), target)
	}
	return filepath.Clean(target), nil
}

// BubbleStart creates a fresh bubble root under /tmp and returns the
// information needed for the caller to spawn a shell into it.
// The caller is responsible for spawning the shell — this method only
// prepares the directory layout (via NewBubble).
func (e *Engine) BubbleStart() (*BubbleSession, error) {
	bubbleID := fmt.Sprintf("%d", os.Getpid())
	bubbleRoot := filepath.Join(os.TempDir(), "dpm-bubble-"+bubbleID)

	// Initialise the bubble layout (creates dirs, metadata, settings).
	// We discard the engine — the caller's main engine instance keeps serving
	// RPCs against the user's normal DPM root.
	if _, err := NewBubble(bubbleRoot); err != nil {
		return nil, fmt.Errorf("engine: bubble start: %w", err)
	}

	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}

	env := map[string]string{
		"DPM_HOME":  bubbleRoot,
		"HOME":      bubbleRoot,
		"PATH":      filepath.Join(bubbleRoot, "bin") + string(os.PathListSeparator) + os.Getenv("PATH"),
		"PS1":       "(dpm-bubble) $ ",
		"DPM_BUBBLE": "1",
	}

	return &BubbleSession{
		RootPath: bubbleRoot,
		Shell:    shell,
		Env:      env,
	}, nil
}

// BubbleStop removes a bubble root previously created by BubbleStart.
// It refuses to remove anything outside /tmp/dpm-bubble-* as a safety guard.
func (e *Engine) BubbleStop(rootPath string) error {
	clean := filepath.Clean(rootPath)
	tmpPrefix := filepath.Join(os.TempDir(), "dpm-bubble-")
	if !strings.HasPrefix(clean, tmpPrefix) {
		return fmt.Errorf("engine: bubble stop: refusing to remove %q (not under %s)", clean, tmpPrefix)
	}
	if err := os.RemoveAll(clean); err != nil {
		return fmt.Errorf("engine: bubble stop: remove %s: %w", clean, err)
	}
	// Reset DPM_HOME so subsequent operations use the user's normal root.
	_ = os.Unsetenv("DPM_HOME")
	return nil
}

// ScanGitDotfiles clones the given repo URL via the existing dotfile pipeline
// (using a synthetic Dotfile entry) and runs DetectConfigs on the result.
func (e *Engine) ScanGitDotfiles(repoURL string) (*DotfileScanResult, error) {
	if strings.TrimSpace(repoURL) == "" {
		return nil, fmt.Errorf("engine: scan git dotfiles: repo URL is required")
	}

	// Use a unique synthetic ID so concurrent scans don't collide.
	df := dotfiles.Dotfile{
		ID:         fmt.Sprintf("import-scan-%d", os.Getpid()),
		SourceRepo: repoURL,
	}
	result, err := e.InstallDotfile(df)
	if err != nil {
		return nil, fmt.Errorf("engine: scan git dotfiles: clone: %w", err)
	}
	if result.ClonedTo == "" {
		return nil, fmt.Errorf("engine: scan git dotfiles: clone returned no path")
	}

	detected := dotfiles.DetectConfigs(result.ClonedTo)
	scanned := make([]ScannedDotfileConfig, 0, len(detected))
	for _, d := range detected {
		scanned = append(scanned, ScannedDotfileConfig{
			Name:          d.Name,
			Source:        d.Source,
			Target:        d.Target,
			MergeStrategy: d.MergeStrategy,
			IsScript:      d.IsScript,
		})
	}

	return &DotfileScanResult{
		RepoDir: result.ClonedTo,
		Configs: scanned,
	}, nil
}

// ApplyImportedDotfiles applies a previously scanned dotfile selection.
// `repoDir` must be a directory previously returned by ScanGitDotfiles.
// Each config is either copied to its target path or executed as a script.
func (e *Engine) ApplyImportedDotfiles(repoDir string, configs []ScannedDotfileConfig) (*dotfiles.ApplyResult, error) {
	if strings.TrimSpace(repoDir) == "" {
		return nil, fmt.Errorf("engine: apply imported dotfiles: repo dir is required")
	}
	if _, err := os.Stat(repoDir); err != nil {
		return nil, fmt.Errorf("engine: apply imported dotfiles: repo dir %s: %w", repoDir, err)
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("engine: apply imported dotfiles: get home dir: %w", err)
	}

	result := &dotfiles.ApplyResult{
		DotfileID: "imported",
		ClonedTo:  repoDir,
	}

	for _, cfg := range configs {
		if cfg.IsScript {
			scriptPath := filepath.Join(repoDir, cfg.Source)
			cmd := exec.Command("sh", scriptPath)
			cmd.Dir = repoDir
			var scriptOut strings.Builder
			cmd.Stdout = &scriptOut
			cmd.Stderr = &scriptOut
			if runErr := cmd.Run(); runErr != nil {
				e.logger.Printf("script %s failed: %v\n%s", cfg.Source, runErr, scriptOut.String())
				return nil, fmt.Errorf("engine: apply imported dotfiles: script %s: %w", cfg.Source, runErr)
			}
			if scriptOut.Len() > 0 {
				e.logger.Printf("script %s: %s", cfg.Source, scriptOut.String())
			}
			result.Applied = append(result.Applied, cfg.Source)
			continue
		}
		sourcePath := filepath.Join(repoDir, cfg.Source)
		targetPath := cfg.Target
		if !filepath.IsAbs(targetPath) {
			targetPath = filepath.Join(homeDir, targetPath)
		}

		// Use the adapter's dotfile pipeline so backups + merges follow the
		// same rules as native dotfile installs.
		spec := adapter.DotfileSpec{
			SourcePath:    sourcePath,
			TargetPath:    targetPath,
			MergeStrategy: parseMergeStrategy(cfg.MergeStrategy),
		}
		applyResults, applyErr := e.adapter.ApplyDotfiles([]adapter.DotfileSpec{spec}, adapter.InstallOptions{})
		if applyErr != nil {
			return nil, fmt.Errorf("engine: apply imported dotfiles: %s: %w", cfg.Source, applyErr)
		}
		for _, r := range applyResults {
			if r.Applied {
				result.Applied = append(result.Applied, r.Spec.TargetPath)
			}
		}
	}

	e.logger.Printf("imported dotfiles: %d files applied from %s", len(result.Applied), repoDir)
	return result, nil
}

// parseMergeStrategy mirrors the helper in package dotfiles. We re-implement
// it here to avoid an import cycle and keep this file self-contained.
func parseMergeStrategy(s string) adapter.MergeStrategy {
	switch strings.ToLower(s) {
	case "append":
		return adapter.MergeAppend
	case "skip":
		return adapter.MergeSkip
	case "force":
		return adapter.MergeForce
	default:
		return adapter.MergeBackup
	}
}

// dirSize walks dir and returns the total byte count.
func dirSize(dir string) (int64, error) {
	var total int64
	err := filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		total += info.Size()
		return nil
	})
	return total, err
}

// formatBytes formats a byte count as a human-readable string.
func formatBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	if n < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
}
