package installer

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"dpm.iskff.fi/dpm/internal/adapter"
	"dpm.iskff.fi/dpm/internal/catalog"
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
	binPath, err := exec.LookPath(binName)
	if err != nil {
		return adapter.Bundle{}, nil, fmt.Errorf("brew: binary %q not found in PATH after install: %w", binName, err)
	}

	b.logger.Printf("brew: %s installed at %s", pkg, binPath)
	return stageWrapperBundle(tool, version, method, dpmRoot, binPath, catalog.MethodBrew)
}
