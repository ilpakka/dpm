package engine

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"dpm.fi/dpm/internal/adapter"
	"dpm.fi/dpm/internal/catalog"
	"dpm.fi/dpm/internal/dotfiles"
	"dpm.fi/dpm/internal/installer"
	"dpm.fi/dpm/internal/metadata"
	"dpm.fi/dpm/internal/profiles"
	"dpm.fi/dpm/internal/settings"
)

var ErrAlreadyInstalled = errors.New("tool version already installed")

// CatalogProvider defines the minimal catalog functionality the engine needs.
// The default implementation loads tools from the local catalog directory.
type CatalogProvider interface {
	LoadTools() ([]catalog.Tool, error)
}

// Installer prepares an installable bundle for the adapter.
type Installer interface {
	PrepareBundle(tool catalog.Tool, version catalog.ToolVersion, platform catalog.Platform) (adapter.Bundle, func(), error)
}

// ProfileProvider loads course profiles from YAML files or mock data.
type ProfileProvider interface {
	LoadProfiles() ([]profiles.Profile, error)
}

// DotfilesProvider loads dotfile definitions from YAML files or mock data.
type DotfilesProvider interface {
	LoadDotfiles() ([]dotfiles.Dotfile, error)
}

// Logger is intentionally minimal so the engine can accept log.Default() or a
// test double without pulling in any heavy logging framework.
type Logger interface {
	Printf(format string, v ...any)
}

// ChanLogger sends logs to a channel for TUI streaming.
type ChanLogger struct {
	LogChan chan<- string
}

func (cl *ChanLogger) Printf(format string, v ...any) {
	if cl.LogChan == nil {
		return
	}
	msg := fmt.Sprintf(format, v...)
	// Non-blocking send: if the TUI hasn't drained the channel yet, drop the
	// line rather than stalling the install pipeline.
	select {
	case cl.LogChan <- msg:
	default:
	}
}

// WithLogChan returns an opt that wires a ChanLogger to the given channel.
// Käytetään TUI-moodissa jotta logit virtaavat lipgloss-renderiin.
func WithLogChan(ch chan<- string) func(*Config) {
	return func(cfg *Config) {
		cfg.Logger = &ChanLogger{LogChan: ch}
	}
}

// Config allows dependency injection for tests and future implementations.
type Config struct {
	Adapter          adapter.IAdapter
	Installer        Installer
	Catalog          CatalogProvider
	Profiles         ProfileProvider
	Dotfiles         DotfilesProvider
	Metadata         *metadata.Store
	Settings         *settings.Manager
	Logger           Logger
	EmbeddedCatalog  fs.FS // optional: embedded catalog YAML files
	EmbeddedProfiles fs.FS // optional: embedded profile YAML files
	EmbeddedDotfiles fs.FS // optional: embedded dotfiles YAML files
}

// Engine coordinates the main DPM subsystems.
type Engine struct {
	adapter        adapter.IAdapter
	installer      Installer
	catalog        CatalogProvider
	profiles       ProfileProvider
	dotfileCatalog DotfilesProvider
	metadata       *metadata.Store
	settingsMgr    *settings.Manager
	logger         Logger

	// catalog cache — loaded once and reused for the engine's lifetime.
	mu          sync.Mutex
	cachedTools []catalog.Tool
	toolsLoaded bool
}

