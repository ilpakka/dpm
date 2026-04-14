package catalog

import (
	"os"
	"strings"
)

// Platform represents a supported platform
type Platform string

const (
	PlatformLinuxAMD64  Platform = "linux-amd64"
	PlatformLinuxARM64  Platform = "linux-arm64"
	PlatformDarwinAMD64 Platform = "darwin-amd64"
	PlatformDarwinARM64 Platform = "darwin-arm64"
	// Windows-native is not supported in v1.0; WSL users run the Linux binary.
)

// MethodType identifies the installation backend (http, apt, brew, pip, cargo).
type MethodType string

const (
	MethodHTTP  MethodType = "http"  // Download static binary/archive from URL
	MethodApt   MethodType = "apt"   // Install via apt-get (Debian/Ubuntu)
	MethodBrew  MethodType = "brew"  // Install via Homebrew
	MethodPip   MethodType = "pip"   // Install via pip (Python)
	MethodCargo MethodType = "cargo" // Install via cargo (Rust)
)

// InstallMethod describes one way to install a tool version on specific platforms.
// A tool version may list multiple methods; the engine picks the best available
// one based on priority, platform, and bubble mode.
type InstallMethod struct {
	Type         MethodType `yaml:"type"              json:"type"`              // "http", "apt", "brew", "pip", "cargo"
	Platforms    []Platform `yaml:"platforms"         json:"platforms"`         // platforms where this method works
	BubbleCompat bool       `yaml:"bubble_compatible" json:"bubble_compatible"` // safe for ephemeral bubble sessions

	// HTTP-specific fields
	URL    string `yaml:"url,omitempty"    json:"url,omitempty"`    // download URL (http only)
	SHA256 string `yaml:"sha256,omitempty" json:"sha256,omitempty"` // expected SHA-256 hex digest (http only)
	Size   int64  `yaml:"size,omitempty"   json:"size,omitempty"`   // expected archive size in bytes (http only)

	// Package manager fields
	Package string `yaml:"package,omitempty" json:"package,omitempty"` // package name (apt, brew, pip, cargo)

	// Binary name override — if the installed binary has a different name than tool ID
	BinaryName string `yaml:"binary_name,omitempty" json:"binary_name,omitempty"`

	// DataDirs lists tool-specific directories to create under the user's home
	// during installation (e.g. ~/.tool/wordlists). Paths starting with ~/ are
	// expanded by the engine before being passed to the adapter.
	DataDirs []string `yaml:"data_dirs,omitempty" json:"data_dirs,omitempty"`

	// PGP fields for publisher-signed releases.
	// If both are set the HTTP backend fetches the key and signature and verifies
	// the downloaded archive using the publisher's own public key.
	PGPKeyURL string `yaml:"pgp_key_url,omitempty" json:"pgp_key_url,omitempty"` // URL to the ASCII-armored public key
	PGPSigURL string `yaml:"pgp_sig_url,omitempty" json:"pgp_sig_url,omitempty"` // URL to the detached signature (.asc/.sig)
}

// Binary represents a pre-built binary for a specific platform.
// Retained for backward compatibility with existing YAML catalogs that use
// the binaries: map. New tool definitions should prefer install_methods.
type Binary struct {
	URL    string `yaml:"url"    json:"url"`
	SHA256 string `yaml:"sha256" json:"sha256"`
	Size   int64  `yaml:"size"   json:"size"`
}

// ToolVersion represents a specific version of a tool
type ToolVersion struct {
	Version        string              `yaml:"version"         json:"version"`
	MajorVersion   int                 `yaml:"major_version"   json:"major_version"`
	IsLatest       bool                `yaml:"is_latest"       json:"is_latest"`
	ReleaseDate    string              `yaml:"release_date"    json:"release_date,omitempty"`
	Binaries       map[Platform]Binary `yaml:"binaries"        json:"binaries,omitempty"`        // legacy: per-platform binary map
	InstallMethods []InstallMethod     `yaml:"install_methods" json:"install_methods,omitempty"` // new: multi-source install methods
	Installed      bool                `json:"installed"`                                        // Runtime state
}

// MethodsForPlatform returns the InstallMethods available for a given platform,
// optionally filtering to bubble-compatible methods only.
func (v ToolVersion) MethodsForPlatform(platform Platform, bubbleOnly bool) []InstallMethod {
	var methods []InstallMethod
	for _, m := range v.InstallMethods {
		if !platformMatch(m.Platforms, platform) {
			continue
		}
		if bubbleOnly && !m.BubbleCompat {
			continue
		}
		methods = append(methods, m)
	}
	return methods
}

