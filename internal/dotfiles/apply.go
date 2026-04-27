package dotfiles

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"dpm.fi/dpm/internal/adapter"
)

// ApplyResult tracks what happened when installing a dotfile set.
type ApplyResult struct {
	DotfileID string   `json:"dotfile_id"`
	ClonedTo  string   `json:"cloned_to,omitempty"` // path where repo was cloned
	Commit    string   `json:"commit,omitempty"`    // git commit used for immutable snapshots
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

	plan, err := Plan(df, opts)
	if err != nil {
		return nil, err
	}
	result.ClonedTo = plan.SourceDir
	result.Commit = plan.Commit
	if plan.Blocked {
		return nil, fmt.Errorf("dotfiles: plan for %s is blocked: %s", df.ID, blockedIssues(plan))
	}

	specs := plan.Specs()
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

func blockedIssues(plan *PlanResult) string {
	var issues []string
	for _, item := range plan.Items {
		if item.Risk != RiskBlock {
			continue
		}
		for _, issue := range item.Issues {
			issues = append(issues, fmt.Sprintf("%s: %s", item.Target, issue))
		}
	}
	if len(issues) == 0 {
		return "blocked by dotfile safety policy"
	}
	return strings.Join(issues, "; ")
}

// resolveSource ensures dotfile source files are available locally.
// For git repos, clones into an immutable commit snapshot. For local dirs, returns as-is.
func resolveSource(df Dotfile, dpmRoot string) (string, error) {
	// Local source directory — use directly.
	if df.SourceDir != "" {
		if _, err := os.Stat(df.SourceDir); err != nil {
			return "", fmt.Errorf("source dir %s: %w", df.SourceDir, err)
		}
		abs, err := filepath.Abs(df.SourceDir)
		if err != nil {
			return "", fmt.Errorf("source dir %s: abs: %w", df.SourceDir, err)
		}
		return abs, nil
	}

	// Git repo — clone to staging, resolve commit, then promote to immutable snapshot.
	if df.SourceRepo != "" {
		return resolveGitSnapshot(df, dpmRoot)
	}

	// No source specified — curated dotfile without source.
	// Return a non-existent path which will produce no specs.
	return filepath.Join(dpmRoot, "dotfiles", df.ID), nil
}

func resolveGitSnapshot(df Dotfile, dpmRoot string) (string, error) {
	repoURL, err := normalizeRepoURL(df.SourceRepo)
	if err != nil {
		return "", fmt.Errorf("invalid dotfile repo URL: %w", err)
	}

	snapshotID := safeSnapshotID(df.ID, repoURL)
	dotRoot := filepath.Join(dpmRoot, "dotfiles")
	stagingRoot := filepath.Join(dotRoot, "staging")
	snapshotRoot := filepath.Join(dotRoot, "snapshots", snapshotID)
	if err := os.MkdirAll(stagingRoot, 0o700); err != nil {
		return "", fmt.Errorf("create dotfiles staging dir: %w", err)
	}
	if err := os.MkdirAll(snapshotRoot, 0o700); err != nil {
		return "", fmt.Errorf("create dotfiles snapshots dir: %w", err)
	}

	stagingDir, err := os.MkdirTemp(stagingRoot, snapshotID+"-")
	if err != nil {
		return "", fmt.Errorf("create dotfiles staging clone: %w", err)
	}
	cleanupStaging := true
	defer func() {
		if cleanupStaging {
			_ = os.RemoveAll(stagingDir)
		}
	}()

	cloneCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if _, err := runGit(cloneCtx, "", "clone", "--depth", "1", repoURL, stagingDir); err != nil {
		return "", fmt.Errorf("git clone %s: %w", repoURL, err)
	}
	commit, err := gitCommit(stagingDir)
	if err != nil {
		return "", err
	}
	snapshotDir := filepath.Join(snapshotRoot, commit)

	if snapshotOK(snapshotDir, commit) {
		log.Printf("dotfiles: using cached snapshot %s", snapshotDir)
		return snapshotDir, nil
	}
	if _, err := os.Stat(snapshotDir); err == nil {
		return "", fmt.Errorf("dotfiles: snapshot %s exists but does not match commit %s", snapshotDir, commit)
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("dotfiles: stat snapshot %s: %w", snapshotDir, err)
	}

	if err := os.Rename(stagingDir, snapshotDir); err != nil {
		if snapshotOK(snapshotDir, commit) {
			return snapshotDir, nil
		}
		return "", fmt.Errorf("dotfiles: promote snapshot %s: %w", snapshotDir, err)
	}
	cleanupStaging = false
	log.Printf("dotfiles: promoted %s@%s → %s", repoURL, commit, snapshotDir)
	return snapshotDir, nil
}

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	if err := cmd.Run(); err != nil {
		return strings.TrimSpace(buf.String()), fmt.Errorf("%w (%s)", err, strings.TrimSpace(buf.String()))
	}
	return strings.TrimSpace(buf.String()), nil
}