// New creates an Engine with default dependencies.
// Uses the multi-source installer (Resolver) by default, which picks the best
// available install method (http, apt, brew, pip, cargo) for each tool.
func New(opts ...func(*Config)) (*Engine, error) {
	a, err := adapter.NewAdapter()
	if err != nil {
		return nil, fmt.Errorf("engine: create adapter: %w", err)
	}

	// Detect bubble mode by checking if DPM_HOME is under /tmp/.
	dpmRoot := a.GetDPMRoot()
	bubble := strings.HasPrefix(dpmRoot, "/tmp/")

	sm, err := settings.NewManager(dpmRoot)
	if err != nil {
		return nil, fmt.Errorf("engine: init settings: %w", err)
	}

	cfg := Config{
		Adapter:  a,
		Metadata: metadata.NewStore(dpmRoot),
		Settings: sm,
		Logger:   log.Default(),
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.Installer == nil {
		cfg.Installer = installer.NewResolver(installer.ResolverConfig{
			Platform: a.Platform(),
			Bubble:   bubble,
			DPMRoot:  dpmRoot,
			Logger:   cfg.Logger,
		})
	}
	cfg.Catalog = &fileCatalog{dir: "catalog", fsys: cfg.EmbeddedCatalog}
	cfg.Profiles = &fileProfiles{dir: "profiles", fsys: cfg.EmbeddedProfiles}
	cfg.Dotfiles = &fileDotfiles{dir: "dotfiles", fsys: cfg.EmbeddedDotfiles}

	return NewWithConfig(cfg)
}

// NewBubble creates an Engine in bubble mode with an explicit root directory.
// The root should be under /tmp/ (e.g., /tmp/dpm-bubble-<id>/).
func NewBubble(bubbleRoot string, opts ...func(*Config)) (*Engine, error) {
	// Set DPM_HOME so the adapter picks it up.
	if err := os.Setenv("DPM_HOME", bubbleRoot); err != nil {
		return nil, fmt.Errorf("engine: set DPM_HOME: %w", err)
	}

	a, err := adapter.NewAdapter()
	if err != nil {
		return nil, fmt.Errorf("engine: create bubble adapter: %w", err)
	}

	bsm, err := settings.NewManager(bubbleRoot)
	if err != nil {
		return nil, fmt.Errorf("engine: init bubble settings: %w", err)
	}

	cfg := Config{
		Adapter:  a,
		Metadata: metadata.NewStore(bubbleRoot),
		Settings: bsm,
		Logger:   log.Default(),
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.Installer == nil {
		cfg.Installer = installer.NewResolver(installer.ResolverConfig{
			Platform: a.Platform(),
			Bubble:   true,
			DPMRoot:  bubbleRoot,
			Logger:   cfg.Logger,
		})
	}
	cfg.Catalog = &fileCatalog{dir: "catalog", fsys: cfg.EmbeddedCatalog}
	cfg.Profiles = &fileProfiles{dir: "profiles", fsys: cfg.EmbeddedProfiles}
	cfg.Dotfiles = &fileDotfiles{dir: "dotfiles", fsys: cfg.EmbeddedDotfiles}

	return NewWithConfig(cfg)
}

// NewWithConfig creates an Engine with explicit dependencies.
func NewWithConfig(cfg Config) (*Engine, error) {
	if cfg.Adapter == nil {
		return nil, fmt.Errorf("engine: adapter is required")
	}
	if cfg.Installer == nil {
		return nil, fmt.Errorf("engine: installer is required")
	}
	if cfg.Catalog == nil {
		return nil, fmt.Errorf("engine: catalog is required")
	}
	if cfg.Metadata == nil {
		cfg.Metadata = metadata.NewStore(cfg.Adapter.GetDPMRoot())
	}
	if cfg.Profiles == nil {
		cfg.Profiles = &fileProfiles{dir: "profiles"}
	}
	if cfg.Dotfiles == nil {
		cfg.Dotfiles = &fileDotfiles{dir: "dotfiles"}
	}
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}

	e := &Engine{
		adapter:        cfg.Adapter,
		installer:      cfg.Installer,
		catalog:        cfg.Catalog,
		profiles:       cfg.Profiles,
		dotfileCatalog: cfg.Dotfiles,
		metadata:       cfg.Metadata,
		settingsMgr:    cfg.Settings,
		logger:         cfg.Logger,
	}

	if err := e.initialize(); err != nil {
		return nil, err
	}

	e.logger.Printf("engine initialized: platform=%s root=%s metadata=%s", e.Platform(), e.adapter.GetDPMRoot(), e.metadata.Path())
	return e, nil
}

// NewWithAdapter preserves the old helper constructor used in tests and ad-hoc
// experiments. It fills in the other dependencies with defaults.
func NewWithAdapter(a adapter.IAdapter) *Engine {
	resolve := installer.NewResolver(installer.ResolverConfig{
		Platform: a.Platform(),
		DPMRoot:  a.GetDPMRoot(),
		Logger:   log.Default(),
	})
	e, err := NewWithConfig(Config{
		Adapter:   a,
		Installer: resolve,
		Catalog:   &fileCatalog{dir: "catalog"},
		Profiles:  &fileProfiles{dir: "profiles"},
		Metadata:  metadata.NewStore(a.GetDPMRoot()),
		Logger:    log.Default(),
	})
	if err != nil {
		return &Engine{
			adapter:   a,
			installer: resolve,
			catalog:   &fileCatalog{dir: "catalog"},
			profiles:  &fileProfiles{dir: "profiles"},
			metadata:  metadata.NewStore(a.GetDPMRoot()),
			logger:    log.Default(),
		}
	}
	return e
}

func (e *Engine) SetLogger(l Logger) {
	e.logger = l
}

// Platform returns the current platform string.
func (e *Engine) Platform() catalog.Platform {
	return e.adapter.Platform()
}

// GetDPMRoot returns the DPM root directory (e.g., ~/.dpm).
func (e *Engine) GetDPMRoot() string {
	return e.adapter.GetDPMRoot()
}

// GetSettings returns the Settings Manager for reading/writing DPM configuration.
// May be nil if the engine was constructed without settings (e.g. in tests).
func (e *Engine) GetSettings() *settings.Manager {
	return e.settingsMgr
}

// IsInPATH reports whether ~/.dpm/bin is in the current PATH.
func (e *Engine) IsInPATH() bool {
	return e.adapter.IsInPATH(filepath.Join(e.adapter.GetDPMRoot(), "bin"))
}

// AddToPATH adds ~/.dpm/bin to the user's shell rc file.
func (e *Engine) AddToPATH() error {
	return e.adapter.AddToPATH(filepath.Join(e.adapter.GetDPMRoot(), "bin"))
}

// Catalog loads tools through the configured catalog provider and overlays the
// installed state from ~/.dpm/metadata/installed.json.
func (e *Engine) Catalog() ([]catalog.Tool, error) {
	tools, err := e.loadCatalog()
	if err != nil {
		return nil, fmt.Errorf("engine: load catalog: %w", err)
	}

	if err := e.applyInstalledState(tools); err != nil {
		return nil, fmt.Errorf("engine: apply installed state: %w", err)
	}

	return tools, nil
}

// ListAllTools returns all tools from the catalog with installed state overlaid.
// It is an alias for Catalog() with a name that matches the query method contract.
func (e *Engine) ListAllTools() ([]catalog.Tool, error) {
	return e.Catalog()
}

// GetToolByID finds a single tool by ID from the catalog.
// Returns nil if no tool with that ID exists.
func (e *Engine) GetToolByID(id string) (*catalog.Tool, error) {
	tools, err := e.loadCatalog()
	if err != nil {
		return nil, fmt.Errorf("engine: GetToolByID: load catalog: %w", err)
	}
	return catalog.GetToolByID(tools, id), nil
}

// GetBundleForPlatform returns the best available InstallMethod for toolID on
// the current platform. Returns nil if the tool is not found or has no method
// for this platform.
func (e *Engine) GetBundleForPlatform(toolID string) (*catalog.InstallMethod, error) {
	tool, err := e.GetToolByID(toolID)
	if err != nil {
		return nil, err
	}
	if tool == nil {
		return nil, nil
	}
	return tool.GetBundleForPlatform(e.Platform()), nil
}

// InstalledRecords returns the metadata records currently stored on disk.
func (e *Engine) InstalledRecords() ([]metadata.InstalledTool, error) {
	records, err := e.metadata.List()
	if err != nil {
		return nil, fmt.Errorf("engine: list installed records: %w", err)
	}
	return records, nil
}

// UpdateStatus reports whether a tool has an update available.
type UpdateStatus struct {
	ToolID         string `json:"tool_id"`
	InstalledVer   string `json:"installed_ver"`
	AvailableVer   string `json:"available_ver,omitempty"`
	UpdateRequired bool   `json:"update_required"`        // true when catalog has a newer version
	NotInCatalog   bool   `json:"not_in_catalog"`         // true when tool is installed but no longer in catalog
}

// CheckUpdates compares every installed tool against the catalog and returns
// a status entry for each one. Tools that are already at the latest version
// are included in the result with UpdateRequired=false so callers can show
// a complete picture.
func (e *Engine) CheckUpdates() ([]UpdateStatus, error) {
	records, err := e.metadata.List()
	if err != nil {
		return nil, fmt.Errorf("engine: check updates: load metadata: %w", err)
	}

	tools, err := e.loadCatalog()
	if err != nil {
		return nil, fmt.Errorf("engine: check updates: load catalog: %w", err)
	}

	toolMap := make(map[string]catalog.Tool, len(tools))
	for _, t := range tools {
		toolMap[t.ID] = t
	}

	var statuses []UpdateStatus
	for _, record := range records {
		s := UpdateStatus{
			ToolID:       record.ToolID,
			InstalledVer: record.Version,
		}
		t, ok := toolMap[record.ToolID]
		if !ok {
			s.NotInCatalog = true
			statuses = append(statuses, s)
			continue
		}
		latest := t.GetDefaultVersion()
		if latest == nil {
			s.NotInCatalog = true
			statuses = append(statuses, s)
			continue
		}
		s.AvailableVer = latest.Version
		s.UpdateRequired = compareSemver(latest.Version, record.Version) > 0
		statuses = append(statuses, s)
	}
	return statuses, nil
}

// UpdateTool updates a single tool to the latest catalog version.
// It removes the installed version and installs the latest one.
// Returns ErrAlreadyInstalled (wrapped) if the installed version is already latest.
func (e *Engine) UpdateTool(toolID string) (*InstallResult, error) {
	tools, err := e.loadCatalog()
	if err != nil {
		return nil, fmt.Errorf("engine: update %s: load catalog: %w", toolID, err)
	}
	toolPtr := catalog.GetToolByID(tools, toolID)
	if toolPtr == nil {
		return nil, fmt.Errorf("engine: update %s: tool not found in catalog", toolID)
	}
	tool := *toolPtr

	latest := tool.GetDefaultVersion()
	if latest == nil {
		return nil, fmt.Errorf("engine: update %s: no version available in catalog", toolID)
	}

	records, err := e.metadata.List()
	if err != nil {
		return nil, fmt.Errorf("engine: update %s: load metadata: %w", toolID, err)
	}

	// Find the currently installed version(s) of this tool.
	var installedVer string
	for _, r := range records {
		if r.ToolID == toolID {
			installedVer = r.Version
			break
		}
	}

	if installedVer == "" {
		return nil, fmt.Errorf("engine: update %s: tool is not installed", toolID)
	}
	if installedVer == latest.Version {
		return nil, fmt.Errorf("engine: update %s: already at latest version %s", toolID, latest.Version)
	}

	e.logger.Printf("updating %s: %s → %s", toolID, installedVer, latest.Version)

	// Install the new version FIRST — if it fails the old version is still intact.
	result, err := e.InstallTool(tool, *latest)
	if err != nil {
		return nil, fmt.Errorf("engine: update %s: install new version: %w", toolID, err)
	}

	// Remove the old version only after the new one is confirmed installed.
	if err := e.RemoveTool(toolID, installedVer); err != nil {
		return nil, fmt.Errorf("engine: update %s: remove old version: %w", toolID, err)
	}

	e.logger.Printf("updated %s: %s → %s", toolID, installedVer, latest.Version)
	return result, nil
}

// InstallResult wraps adapter.InstallResult with additional engine-level info.
type InstallResult struct {
	adapter.InstallResult
	DryRun bool `json:"dry_run,omitempty"`
}

// InstallTool installs a tool version using the configured installer.
//
// For now the default installer still builds a tiny local stub archive so the
// rest of the install pipeline can be exercised without a real package host.
func (e *Engine) InstallTool(tool catalog.Tool, version catalog.ToolVersion) (*InstallResult, error) {
	if err := e.removeStaleMetadataIfNeeded(tool.ID, version.Version); err != nil {
		return nil, err
	}

	installed, err := e.metadata.IsInstalled(tool.ID, version.Version)
	if err != nil {
		return nil, fmt.Errorf("engine: check installed state for %s@%s: %w", tool.ID, version.Version, err)
	}
	if installed {
		return nil, fmt.Errorf("engine: %w: %s@%s", ErrAlreadyInstalled, tool.ID, version.Version)
	}

	bundle, cleanup, err := e.installer.PrepareBundle(tool, version, e.Platform())
	if err != nil {
		return nil, fmt.Errorf("engine: prepare bundle for %s@%s: %w", tool.ID, version.Version, err)
	}
	if cleanup != nil {
		defer cleanup()
	}

	result, err := e.adapter.InstallBundle(bundle, adapter.InstallOptions{
		ConfirmFunc: stdinConfirm,
	})
	if err != nil {
		return nil, fmt.Errorf("engine: install %s@%s: %w", tool.ID, version.Version, err)
	}

	record := metadata.InstalledTool{
		ToolID:      tool.ID,
		ToolName:    tool.Name,
		Version:     version.Version,
		Platform:    string(e.Platform()),
		InstallDir:  result.InstallDir,
		Symlinks:    append([]string(nil), result.Symlinks...),
		InstalledAt: time.Now().UTC().Format(time.RFC3339),
		SHA256:      bundle.SHA256,
		Verified:    bundle.Verified,
		Method:      bundle.Method,
	}
	if err := e.metadata.Upsert(record); err != nil {
		return nil, fmt.Errorf("engine: record installed metadata for %s@%s: %w", tool.ID, version.Version, err)
	}

	e.logger.Printf("installed %s@%s into %s", tool.ID, version.Version, result.InstallDir)
	return &InstallResult{InstallResult: *result}, nil
}

// RemoveTool removes an installed tool version by deleting its install
// directory and the symlinks it created in ~/.dpm/bin/, then updates metadata.
func (e *Engine) RemoveTool(toolID, version string) error {
	record, err := e.metadata.Find(toolID, version)
	if err != nil {
		return fmt.Errorf("engine: load metadata for %s@%s: %w", toolID, version, err)
	}

	dpmRoot := e.adapter.GetDPMRoot()
	installDir := filepath.Join(dpmRoot, "tools", toolID, version)
	if record != nil && strings.TrimSpace(record.InstallDir) != "" {
		installDir = record.InstallDir
	}

	// Safety check: refuse to delete anything outside dpmRoot.
	// A corrupt or malicious metadata record must not be able to direct RemoveAll
	// at an arbitrary path (e.g. "/" or "/usr").
	if !pathWithin(installDir, dpmRoot) {
		return fmt.Errorf("engine: remove %s@%s: install dir %q is outside dpm root %q, refusing to delete",
			toolID, version, installDir, dpmRoot)
	}

	if err := os.RemoveAll(installDir); err != nil {
		return fmt.Errorf("engine: remove %s@%s: remove install dir: %w", toolID, version, err)
	}

	removedLinks := make([]string, 0)
	if record != nil && len(record.Symlinks) > 0 {
		for _, linkPath := range record.Symlinks {
			if removed, err := removeSymlinkIfOwned(linkPath, installDir); err != nil {
				return fmt.Errorf("engine: remove %s@%s: remove symlink %s: %w", toolID, version, linkPath, err)
			} else if removed {
				removedLinks = append(removedLinks, linkPath)
			}
		}
	} else {
		linkPath := filepath.Join(dpmRoot, "bin", toolID)
		if removed, err := removeSymlinkIfOwned(linkPath, installDir); err != nil {
			return fmt.Errorf("engine: remove %s@%s: remove symlink %s: %w", toolID, version, linkPath, err)
		} else if removed {
			removedLinks = append(removedLinks, linkPath)
		}
	}

	if err := e.metadata.Remove(toolID, version); err != nil {
		return fmt.Errorf("engine: remove metadata for %s@%s: %w", toolID, version, err)
	}

	e.logger.Printf("removed %s@%s (install dir: %s, symlinks removed: %d)", toolID, version, installDir, len(removedLinks))
	return nil
}

// RestoreResult summarises what Restore did.
type RestoreResult struct {
	RemovedTools    []string `json:"removed_tools,omitempty"`    // tool IDs that were uninstalled
	RemovedSymlinks []string `json:"removed_symlinks,omitempty"` // symlink paths that were deleted
	RemovedDirs     []string `json:"removed_dirs,omitempty"`     // dpmRoot sub-directories that were removed
	Errors          []error  `json:"errors,omitempty"`           // non-fatal per-step errors (removal continues past them)
}

// Restore removes every DPM-managed tool, wipes the ~/.dpm/tools/ and
// ~/.dpm/bin/ directories, and resets the metadata store to empty.
// It does NOT remove ~/.dpm/cache (downloads can be reused).
// It does NOT remove dotfiles backups — those live in the user's home dir.
//
// This is the "clean slate" command described in scope.md as `dpm restore`.
func (e *Engine) Restore() (*RestoreResult, error) {
	records, err := e.metadata.List()
	if err != nil {
		return nil, fmt.Errorf("engine: restore: load metadata: %w", err)
	}

	result := &RestoreResult{}
	dpmRoot := e.adapter.GetDPMRoot()

	// Remove each installed tool one by one so metadata stays consistent.
	for _, record := range records {
		if err := e.RemoveTool(record.ToolID, record.Version); err != nil {
			// Non-fatal: record the error and continue with the next tool.
			result.Errors = append(result.Errors, fmt.Errorf("remove %s@%s: %w", record.ToolID, record.Version, err))
			e.logger.Printf("restore: warning: %v", err)
			continue
		}
		result.RemovedTools = append(result.RemovedTools, record.ToolID+"@"+record.Version)
	}

	// Wipe tools/ and bin/ entirely — catches any orphaned files not in metadata.
	for _, sub := range []string{"tools", "bin"} {
		dir := filepath.Join(dpmRoot, sub)
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}
		if err := os.RemoveAll(dir); err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("remove %s/: %w", sub, err))
			e.logger.Printf("restore: warning: remove %s: %v", dir, err)
			continue
		}
		result.RemovedDirs = append(result.RemovedDirs, dir)
	}

	// Re-create empty tools/ and bin/ so the engine can be used again immediately.
	for _, sub := range []string{"tools", "bin"} {
		dir := filepath.Join(dpmRoot, sub)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			result.Errors = append(result.Errors, fmt.Errorf("recreate %s/: %w", sub, err))
		}
	}

	e.logger.Printf("restore complete: %d tools removed, %d errors", len(result.RemovedTools), len(result.Errors))
	return result, nil
}

