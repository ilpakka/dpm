//go:build !linux && !darwin

package adapter

import (
	"fmt"

	"dpm.iskff.fi/dpm/internal/catalog"
)

func newAdapter(platform catalog.Platform) (IAdapter, error) {
	return nil, fmt.Errorf("adapter: platform %q is not supported (Linux and macOS only)", platform)
}
