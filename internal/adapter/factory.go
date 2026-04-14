package adapter

import (
	"fmt"
	"runtime"

	"dpm.fi/dpm/internal/catalog"
)

func NewAdapter() (IAdapter, error) {
	platform, err := detectPlatform(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return nil, err
	}
	return newAdapter(platform)
}

func detectPlatform(goos, goarch string) (catalog.Platform, error) {
	switch goos + "/" + goarch {
	case "linux/amd64":
		return catalog.PlatformLinuxAMD64, nil
	case "linux/arm64":
		return catalog.PlatformLinuxARM64, nil
	case "darwin/amd64":
		return catalog.PlatformDarwinAMD64, nil
	case "darwin/arm64":
		return catalog.PlatformDarwinARM64, nil
	default:
		return "", fmt.Errorf("adapter: unsupported platform: %s/%s", goos, goarch)
	}
}