// initialize ensures the base DPM directory layout exists and that the
// installed.json metadata file is present.
func (e *Engine) initialize() error {
	root := e.adapter.GetDPMRoot()
	if root == "" {
		return fmt.Errorf("engine: initialize: adapter returned empty DPM root")
	}

	dirs := []string{
		root,
		filepath.Join(root, "cache"),
		filepath.Join(root, "metadata"),
		filepath.Join(root, "tools"),
		filepath.Join(root, "bin"),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("engine: initialize: create %s: %w", dir, err)
		}
	}

	if err := e.metadata.Ensure(); err != nil {
		return fmt.Errorf("engine: initialize metadata: %w", err)
	}

	return nil
}

func (e *Engine) applyInstalledState(tools []catalog.Tool) error {
	records, err := e.metadata.List()
	if err != nil {
		return fmt.Errorf("engine: apply installed state: %w", err)
	}

	installed := make(map[string]map[string]struct{}, len(records))
	for _, record := range records {
		versions, ok := installed[record.ToolID]
		if !ok {
			versions = make(map[string]struct{})
			installed[record.ToolID] = versions
		}
		versions[record.Version] = struct{}{}
	}

	for i := range tools {
		versions := installed[tools[i].ID]
		for j := range tools[i].Versions {
			_, ok := versions[tools[i].Versions[j].Version]
			tools[i].Versions[j].Installed = ok
		}
	}

	return nil
}

