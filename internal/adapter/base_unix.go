//go:build linux || darwin

package adapter

import (
	"archive/tar"
	"compress/bzip2"
	"fmt"
	"os"
	"path/filepath"

	"dpm.fi/dpm/internal/catalog"
)

func newBaseAdapter(platform catalog.Platform) BaseAdapter {
	dpmRoot := os.Getenv("DPM_HOME")
	if dpmRoot == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = os.Getenv("HOME")
		}
		dpmRoot = filepath.Join(home, ".dpm")
	}
	return BaseAdapter{
		dpmRoot:  dpmRoot,
		platform: platform,
	}
}

func extractTarBz2(archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("adapter: extractTarBz2: open: %w", err)
	}
	defer f.Close()
	return extractTar(tar.NewReader(bzip2.NewReader(f)), destDir)
}
