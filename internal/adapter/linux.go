//go:build linux

package adapter

import (
	"fmt"
	"strings"

	"dpm.fi/dpm/internal/catalog"
)

var _ IAdapter = (*LinuxAdapter)(nil)

type LinuxAdapter struct {
	BaseAdapter
}

func NewLinuxAdapter(platform catalog.Platform) *LinuxAdapter {
	return &LinuxAdapter{BaseAdapter: newBaseAdapter(platform)}
}

func newAdapter(platform catalog.Platform) (IAdapter, error) {
	return NewLinuxAdapter(platform), nil
}

func (a *LinuxAdapter) ExtractArchive(archivePath, destDir string) error {
	switch {
	case strings.HasSuffix(archivePath, ".tar.gz"), strings.HasSuffix(archivePath, ".tgz"):
		return extractTarGz(archivePath, destDir)
	case strings.HasSuffix(archivePath, ".tar.bz2"):
		return extractTarBz2(archivePath, destDir)
	case strings.HasSuffix(archivePath, ".zip"):
		return extractZip(archivePath, destDir)
	default:
		return fmt.Errorf("adapter: LinuxAdapter.ExtractArchive: unsupported format: %q", archivePath)
	}
}

func (a *LinuxAdapter) InstallBundle(bundle Bundle, opts InstallOptions) (*InstallResult, error) {
	return a.installBundle(bundle, opts, a.ExtractArchive)
}
