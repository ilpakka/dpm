//go:build darwin

package adapter

import (
	"fmt"
	"os/exec"
	"strings"

	"dpm.fi/dpm/internal/catalog"
)

var _ IAdapter = (*MacOSAdapter)(nil)

type MacOSAdapter struct {
	BaseAdapter
}

func NewMacOSAdapter(platform catalog.Platform) *MacOSAdapter {
	return &MacOSAdapter{BaseAdapter: newBaseAdapter(platform)}
}

func newAdapter(platform catalog.Platform) (IAdapter, error) {
	return NewMacOSAdapter(platform), nil
}

func (a *MacOSAdapter) ExtractArchive(archivePath, destDir string) error {
	switch {
	case strings.HasSuffix(archivePath, ".tar.gz"), strings.HasSuffix(archivePath, ".tgz"):
		return extractTarGz(archivePath, destDir)
	case strings.HasSuffix(archivePath, ".tar.bz2"):
		return extractTarBz2(archivePath, destDir)
	case strings.HasSuffix(archivePath, ".zip"):
		return extractZip(archivePath, destDir)
	case strings.HasSuffix(archivePath, ".pkg"):
		return extractPkg(archivePath, destDir)
	default:
		return fmt.Errorf("adapter: MacOSAdapter.ExtractArchive: unsupported format: %q", archivePath)
	}
}

func extractPkg(archivePath, destDir string) error {
	cmd := exec.Command("pkgutil", "--expand", archivePath, destDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("adapter: extractPkg: pkgutil: %w\n%s", err, out)
	}
	return nil
}

func (a *MacOSAdapter) InstallBundle(bundle Bundle, opts InstallOptions) (*InstallResult, error) {
	return a.installBundle(bundle, opts, a.ExtractArchive)
}
