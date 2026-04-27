package installer

import (
	"bytes"
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

// AptBackend installs tools via apt-get on Debian/Ubuntu systems.
// After installation it locates the binary and creates a symlink-ready Bundle.
type AptBackend struct {
	logger Logger
}

func (a *AptBackend) Type() catalog.MethodType { return catalog.MethodApt }

func (a *AptBackend) Available() bool { return commandExists("apt-get") }

func (a *AptBackend) PrepareBundle(tool catalog.Tool, version catalog.ToolVersion, method catalog.InstallMethod, dpmRoot string) (adapter.Bundle, func(), error) {
	pkg := method.Package
	if pkg == "" {
		pkg = tool.ID
	}

	a.logger.Printf("apt: installing package %s", pkg)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sudo", "apt-get", "install", "--", pkg)
	if err := runWithLogger(cmd, a.logger); err != nil {
		return adapter.Bundle{}, nil, fmt.Errorf("apt: install %s: %w", pkg, err)
	}

	binName := binaryName(tool, method)

	// Resolve the binary using dpkg so we are not vulnerable to a shadowing
	// entry earlier in the ambient PATH.
	binPath, err := dpkgResolveBin(pkg, binName)
	if err != nil {
		return adapter.Bundle{}, nil, fmt.Errorf("apt: locate binary %q after install: %w", binName, err)
	}

	a.logger.Printf("apt: %s installed at %s", pkg, binPath)
	return stageWrapperBundle(tool, version, method, dpmRoot, binPath, catalog.MethodApt)
}

// dpkgResolveBin uses "dpkg -L <pkg>" to enumerate the package's file list and
// returns the first file whose base name matches binName inside a known system
// binary directory (/bin, /usr/bin, /usr/local/bin, /sbin, /usr/sbin).
// Falls back to a PATH search restricted to those trusted directories only.
func dpkgResolveBin(pkg, binName string) (string, error) {
	out, err := exec.Command("dpkg", "-L", pkg).Output()
	if err == nil {
		trustedDirs := map[string]bool{
			"/bin": true, "/usr/bin": true, "/usr/local/bin": true,
			"/sbin": true, "/usr/sbin": true,
		}
		for _, line := range strings.Split(string(bytes.TrimSpace(out)), "\n") {
			line = strings.TrimSpace(line)
			if filepath.Base(line) == binName && trustedDirs[filepath.Dir(line)] {
				if info, statErr := os.Stat(line); statErr == nil && !info.IsDir() {
					return line, nil
				}
			}
		}
	}

	// dpkg lookup failed or binary not in trusted dirs — fall back to a
	// PATH search restricted to known system binary directories only.
	for _, dir := range []string{"/usr/bin", "/bin", "/usr/local/bin", "/usr/sbin", "/sbin"} {
		candidate := filepath.Join(dir, binName)
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() {
			return candidate, nil
		}
	}

	return "", fmt.Errorf("binary %q not found in package file list or trusted system paths", binName)
}
