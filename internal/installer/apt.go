package installer

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"dpm.iskff.fi/dpm/internal/adapter"
	"dpm.iskff.fi/dpm/internal/catalog"
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
	binPath, err := exec.LookPath(binName)
	if err != nil {
		return adapter.Bundle{}, nil, fmt.Errorf("apt: binary %q not found in PATH after install: %w", binName, err)
	}

	a.logger.Printf("apt: %s installed at %s", pkg, binPath)
	return stageWrapperBundle(tool, version, method, dpmRoot, binPath, catalog.MethodApt)
}