func (e *Engine) removeStaleMetadataIfNeeded(toolID, version string) error {
	record, err := e.metadata.Find(toolID, version)
	if err != nil {
		return fmt.Errorf("engine: load metadata for %s@%s: %w", toolID, version, err)
	}
	if record == nil {
		return nil
	}

	installDir := strings.TrimSpace(record.InstallDir)
	if installDir == "" {
		e.logger.Printf("stale metadata detected for %s@%s: empty install_dir, removing record", toolID, version)
		return e.metadata.Remove(toolID, version)
	}

	if _, err := os.Stat(installDir); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("engine: stat install dir for %s@%s: %w", toolID, version, err)
	}

	e.logger.Printf("stale metadata detected for %s@%s: %s does not exist, removing record", toolID, version, installDir)
	if err := e.metadata.Remove(toolID, version); err != nil {
		return fmt.Errorf("engine: remove stale metadata for %s@%s: %w", toolID, version, err)
	}
	return nil
}

func removeSymlinkIfOwned(linkPath, installDir string) (bool, error) {
	target, err := os.Readlink(linkPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}

	if !pathWithin(target, installDir) {
		return false, nil
	}

	if err := os.Remove(linkPath); err != nil && !os.IsNotExist(err) {
		return false, err
	}
	return true, nil
}

