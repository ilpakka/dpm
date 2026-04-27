package installer

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"dpm.fi/dpm/internal/adapter"
	"dpm.fi/dpm/internal/catalog"
)

// BrewBackend installs tools via Homebrew (macOS / Linux).
type BrewBackend struct {
	logger Logger
}

func (b *BrewBackend) Type() catalog.MethodType { return catalog.MethodBrew }

func (b *BrewBackend) Available() bool { return commandExists("brew") }

func (b *BrewBackend) PrepareBundle(tool catalog.Tool, version catalog.ToolVersion, method catalog.InstallMethod, dpmRoot string) (adapter.Bundle, func(), error) {
	pkg := method.Package
	if pkg == "" {
		pkg = tool.ID
	}

	b.logger.Printf("brew: installing package %s", pkg)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "brew", "install", pkg)
	if err := runWithLogger(cmd, b.logger); err != nil {
		return adapter.Bundle{}, nil, fmt.Errorf("brew: install %s: %w", pkg, err)
	}

	binName := binaryName(tool, method)

	// Resolve the binary via "brew --prefix <pkg>" so we are not vulnerable to
	// a shadowing entry earlier in the ambient PATH.
	binPath, err := brewResolveBin(pkg, binName)
	if err != nil {
		return adapter.Bundle{}, nil, fmt.Errorf("brew: locate binary %q after install: %w", binName, err)
	}

	b.logger.Printf("brew: %s installed at %s", pkg, binPath)
	return stageWrapperBundle(tool, version, method, dpmRoot, binPath, catalog.MethodBrew)
}

// brewResolveBin resolves the installed binary using "brew --prefix <pkg>" to
// get the keg root, then looks for binName under <prefix>/bin/. Falls back to
// the shared Homebrew bin directory as a secondary check.
func brewResolveBin(pkg, binName string) (string, error) {
	out, err := exec.Command("brew", "--prefix", pkg).Output()
	if err == nil {
		prefix := strings.TrimSpace(string(out))
		candidate := filepath.Join(prefix, "bin", binName)
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			return candidate, nil
		}
	}

	// Fall back: check the shared Homebrew bin dir (e.g. /opt/homebrew/bin or
	// /usr/local/bin on Intel Macs). These are brew-owned paths, not user PATH.
	for _, dir := range []string{"/opt/homebrew/bin", "/usr/local/bin", "/home/linuxbrew/.linuxbrew/bin"} {
		candidate := filepath.Join(dir, binName)
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("binary %q not found via brew --prefix or known brew paths", binName)
}
