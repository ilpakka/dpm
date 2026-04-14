package dotfiles

import (
	"bytes"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"dpm.iskff.fi/dpm/internal/adapter"
)

// ApplyResult tracks what happened when installing a dotfile set.
type ApplyResult struct {
	DotfileID string   `json:"dotfile_id"`
	ClonedTo  string   `json:"cloned_to,omitempty"` // path where repo was cloned
	Applied   []string `json:"applied,omitempty"`   // files that were applied
	Err       error    `json:"error,omitempty"`
}

// ApplyOptions controls dotfile installation behavior.
type ApplyOptions struct {
	DPMRoot string           // ~/.dpm or /tmp/dpm-bubble-<id>
	HomeDir string           // user's home directory for target paths
	Adapter adapter.IAdapter // for calling ApplyDotfile
}

// Apply clones the dotfile's source repo (if needed) and applies all file mappings
// using the adapter's dotfile pipeline.
func Apply(df Dotfile, opts ApplyOptions) (*ApplyResult, error) {
	result := &ApplyResult{DotfileID: df.ID}

	// Determine where source files live.
	sourceDir, cleanup, err := resolveSource(df, opts.DPMRoot)
	if err != nil {
		return nil, fmt.Errorf("dotfiles: resolve source for %s: %w", df.ID, err)
	}
	if cleanup != nil {
		// Don't clean up — we keep cloned repos in ~/.dpm/dotfiles/<id>/
		_ = cleanup
	}
	result.ClonedTo = sourceDir

	// Build DotfileSpecs from mappings or legacy Files list.
	specs := buildSpecs(df, sourceDir, opts.HomeDir)
	if len(specs) == 0 {
		return result, nil
	}

	// Apply through the adapter.
	results, err := opts.Adapter.ApplyDotfiles(specs, adapter.InstallOptions{})
	if err != nil {
		return nil, fmt.Errorf("dotfiles: apply %s: %w", df.ID, err)
	}

	for _, r := range results {
		if r.Applied {
			result.Applied = append(result.Applied, r.Spec.TargetPath)
		}
		if r.Err != nil && result.Err == nil {
			result.Err = r.Err
		}
	}

	return result, nil
}

// resolveSource ensures dotfile source files are available locally.
// For git repos, clones into ~/.dpm/dotfiles/<id>/. For local dirs, returns as-is.
func resolveSource(df Dotfile, dpmRoot string) (string, func(), error) {
	// Local source directory — use directly.
	if df.SourceDir != "" {
		if _, err := os.Stat(df.SourceDir); err != nil {
			return "", nil, fmt.Errorf("source dir %s: %w", df.SourceDir, err)
		}
		return df.SourceDir, nil, nil
	}

	// Git repo — clone or pull.
	if df.SourceRepo != "" {
		destDir := filepath.Join(dpmRoot, "dotfiles", df.ID)

		// If already cloned, do a pull.
		if isGitRepo(destDir) {
			cmd := exec.Command("git", "-C", destDir, "pull", "--ff-only")
			var buf bytes.Buffer
			cmd.Stdout = &buf
			cmd.Stderr = &buf
			if err := cmd.Run(); err != nil {
				log.Printf("dotfiles: git pull %s: %v (%s)", destDir, err, buf.String())
			}
			return destDir, nil, nil
		}

		// Clone fresh.
		repoURL, err := normalizeRepoURL(df.SourceRepo)
		if err != nil {
			return "", nil, fmt.Errorf("invalid dotfile repo URL: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(destDir), 0o755); err != nil {
			return "", nil, fmt.Errorf("create dotfiles dir: %w", err)
		}

		cmd := exec.Command("git", "clone", "--depth", "1", repoURL, destDir)
		var buf bytes.Buffer
		cmd.Stdout = &buf
		cmd.Stderr = &buf
		if err := cmd.Run(); err != nil {
			return "", nil, fmt.Errorf("git clone %s: %w (%s)", repoURL, err, buf.String())
		}
		log.Printf("dotfiles: cloned %s → %s", repoURL, destDir)

		return destDir, nil, nil
	}

	// No source specified — curated dotfile without source.
	// Return a non-existent path which will produce no specs.
	return filepath.Join(dpmRoot, "dotfiles", df.ID), nil, nil
}

// buildSpecs converts a Dotfile's mappings into adapter.DotfileSpec entries.
func buildSpecs(df Dotfile, sourceDir, homeDir string) []adapter.DotfileSpec {
	var specs []adapter.DotfileSpec

	// Prefer explicit Mappings.
	if len(df.Mappings) > 0 {
		for _, m := range df.Mappings {
			sourcePath := filepath.Join(sourceDir, m.Source)
			if _, err := os.Stat(sourcePath); err != nil {
				continue // Skip missing source files.
			}
			targetPath, err := expandTarget(m.Target, homeDir)
			if err != nil {
				log.Printf("dotfiles: skipping mapping with invalid target %q: %v", m.Target, err)
				continue
			}
			strategy := parseMergeStrategy(m.MergeStrategy)
			specs = append(specs, adapter.DotfileSpec{
				SourcePath:    sourcePath,
				TargetPath:    targetPath,
				MergeStrategy: strategy,
			})
		}
		return specs
	}

	// Legacy: Files list — auto-detect source files in repo.
	for _, file := range df.Files {
		// Try to find source in the cloned repo.
		sourcePath := findSourceFile(sourceDir, file)
		if sourcePath == "" {
			continue
		}
		targetPath, err := expandTarget(file, homeDir)
		if err != nil {
			log.Printf("dotfiles: skipping file with invalid target %q: %v", file, err)
			continue
		}
		specs = append(specs, adapter.DotfileSpec{
			SourcePath:    sourcePath,
			TargetPath:    targetPath,
			MergeStrategy: adapter.MergeBackup, // safe default
		})
	}

	return specs
}

// findSourceFile looks for a dotfile source in common repo layouts.
func findSourceFile(repoDir, targetName string) string {
	// Strip leading dot for lookup — many repos store configs without the dot.
	bare := strings.TrimPrefix(targetName, ".")

	candidates := []string{
		filepath.Join(repoDir, targetName),           // exact match: .tmux.conf
		filepath.Join(repoDir, bare),                 // without dot: tmux.conf
		filepath.Join(repoDir, "config", targetName), // config/.tmux.conf
		filepath.Join(repoDir, "config", bare),       // config/tmux.conf
	}

	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() {
			return c
		}
	}
	return ""
}