// fileCatalog loads tools from a local directory, falling back to embedded data.
type fileCatalog struct {
	dir  string
	fsys fs.FS // embedded FS (optional)
}

func (c *fileCatalog) LoadTools() ([]catalog.Tool, error) {
	// Local dir takes priority (development / override).
	if _, err := os.Stat(c.dir); err == nil {
		return catalog.LoadToolsFromDir(c.dir)
	}
	// Fall back to embedded data.
	if c.fsys != nil {
		return catalog.LoadToolsFromFS(c.fsys, c.dir)
	}
	return nil, nil
}

// fileProfiles loads profiles from a local directory, falling back to embedded data.
type fileProfiles struct {
	dir  string
	fsys fs.FS // embedded FS (optional)
}

func (p *fileProfiles) LoadProfiles() ([]profiles.Profile, error) {
	// Local dir takes priority.
	if _, err := os.Stat(p.dir); err == nil {
		return profiles.LoadProfilesFromDir(p.dir)
	}
	// Fall back to embedded data.
	if p.fsys != nil {
		return profiles.LoadProfilesFromFS(p.fsys, p.dir)
	}
	return nil, nil
}

// fileDotfiles loads dotfiles from a local directory, falling back to embedded data or mock data.
type fileDotfiles struct {
	dir  string
	fsys fs.FS // embedded FS (optional)
}