func gitCommit(repoDir string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	commit, err := runGit(ctx, repoDir, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("dotfiles: resolve git commit for %s: %w", repoDir, err)
	}
	if len(commit) != 40 {
		return "", fmt.Errorf("dotfiles: git commit for %s is invalid: %q", repoDir, commit)
	}
	return commit, nil
}

func snapshotOK(snapshotDir, commit string) bool {
	if _, err := os.Stat(snapshotDir); err != nil {
		return false
	}
	existing, err := gitCommit(snapshotDir)
	return err == nil && existing == commit
}

func safeSnapshotID(id, repoURL string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	base := strings.Trim(b.String(), "-._")
	if base == "" {
		base = "dotfile"
	}
	return base + "-" + shortHash(repoURL)
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:12]
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
			targetPath, err := ExpandTarget(m.Target, homeDir)
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
		targetPath, err := ExpandTarget(file, homeDir)
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

// ExpandTarget turns a relative target path (e.g., ".tmux.conf") into an absolute
// path under the user's home directory. Relative paths that would escape the home
// directory (e.g. "../../etc/sudoers") are rejected with an error.
func ExpandTarget(target, homeDir string) (string, error) {
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
// SSH URLs (both ssh:// scheme and SCP-style [user@]host:path) are passed
// through after lightweight validation; git handles auth via SSH keys.
// HTTPS URLs are validated and credentials are rejected.
func normalizeRepoURL(repo string) (string, error) {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return "", fmt.Errorf("repository URL is empty")
	}

	// SCP-style SSH URL: [user@]host:path (e.g. git@github.com:user/repo or
	// henry@polaris.koti:/storage/dotfiles.git). Detected by a colon that
	// precedes any slash and no "://" anywhere in the string.
	if isSSHScpStyle(repo) {
		if err := validateSSHScpURL(repo); err != nil {
			return "", err
		}
		return repo, nil
	}

	// ssh:// protocol URL.
	if strings.HasPrefix(repo, "ssh://") {
		parsed, err := url.Parse(repo)
		if err != nil {
			return "", fmt.Errorf("invalid ssh repository URL %q: %w", repo, err)
		}
		if parsed.Host == "" {
			return "", fmt.Errorf("ssh repository URL has no host: %q", repo)
		}
		if parsed.User != nil {
			if _, hasPassword := parsed.User.Password(); hasPassword {
				return "", fmt.Errorf("ssh repository URL must not contain a password: %q", repo)
			}
		}
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

func isSSHScpStyle(repo string) bool {
	if strings.Contains(repo, "://") {
		return false
	}
	colon := strings.IndexByte(repo, ':')
	if colon <= 0 {
		return false
	}
	if slash := strings.IndexByte(repo, '/'); slash != -1 && slash < colon {
		return false
	}
	return true
}

func validateSSHScpURL(repo string) error {
	colon := strings.IndexByte(repo, ':')
	hostPart := repo[:colon]

	var user, host string
	if at := strings.IndexByte(hostPart, '@'); at >= 0 {
		user = hostPart[:at]
		host = hostPart[at+1:]
	} else {
		host = hostPart
	}

	if host == "" {
		return fmt.Errorf("repository URL has empty host: %q", repo)
	}
	if !validHostChars(user) {
		return fmt.Errorf("repository URL has invalid user %q", user)
	}
	if !validHostChars(host) {
		return fmt.Errorf("repository URL has invalid host %q", host)
	}
	return nil
}

func validHostChars(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-', r == '.', r == '_':
		default:
			return false
		}
	}
	return true
}

// isGitRepo checks if dir contains a .git directory.
func isGitRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

// manifestCandidates lists the file names a dotfiles repo can use to declare
// its file mappings. The first match wins.
var manifestCandidates = []string{"dpm.yaml", "dpm.yml", ".dpm.yaml", ".dpm.yml"}

// mergeManifest looks for a DPM manifest at the repo root and fills in
// df.Mappings / df.Files from it when the caller didn't supply any. CLI-provided
// mappings always take precedence; the manifest only fills empty metadata fields.
func mergeManifest(df *Dotfile, sourceDir string) error {
	if len(df.Mappings) > 0 || len(df.Files) > 0 {
		return nil
	}
	for _, name := range manifestCandidates {
		path := filepath.Join(sourceDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("read manifest %s: %w", name, err)
		}
		var manifest Dotfile
		if err := yaml.Unmarshal(data, &manifest); err != nil {
			return fmt.Errorf("parse manifest %s: %w", name, err)
		}
		if df.ID == "" {
			df.ID = manifest.ID
		}
		if df.Name == "" {
			df.Name = manifest.Name
		}
		if df.Description == "" {
			df.Description = manifest.Description
		}
		if df.ToolID == "" {
			df.ToolID = manifest.ToolID
		}
		if df.Version == "" {
			df.Version = manifest.Version
		}
		df.Mappings = manifest.Mappings
		df.Files = manifest.Files
		log.Printf("dotfiles: loaded manifest %s with %d mappings", name, len(manifest.Mappings))
		return nil
	}
	return nil
}
