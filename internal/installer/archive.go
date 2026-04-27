package installer

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"dpm.fi/dpm/internal/adapter"
	"dpm.fi/dpm/internal/catalog"
)

// buildTarGz creates a .tar.gz archive from the contents of srcDir.
// Files inside the archive are stored relative to srcDir.
// Returns the archive path and a cleanup function that removes the temp file.
func buildTarGz(srcDir, toolID, version string) (archivePath string, cleanup func(), err error) {
	tmpDir, err := os.MkdirTemp("", "dpm-archive-"+toolID+"-")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { _ = os.RemoveAll(tmpDir) }

	archivePath = filepath.Join(tmpDir, toolID+"-"+version+".tar.gz")
	f, err := os.Create(archivePath)
	if err != nil {
		cleanup()
		return "", nil, err
	}

	gw := gzip.NewWriter(f)
	tw := tar.NewWriter(gw)

	err = filepath.Walk(srcDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return fmt.Errorf("archive: rel path %q: %w", path, err)
		}
		if rel == "." {
			return nil
		}

		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return fmt.Errorf("archive: file info header %q: %w", rel, err)
		}
		header.Name = rel

		// Ensure forward slashes in archive.
		header.Name = strings.ReplaceAll(header.Name, string(os.PathSeparator), "/")

		if err := tw.WriteHeader(header); err != nil {
			return fmt.Errorf("archive: write header %q: %w", rel, err)
		}

		if info.IsDir() {
			return nil
		}

		src, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("archive: open %q: %w", path, err)
		}
		defer src.Close()
		if _, err := io.Copy(tw, src); err != nil {
			return fmt.Errorf("archive %s: %w", rel, err)
		}
		return nil
	})

	if err != nil {
		_ = tw.Close()
		_ = gw.Close()
		_ = f.Close()
		cleanup()
		return "", nil, err
	}

	if err := tw.Close(); err != nil {
		_ = gw.Close()
		_ = f.Close()
		cleanup()
		return "", nil, err
	}
	if err := gw.Close(); err != nil {
		_ = f.Close()
		cleanup()
		return "", nil, err
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", nil, err
	}

	return archivePath, cleanup, nil
}

// packageBundleFromDir archives dir into a Bundle and removes dir.
// Used by pip and cargo after installing directly into an install directory.
func packageBundleFromDir(
	tool catalog.Tool,
	version catalog.ToolVersion,
	method catalog.InstallMethod,
	dir string,
	methodType catalog.MethodType,
) (adapter.Bundle, func(), error) {
	archivePath, cleanup, err := buildTarGz(dir, tool.ID, version.Version)
	if err != nil {
		_ = os.RemoveAll(dir)
		return adapter.Bundle{}, nil, fmt.Errorf("%s: build archive: %w", methodType, err)
	}
	_ = os.RemoveAll(dir)
	return adapter.Bundle{
		ToolID:      tool.ID,
		ToolName:    tool.Name,
		Version:     version.Version,
		ArchivePath: archivePath,
		Binaries:    []string{binaryName(tool, method)},
		DataDirs:    method.DataDirs,
		Method:      string(methodType),
		Verified:    true,
	}, cleanup, nil
}

// stageWrapperBundle creates a wrapper script for a system-installed binary,
// archives the staging dir into a Bundle, and removes the staging dir.
// Used by apt and brew after locating the real binary in PATH.
func stageWrapperBundle(
	tool catalog.Tool,
	version catalog.ToolVersion,
	method catalog.InstallMethod,
	dpmRoot string,
	binPath string,
	methodType catalog.MethodType,
) (adapter.Bundle, func(), error) {
	binName := binaryName(tool, method)
	stageDir := filepath.Join(dpmRoot, "tools", tool.ID, version.Version)
	stageBin := filepath.Join(stageDir, "bin")
	if err := os.MkdirAll(stageBin, 0o700); err != nil {
		return adapter.Bundle{}, nil, fmt.Errorf("%s: create stage dir: %w", methodType, err)
	}
	// Single-quote the binary path so spaces and most special characters in
	// the path do not cause word-splitting or unexpected shell expansion.
	// Literal single quotes inside the path are escaped with '\''.
	safePath := strings.ReplaceAll(binPath, "'", "'\\''")
	wrapperContent := fmt.Sprintf("#!/bin/sh\nexec '%s' \"$@\"\n", safePath)
	if err := os.WriteFile(filepath.Join(stageBin, binName), []byte(wrapperContent), 0o755); err != nil {
		_ = os.RemoveAll(stageDir)
		return adapter.Bundle{}, nil, fmt.Errorf("%s: write wrapper: %w", methodType, err)
	}
	return packageBundleFromDir(tool, version, method, stageDir, methodType)
}