func (d *fileDotfiles) LoadDotfiles() ([]dotfiles.Dotfile, error) {
	// Local dir takes priority.
	if _, err := os.Stat(d.dir); err == nil {
		return dotfiles.LoadDotfilesFromDir(d.dir)
	}
	// Fall back to embedded data.
	if d.fsys != nil {
		return dotfiles.LoadDotfilesFromFS(d.fsys, d.dir)
	}
	return nil, nil
}

// ---------------------------------------------------------------------------
// Profile application
// ---------------------------------------------------------------------------

// ToolInstallStatus tracks the result of installing one tool within a profile.
type ToolInstallStatus struct {
	ToolID  string `json:"tool_id"`
	Version string `json:"version"`
	Success bool   `json:"success"`
	Skipped bool   `json:"skipped,omitempty"` // true if already installed
	Err     error  `json:"error,omitempty"`
}

// DotfileInstallStatus tracks the result of applying one dotfile within a profile.
type DotfileInstallStatus struct {
	DotfileID string `json:"dotfile_id"`
	Success   bool   `json:"success"`
	Err       error  `json:"error,omitempty"`
}

// ProfileResult summarises the outcome of applying a profile.
type ProfileResult struct {
	ProfileID string                 `json:"profile_id"`
	Tools     []ToolInstallStatus    `json:"tools,omitempty"`
	Dotfiles  []DotfileInstallStatus `json:"dotfiles,omitempty"`
}

