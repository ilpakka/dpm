package installer

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"dpm.fi/dpm/internal/adapter"
	"dpm.fi/dpm/internal/catalog"
)

// cargoInstallTimeout caps how long a `cargo install` may run.
// Rust compilation can be slow for large crates; 30 minutes covers all known cases.
const cargoInstallTimeout = 30 * time.Minute

// CargoBackend installs Rust packages via cargo install.
// Compiles from source into a DPM-managed directory.
type CargoBackend struct {
	logger Logger
}

func (c *CargoBackend) Type() catalog.MethodType { return catalog.MethodCargo }

func (c *CargoBackend) Available() bool { return commandExists("cargo") }

func (c *CargoBackend) PrepareBundle(tool catalog.Tool, version catalog.ToolVersion, method catalog.InstallMethod, dpmRoot string) (adapter.Bundle, func(), error) {
	pkg := method.Package
	if pkg == "" {
		pkg = tool.ID
	}

	// cargo install --root puts the binary in <root>/bin/
	installDir := filepath.Join(dpmRoot, "tools", tool.ID, version.Version)
	if err := os.MkdirAll(installDir, 0o700); err != nil {
		return adapter.Bundle{}, nil, fmt.Errorf("cargo: create install dir: %w", err)
	}

	c.logger.Printf("cargo: installing %s into %s (this may take a while — compiling from source)", pkg, installDir)

	ctx, cancel := context.WithTimeout(context.Background(), cargoInstallTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "cargo", "install", "--root", installDir, pkg)
	if err := runWithLogger(cmd, c.logger); err != nil {
		_ = os.RemoveAll(installDir)
		return adapter.Bundle{}, nil, fmt.Errorf("cargo: install %s: %w", pkg, err)
	}

	// Verify the binary exists before archiving.
	binName := binaryName(tool, method)
	expectedBin := filepath.Join(installDir, "bin", binName)
	if _, err := os.Stat(expectedBin); err != nil {
		_ = os.RemoveAll(installDir)
		return adapter.Bundle{}, nil, fmt.Errorf("cargo: expected binary %s not found after install: %w", expectedBin, err)
	}

	c.logger.Printf("cargo: %s compiled and installed", pkg)
	return packageBundleFromDir(tool, version, method, installDir, catalog.MethodCargo)
}
