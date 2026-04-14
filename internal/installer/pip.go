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

// PipBackend installs Python packages via pip.
// It installs into a DPM-managed directory and creates a wrapper script.
type PipBackend struct {
	logger Logger
}

func (p *PipBackend) Type() catalog.MethodType { return catalog.MethodPip }

func (p *PipBackend) Available() bool {
	return commandExists("pip3") || commandExists("pip")
}

func (p *PipBackend) pipCmd() string {
	if commandExists("pip3") {
		return "pip3"
	}
	return "pip"
}

func (p *PipBackend) PrepareBundle(tool catalog.Tool, version catalog.ToolVersion, method catalog.InstallMethod, dpmRoot string) (adapter.Bundle, func(), error) {
	pkg := method.Package
	if pkg == "" {
		pkg = tool.ID
	}

	// Install into an isolated target directory under DPM tools.
	installDir := filepath.Join(dpmRoot, "tools", tool.ID, version.Version)
	siteDir := filepath.Join(installDir, "lib")
	binDir := filepath.Join(installDir, "bin")
	if err := os.MkdirAll(siteDir, 0o755); err != nil {
		return adapter.Bundle{}, nil, fmt.Errorf("pip: create install dir: %w", err)
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return adapter.Bundle{}, nil, fmt.Errorf("pip: create bin dir: %w", err)
	}

	p.logger.Printf("pip: installing %s into %s", pkg, installDir)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, p.pipCmd(), "install", "--prefix", installDir, pkg)
	cmd.Env = append(os.Environ(), "PYTHONUSERBASE="+installDir)
	if err := runWithLogger(cmd, p.logger); err != nil {
		_ = os.RemoveAll(installDir)
		return adapter.Bundle{}, nil, fmt.Errorf("pip: install %s: %w", pkg, err)
	}

	// pip --prefix puts scripts in <prefix>/bin/ on Unix.
	// Verify the binary exists; if not, create a wrapper that invokes the module.
	binName := binaryName(tool, method)
	expectedBin := filepath.Join(binDir, binName)
	if _, err := os.Stat(expectedBin); err != nil {
		p.logger.Printf("pip: binary %s not in bin/, creating wrapper script", binName)
		// Escape single quotes in the install path so the generated shell script
		// is safe even if the path contains a quote character.
		safeDir := strings.ReplaceAll(installDir, "'", "'\\''")
		wrapperContent := fmt.Sprintf("#!/bin/sh\nexport PYTHONPATH=\"$(find '%s/lib' -type d -name site-packages 2>/dev/null | head -1):$PYTHONPATH\"\nexec python3 -m %s \"$@\"\n",
			safeDir, tool.ID)
		if err := os.WriteFile(expectedBin, []byte(wrapperContent), 0o755); err != nil {
			_ = os.RemoveAll(installDir)
			return adapter.Bundle{}, nil, fmt.Errorf("pip: write wrapper: %w", err)
		}
	}

	return packageBundleFromDir(tool, version, method, installDir, catalog.MethodPip)
}