// FindProfileByCourseCode looks up a profile by its CourseCode or ID field.
// Returns nil if no match is found.
func (e *Engine) FindProfileByCourseCode(code string) (*profiles.Profile, error) {
	profs, err := e.profiles.LoadProfiles()
	if err != nil {
		return nil, fmt.Errorf("engine: find profile %q: %w", code, err)
	}
	return profiles.FindByCourseCode(profs, code), nil
}

// Profiles loads profiles through the configured profile provider.
func (e *Engine) Profiles() ([]profiles.Profile, error) {
	profs, err := e.profiles.LoadProfiles()
	if err != nil {
		return nil, fmt.Errorf("engine: load profiles: %w", err)
	}
	return profs, nil
}

// LoadDotfiles loads dotfile definitions through the configured dotfiles provider.
func (e *Engine) LoadDotfiles() ([]dotfiles.Dotfile, error) {
	dfs, err := e.dotfileCatalog.LoadDotfiles()
	if err != nil {
		return nil, fmt.Errorf("engine: load dotfiles: %w", err)
	}
	return dfs, nil
}

// ApplyProfile installs all tools listed in a profile. It loads the catalog,
// resolves each tool reference, and calls InstallTool sequentially.
// Tools that are already installed are skipped (not treated as errors).
func (e *Engine) ApplyProfile(profile profiles.Profile) (*ProfileResult, error) {
	tools, err := e.loadCatalog()
	if err != nil {
		return nil, fmt.Errorf("engine: apply profile %s: load catalog: %w", profile.ID, err)
	}

	// Build a lookup map by tool ID.
	toolMap := make(map[string]catalog.Tool, len(tools))
	for _, t := range tools {
		toolMap[t.ID] = t
	}

	result := &ProfileResult{ProfileID: profile.ID}

	// Iterate ToolRefs directly (supports multiple versions of same tool).
	// Fall back to Tools list for entries without a ToolRef.
	refs := profile.ToolRefs
	// Add any tools from the plain Tools list that aren't already in ToolRefs.
	refIDs := make(map[string]struct{}, len(refs))
	for _, r := range refs {
		refIDs[r.ID] = struct{}{}
	}
	for _, id := range profile.Tools {
		if _, ok := refIDs[id]; !ok {
			refs = append(refs, profiles.ToolRef{ID: id})
		}
	}

	for _, ref := range refs {
		tool, ok := toolMap[ref.ID]
		if !ok {
			status := ToolInstallStatus{
				ToolID: ref.ID,
				Err:    fmt.Errorf("tool %q not found in catalog", ref.ID),
			}
			result.Tools = append(result.Tools, status)
			e.logger.Printf("profile %s: skipping %s — not in catalog", profile.ID, ref.ID)
			continue
		}

		var version *catalog.ToolVersion
		if ref.MajorVersion > 0 {
			version = tool.GetVersionByMajor(ref.MajorVersion)
		}
		if version == nil {
			version = tool.GetDefaultVersion()
		}
		if version == nil {
			status := ToolInstallStatus{
				ToolID: ref.ID,
				Err:    fmt.Errorf("no version available for %s", ref.ID),
			}
			result.Tools = append(result.Tools, status)
			continue
		}

		e.logger.Printf("profile %s: installing %s@%s", profile.ID, ref.ID, version.Version)
		_, err := e.InstallTool(tool, *version)
		status := ToolInstallStatus{
			ToolID:  ref.ID,
			Version: version.Version,
		}
		if err != nil {
			if errors.Is(err, ErrAlreadyInstalled) {
				status.Skipped = true
				status.Success = true
				e.logger.Printf("profile %s: %s@%s already installed, skipping", profile.ID, ref.ID, version.Version)
			} else {
				status.Err = err
				e.logger.Printf("profile %s: failed to install %s@%s: %v", profile.ID, ref.ID, version.Version, err)
			}
		} else {
			status.Success = true
		}
		result.Tools = append(result.Tools, status)
	}

	// Apply any dotfiles listed in the profile.
	if len(profile.Dotfiles) > 0 {
		allDotfiles, err := e.dotfileCatalog.LoadDotfiles()
		if err != nil {
			e.logger.Printf("profile %s: failed to load dotfiles catalog: %v", profile.ID, err)
		} else {
			dfMap := make(map[string]dotfiles.Dotfile, len(allDotfiles))
			for _, df := range allDotfiles {
				dfMap[df.ID] = df
			}
			for _, dfID := range profile.Dotfiles {
				df, ok := dfMap[dfID]
				if !ok {
					e.logger.Printf("profile %s: dotfile %q not found in catalog, skipping", profile.ID, dfID)
					result.Dotfiles = append(result.Dotfiles, DotfileInstallStatus{
						DotfileID: dfID,
						Err:       fmt.Errorf("dotfile %q not found in catalog", dfID),
					})
					continue
				}
				e.logger.Printf("profile %s: applying dotfile %s", profile.ID, dfID)
				_, dfErr := e.InstallDotfile(df)
				result.Dotfiles = append(result.Dotfiles, DotfileInstallStatus{
					DotfileID: dfID,
					Success:   dfErr == nil,
					Err:       dfErr,
				})
				if dfErr != nil {
					e.logger.Printf("profile %s: failed to apply dotfile %s: %v", profile.ID, dfID, dfErr)
				}
			}
		}
	}

	return result, nil
}

