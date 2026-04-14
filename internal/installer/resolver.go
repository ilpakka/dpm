// Package installer implements the multi-source installation backends for DPM.
//
// The Resolver picks the best available InstallMethod for a given tool version
// and delegates to the appropriate backend (http, apt, brew, pip, cargo).
// It implements the engine.Installer interface so it can replace stubInstaller.
package installer

import (
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"dpm.iskff.fi/dpm/internal/adapter"
	"dpm.iskff.fi/dpm/internal/catalog"
)

// Logger is compatible with engine.Logger.
type Logger interface {
	Printf(format string, v ...any)
}

// Backend knows how to install a tool using one specific method type.
type Backend interface {
	// Type returns the MethodType this backend handles.
	Type() catalog.MethodType

	// Available returns true if the backend's prerequisites exist on this system
	// (e.g. apt-get is in PATH, cargo is installed, etc.).
	Available() bool

	// PrepareBundle downloads/installs the tool and returns a Bundle ready for
	// the adapter. The cleanup function removes any temp files when called.
	PrepareBundle(tool catalog.Tool, version catalog.ToolVersion, method catalog.InstallMethod, dpmRoot string) (adapter.Bundle, func(), error)
}

// Resolver selects the best available install method and delegates to the
// matching backend. It implements the engine.Installer interface.
type Resolver struct {
	backends map[catalog.MethodType]Backend
	platform catalog.Platform
	bubble   bool   // true if running in bubble mode (ephemeral session)
	dpmRoot  string // ~/.dpm or /tmp/dpm-bubble-<id>
	logger   Logger
}

// ResolverConfig holds the parameters for creating a new Resolver.
type ResolverConfig struct {
	Platform catalog.Platform
	Bubble   bool
	DPMRoot  string
	Logger   Logger
}

// NewResolver creates a Resolver with all available backends registered.
func NewResolver(cfg ResolverConfig) *Resolver {
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}

	r := &Resolver{
		backends: make(map[catalog.MethodType]Backend),
		platform: cfg.Platform,
		bubble:   cfg.Bubble,
		dpmRoot:  cfg.DPMRoot,
		logger:   cfg.Logger,
	}

	// Register all backends. Each checks its own availability at call time.
	r.Register(&HTTPBackend{
		logger: cfg.Logger,
		client: &http.Client{Timeout: 10 * time.Minute}, // archive downloads can be large
		metaClient: &http.Client{Timeout: 30 * time.Second}, // PGP keys/sigs are small
	})
	r.Register(&AptBackend{logger: cfg.Logger})
	r.Register(&BrewBackend{logger: cfg.Logger})
	r.Register(&PipBackend{logger: cfg.Logger})
	r.Register(&CargoBackend{logger: cfg.Logger})

	return r
}

// Register adds a backend to the resolver.
func (r *Resolver) Register(b Backend) {
	r.backends[b.Type()] = b
}

// methodPriority defines the preferred order when multiple methods are available.
// Lower number = higher priority.
var methodPriority = map[catalog.MethodType]int{
	catalog.MethodHTTP:  1, // Fastest, most deterministic
	catalog.MethodBrew:  2, // Good on macOS
	catalog.MethodApt:   3, // Good on Debian/Ubuntu
	catalog.MethodPip:   4, // Slower, needs Python
	catalog.MethodCargo: 5, // Slowest, compiles from source
}

// PrepareBundle implements engine.Installer. It resolves the best method for the
// current platform and delegates to the corresponding backend.
func (r *Resolver) PrepareBundle(tool catalog.Tool, version catalog.ToolVersion, platform catalog.Platform) (adapter.Bundle, func(), error) {
	methods := version.MethodsForPlatform(platform, r.bubble)

	// Fallback: if no install_methods defined, try to build an http method
	// from the legacy Binaries map so old YAML still works.
	if len(methods) == 0 {
		if bin, ok := version.Binaries[platform]; ok {
			methods = []catalog.InstallMethod{{
				Type:         catalog.MethodHTTP,
				Platforms:    []catalog.Platform{platform},
				BubbleCompat: true,
				URL:          bin.URL,
				SHA256:       bin.SHA256,
				Size:         bin.Size,
			}}
		}
	}

	if len(methods) == 0 {
		return adapter.Bundle{}, nil, fmt.Errorf("installer: no install method available for %s@%s on %s (bubble=%t)",
			tool.ID, version.Version, platform, r.bubble)
	}

	// Sort by priority and find the first available backend.
	sorted := sortByPriority(methods)
	var reasons []string
	for _, method := range sorted {
		backend, ok := r.backends[method.Type]
		if !ok {
			reasons = append(reasons, fmt.Sprintf("  %s: no backend registered", method.Type))
			continue
		}
		if !backend.Available() {
			r.logger.Printf("installer: backend %s not available, trying next", method.Type)
			reasons = append(reasons, fmt.Sprintf("  %s: prerequisites not installed", method.Type))
			continue
		}

		r.logger.Printf("installer: using %s backend for %s@%s", method.Type, tool.ID, version.Version)
		bundle, cleanup, err := backend.PrepareBundle(tool, version, method, r.dpmRoot)
		if err != nil {
			wrapped := fmt.Errorf("installer: %s backend failed for %s@%s: %w", method.Type, tool.ID, version.Version, err)
			r.logger.Printf("%s", wrapped)
			reasons = append(reasons, fmt.Sprintf("  %s: %v", method.Type, err))
			continue
		}
		return bundle, cleanup, nil
	}

	return adapter.Bundle{}, nil, fmt.Errorf("installer: all methods failed for %s@%s on %s:\n%s",
		tool.ID, version.Version, platform, strings.Join(reasons, "\n"))
}

// sortByPriority returns methods ordered by the preferred installation order.
func sortByPriority(methods []catalog.InstallMethod) []catalog.InstallMethod {
	sorted := make([]catalog.InstallMethod, len(methods))
	copy(sorted, methods)

	for i := 0; i < len(sorted)-1; i++ {
		for j := i + 1; j < len(sorted); j++ {
			pi := methodPriority[sorted[i].Type]
			pj := methodPriority[sorted[j].Type]
			if pi == 0 {
				pi = 99
			}
			if pj == 0 {
				pj = 99
			}
			if pj < pi {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	return sorted
}

// commandExists checks if a command is available in PATH.
func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// binaryName returns the binary name for a tool — uses method override if set,
// otherwise falls back to tool ID.
func binaryName(tool catalog.Tool, method catalog.InstallMethod) string {
	if method.BinaryName != "" {
		return method.BinaryName
	}
	if strings.TrimSpace(tool.ID) == "" {
		return "tool"
	}
	return tool.ID
}
