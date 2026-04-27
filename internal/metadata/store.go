package metadata

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// InstalledTool represents one installed tool version tracked by DPM.
type InstalledTool struct {
	ToolID      string   `json:"tool_id"`
	ToolName    string   `json:"tool_name"`
	Version     string   `json:"version"`
	Platform    string   `json:"platform"`
	InstallDir  string   `json:"install_dir"`
	Symlinks    []string `json:"symlinks,omitempty"`
	InstalledAt string   `json:"installed_at"`

	// Provenance fields recorded at install time.
	SHA256   string `json:"sha256,omitempty"`   // hex-encoded SHA-256 that was verified
	Verified bool   `json:"verified,omitempty"` // true when SHA256 was successfully checked
	Method   string `json:"method,omitempty"`   // install method: "http", "apt", "brew", "pip", "cargo"
}

// State is the on-disk JSON shape used in ~/.dpm/metadata/installed.json.
type State struct {
	Installed []InstalledTool `json:"installed"`
}

// Store manages the installed.json metadata file.
type Store struct {
	path string
	mu   sync.Mutex
}

// NewStore builds a metadata store rooted under ~/.dpm.
func NewStore(dpmRoot string) *Store {
	return &Store{path: filepath.Join(dpmRoot, "metadata", "installed.json")}
}

// Path returns the absolute path to the installed.json file.
func (s *Store) Path() string {
	return s.path
}

// Ensure creates the metadata directory and an empty installed.json file if needed.
func (s *Store) Ensure() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("metadata: ensure directory: %w", err)
	}

	if _, err := os.Stat(s.path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("metadata: stat %s: %w", s.path, err)
	}

	return s.writeLocked(State{Installed: []InstalledTool{}})
}

// Load reads the current installed state from disk.
func (s *Store) Load() (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.loadLocked()
}

// List returns every installed record.
func (s *Store) List() ([]InstalledTool, error) {
	state, err := s.Load()
	if err != nil {
		return nil, err
	}
	return append([]InstalledTool(nil), state.Installed...), nil
}

// Find returns a single installed record for toolID@version.
func (s *Store) Find(toolID, version string) (*InstalledTool, error) {
	state, err := s.Load()
	if err != nil {
		return nil, err
	}

	for _, record := range state.Installed {
		if sameInstall(record, toolID, version) {
			copyRecord := record
			copyRecord.Symlinks = append([]string(nil), record.Symlinks...)
			return &copyRecord, nil
		}
	}
	return nil, nil
}

// IsInstalled reports whether toolID@version is recorded as installed.
func (s *Store) IsInstalled(toolID, version string) (bool, error) {
	record, err := s.Find(toolID, version)
	if err != nil {
		return false, err
	}
	return record != nil, nil
}

// Upsert inserts or updates a single installed record.
func (s *Store) Upsert(record InstalledTool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.loadLocked()
	if err != nil {
		return fmt.Errorf("metadata: upsert: load state: %w", err)
	}

	record.ToolID = strings.TrimSpace(record.ToolID)
	record.Version = strings.TrimSpace(record.Version)
	record.ToolName = strings.TrimSpace(record.ToolName)
	record.Platform = strings.TrimSpace(record.Platform)
	record.InstallDir = strings.TrimSpace(record.InstallDir)
	record.Symlinks = dedupeStrings(record.Symlinks)

	if record.ToolID == "" || record.Version == "" {
		return fmt.Errorf("metadata: upsert requires tool_id and version")
	}

	replaced := false
	for i := range state.Installed {
		if sameInstall(state.Installed[i], record.ToolID, record.Version) {
			state.Installed[i] = record
			replaced = true
			break
		}
	}
	if !replaced {
		state.Installed = append(state.Installed, record)
	}

	sortInstalled(state.Installed)
	return s.writeLocked(state)
}

// Remove deletes a single installed record.
func (s *Store) Remove(toolID, version string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.loadLocked()
	if err != nil {
		return fmt.Errorf("metadata: remove: load state: %w", err)
	}

	filtered := state.Installed[:0]
	for _, record := range state.Installed {
		if !sameInstall(record, toolID, version) {
			filtered = append(filtered, record)
		}
	}
	state.Installed = filtered
	sortInstalled(state.Installed)

	return s.writeLocked(state)
}

func (s *Store) loadLocked() (State, error) {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return State{}, fmt.Errorf("metadata: ensure directory: %w", err)
	}

	if _, err := os.Stat(s.path); os.IsNotExist(err) {
		if err := s.writeLocked(State{Installed: []InstalledTool{}}); err != nil {
			return State{}, err
		}
	}

	data, err := os.ReadFile(s.path)
	if err != nil {
		return State{}, fmt.Errorf("metadata: read %s: %w", s.path, err)
	}

	if len(strings.TrimSpace(string(data))) == 0 {
		return State{Installed: []InstalledTool{}}, nil
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return State{}, fmt.Errorf("metadata: parse %s: %w", s.path, err)
	}
	if state.Installed == nil {
		state.Installed = []InstalledTool{}
	}
	return state, nil
}

func (s *Store) writeLocked(state State) error {
	if state.Installed == nil {
		state.Installed = []InstalledTool{}
	}
	sortInstalled(state.Installed)

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("metadata: encode %s: %w", s.path, err)
	}
	data = append(data, '\n')

	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0o600); err != nil {
		return fmt.Errorf("metadata: write temp %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("metadata: replace %s: %w", s.path, err)
	}
	return nil
}

func sortInstalled(records []InstalledTool) {
	sort.Slice(records, func(i, j int) bool {
		if records[i].ToolID == records[j].ToolID {
			return records[i].Version < records[j].Version
		}
		return records[i].ToolID < records[j].ToolID
	})
}

func sameInstall(record InstalledTool, toolID, version string) bool {
	return record.ToolID == strings.TrimSpace(toolID) && record.Version == strings.TrimSpace(version)
}

func dedupeStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