// InstallDotfile clones a dotfile's source repo and applies its file mappings.
func (e *Engine) InstallDotfile(df dotfiles.Dotfile) (*dotfiles.ApplyResult, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("engine: get home dir: %w", err)
	}

	result, err := dotfiles.Apply(df, dotfiles.ApplyOptions{
		DPMRoot: e.adapter.GetDPMRoot(),
		HomeDir: homeDir,
		Adapter: e.adapter,
	})
	if err != nil {
		return nil, fmt.Errorf("engine: install dotfile %s: %w", df.ID, err)
	}

	e.logger.Printf("installed dotfile %s: %d files applied", df.ID, len(result.Applied))
	return result, nil
}

// loadCatalog loads tools from the configured catalog provider, caching the
// result so the YAML is only parsed once per engine lifetime.
func (e *Engine) loadCatalog() ([]catalog.Tool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.toolsLoaded {
		tools, err := e.catalog.LoadTools()
		if err != nil {
			return nil, err
		}
		e.cachedTools = tools
		e.toolsLoaded = true
	}
	out := make([]catalog.Tool, len(e.cachedTools))
	copy(out, e.cachedTools)
	return out, nil
}

// compareSemver compares two "MAJOR.MINOR.PATCH" version strings.
// Returns 1 if a > b, -1 if a < b, 0 if equal.
// Falls back to lexicographic comparison if either string is not valid semver.
func compareSemver(a, b string) int {
	parse := func(s string) [3]int {
		parts := strings.SplitN(s, ".", 3)
		var out [3]int
		for i := 0; i < 3 && i < len(parts); i++ {
			n, err := strconv.Atoi(parts[i])
			if err != nil {
				return [3]int{-1, -1, -1} // signal parse failure
			}
			out[i] = n
		}
		return out
	}
	av, bv := parse(a), parse(b)
	// If either failed to parse, fall back to string comparison.
	if av[0] == -1 || bv[0] == -1 {
		if a > b {
			return 1
		} else if a < b {
			return -1
		}
		return 0
	}
	for i := 0; i < 3; i++ {
		if av[i] > bv[i] {
			return 1
		}
		if av[i] < bv[i] {
			return -1
		}
	}
	return 0
}

// stdinConfirm prints prompt to stdout and returns true only if the user types "y" or "yes".
func stdinConfirm(prompt string) bool {
	fmt.Printf("\n%s [y/N] ", prompt)
	var response string
	fmt.Scanln(&response) //nolint:errcheck
	return strings.EqualFold(strings.TrimSpace(response), "y") ||
		strings.EqualFold(strings.TrimSpace(response), "yes")
}

func pathWithin(path, parent string) bool {
	cleanPath := filepath.Clean(path)
	cleanParent := filepath.Clean(parent)

	rel, err := filepath.Rel(cleanParent, cleanPath)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