// expandTarget turns a relative target path (e.g., ".tmux.conf") into an absolute
// path under the user's home directory. Relative paths that would escape the home
// directory (e.g. "../../etc/sudoers") are rejected with an error.
func expandTarget(target, homeDir string) (string, error) {
	var expanded string
	if filepath.IsAbs(target) {
		expanded = filepath.Clean(target)
	} else {
		expanded = filepath.Clean(filepath.Join(homeDir, target))
		homeDirClean := filepath.Clean(homeDir)
		if expanded != homeDirClean && !strings.HasPrefix(expanded, homeDirClean+string(os.PathSeparator)) {
			return "", fmt.Errorf("target path %q escapes the home directory", target)
		}
	}
	return expanded, nil
}

// parseMergeStrategy converts a YAML string to adapter.MergeStrategy.
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

// normalizeRepoURL ensures a GitHub/GitLab shorthand becomes a full HTTPS URL.
// SSH git@ URLs are passed through unchanged. All other inputs are parsed and
// validated: credentials embedded in the URL are rejected, and the scheme must
// be HTTPS.
func normalizeRepoURL(repo string) (string, error) {
	// Pass SSH URLs through as-is — git handles auth via SSH keys.
	if strings.HasPrefix(repo, "git@") {
		return repo, nil
	}

	// Expand "user/repo" shorthand to GitHub HTTPS URL.
	if strings.Count(repo, "/") == 1 && !strings.Contains(repo, ".") {
		repo = "https://github.com/" + repo
	} else if !strings.HasPrefix(repo, "https://") && !strings.HasPrefix(repo, "http://") {
		repo = "https://" + repo
	}

	parsed, err := url.Parse(repo)
	if err != nil {
		return "", fmt.Errorf("invalid repository URL %q: %w", repo, err)
	}

	// Reject embedded credentials — they can be used for credential injection.
	if parsed.User != nil {
		return "", fmt.Errorf("repository URL must not contain credentials: %q", repo)
	}

	// Enforce HTTPS only.
	if parsed.Scheme != "https" {
		return "", fmt.Errorf("repository URL must use HTTPS, got scheme %q in %q", parsed.Scheme, repo)
	}

	if parsed.Host == "" {
		return "", fmt.Errorf("repository URL has no host: %q", repo)
	}

	return parsed.String(), nil
}

// isGitRepo checks if dir contains a .git directory.
func isGitRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}