func platformMatch(platforms []Platform, target Platform) bool {
	for _, p := range platforms {
		if p == target {
			return true
		}
	}
	return false
}

// Tool represents a tool with multiple versions
type Tool struct {
	ID          string        `yaml:"id"          json:"id"`
	Name        string        `yaml:"name"        json:"name"`
	Description string        `yaml:"description" json:"description"`
	Category    string        `yaml:"category"    json:"category,omitempty"`
	Tags        []string      `yaml:"tags"        json:"tags,omitempty"`
	Versions    []ToolVersion `yaml:"versions"    json:"versions"`
}

// GetDefaultVersion returns the default (highest major version) version
func (t Tool) GetDefaultVersion() *ToolVersion {
	if len(t.Versions) == 0 {
		return nil
	}

	// Find the highest major version that is marked as latest
	for i := range t.Versions {
		if t.Versions[i].IsLatest {
			return &t.Versions[i]
		}
	}

	// Fallback: return first version
	return &t.Versions[0]
}

// GetVersionByMajor returns a specific major version
func (t Tool) GetVersionByMajor(major int) *ToolVersion {
	for i := range t.Versions {
		if t.Versions[i].MajorVersion == major {
			return &t.Versions[i]
		}
	}
	return nil
}

// GetAllInstalledVersions returns all installed versions
func (t Tool) GetAllInstalledVersions() []ToolVersion {
	var installed []ToolVersion
	for _, v := range t.Versions {
		if v.Installed {
			installed = append(installed, v)
		}
	}
	return installed
}

// IsAvailableOnPlatform checks if tool is available for given platform.
// Checks both legacy Binaries map and new InstallMethods.
func (t Tool) IsAvailableOnPlatform(platform Platform) bool {
	for _, v := range t.Versions {
		// Legacy binaries map
		if _, ok := v.Binaries[platform]; ok {
			return true
		}
		// New install methods
		if len(v.MethodsForPlatform(platform, false)) > 0 {
			return true
		}
	}
	return false
}

// GetToolByID returns the tool with the given ID from a slice, or nil if not found.
func GetToolByID(tools []Tool, id string) *Tool {
	for i := range tools {
		if tools[i].ID == id {
			return &tools[i]
		}
	}
	return nil
}

// GetBundleForPlatform returns the first available InstallMethod for the default
// version of the tool on the given platform. Falls back to the legacy Binaries
// map if no install_methods are defined. Returns nil if nothing is available.
func (t Tool) GetBundleForPlatform(platform Platform) *InstallMethod {
	v := t.GetDefaultVersion()
	if v == nil {
		return nil
	}
	methods := v.MethodsForPlatform(platform, false)
	if len(methods) > 0 {
		m := methods[0]
		return &m
	}
	// Legacy fallback: binaries map.
	if bin, ok := v.Binaries[platform]; ok {
		return &InstallMethod{
			Type:      MethodHTTP,
			Platforms: []Platform{platform},
			URL:       bin.URL,
			SHA256:    bin.SHA256,
			Size:      bin.Size,
		}
	}
	return nil
}

// LoadCatalog loads tools from YAML files in catalogDir.
func LoadCatalog(catalogDir string) ([]Tool, error) {
	if _, err := os.Stat(catalogDir); os.IsNotExist(err) {
		return nil, nil
	}

	return LoadToolsFromDir(catalogDir)
}

// GetMockTools returns an empty list. All tools are loaded from YAML.
func GetMockTools() []Tool {
	return nil
}

// ToolIDSet returns a set of all tool IDs in the catalog.
// Use this to build the knownToolIDs argument for search.Fetch().
func ToolIDSet(tools []Tool) map[string]struct{} {
	ids := make(map[string]struct{}, len(tools))
	for _, t := range tools {
		ids[t.ID] = struct{}{}
	}
	return ids
}

// FilterTools filters tools by search query (case-insensitive).
func FilterTools(tools []Tool, query string) []Tool {
	if query == "" {
		return tools
	}

	q := strings.ToLower(query)
	var filtered []Tool
	for _, tool := range tools {
		if strings.Contains(strings.ToLower(tool.Name), q) ||
			strings.Contains(strings.ToLower(tool.Description), q) ||
			strings.Contains(strings.ToLower(tool.ID), q) ||
			containsTag(tool.Tags, q) {
			filtered = append(filtered, tool)
		}
	}
	return filtered
}

func containsTag(tags []string, query string) bool {
	for _, tag := range tags {
		if strings.Contains(strings.ToLower(tag), query) {
			return true
		}
	}
	return false
}
