package settings

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Setting represents a single DPM setting.
type Setting struct {
	ID          string `yaml:"id"          json:"id"`
	Name        string `yaml:"name"        json:"name"`
	Description string `yaml:"description" json:"description"`
	Type        string `yaml:"type"        json:"type"` // "bool", "string", "path", "action"
	Value       string `yaml:"value"       json:"value"`
	Default     string `yaml:"default"     json:"default"`
}

// SettingsGroup represents a group of related settings.
type SettingsGroup struct {
	Name     string    `yaml:"name"     json:"name"`
	Settings []Setting `yaml:"settings" json:"settings"`
}

// configFile is the on-disk format written to ~/.dpm/config.yaml.
type configFile struct {
	Settings map[string]string `yaml:"settings"` // id → value
}

// Manager loads and persists settings from ~/.dpm/config.yaml.
type Manager struct {
	configPath string
	groups     []SettingsGroup
}

// NewManager creates a Manager rooted under dpmRoot (~/.dpm).
// It initialises the default groups and loads any saved overrides from disk.
func NewManager(dpmRoot string) (*Manager, error) {
	m := &Manager{
		configPath: filepath.Join(dpmRoot, "config.yaml"),
		groups:     defaultGroups(),
	}
	if err := m.load(); err != nil {
		return nil, err
	}
	return m, nil
}

// Groups returns the current settings groups (with saved values applied).
func (m *Manager) Groups() []SettingsGroup {
	return m.groups
}

// Get returns a setting by ID, or nil if not found.
func (m *Manager) Get(id string) *Setting {
	return GetSettingByID(m.groups, id)
}

// Set updates a setting's value in memory and persists to disk.
func (m *Manager) Set(id, value string) error {
	s := m.Get(id)
	if s == nil {
		return fmt.Errorf("settings: unknown setting %q", id)
	}
	s.Value = value
	return m.save()
}

// Toggle flips a bool setting and persists to disk.
func (m *Manager) Toggle(id string) error {
	s := m.Get(id)
	if s == nil {
		return fmt.Errorf("settings: unknown setting %q", id)
	}
	ToggleBoolSetting(s)
	return m.save()
}

// Reset resets a setting to its default value and persists to disk.
func (m *Manager) Reset(id string) error {
	s := m.Get(id)
	if s == nil {
		return fmt.Errorf("settings: unknown setting %q", id)
	}
	ResetToDefault(s)
	return m.save()
}

// load reads ~/.dpm/config.yaml and applies any saved values over the defaults.
// If the file does not exist, it is a no-op (defaults remain).
func (m *Manager) load() error {
	data, err := os.ReadFile(m.configPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("settings: read %s: %w", m.configPath, err)
	}

	var cfg configFile
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("settings: parse %s: %w", m.configPath, err)
	}

	for id, value := range cfg.Settings {
		if s := GetSettingByID(m.groups, id); s != nil {
			s.Value = value
		}
	}
	return nil
}

// save writes the current non-default values to ~/.dpm/config.yaml atomically.
func (m *Manager) save() error {
	if err := os.MkdirAll(filepath.Dir(m.configPath), 0o755); err != nil {
		return fmt.Errorf("settings: create config dir: %w", err)
	}

	cfg := configFile{Settings: make(map[string]string)}
	for _, group := range m.groups {
		for _, s := range group.Settings {
			// Action settings are UI triggers and should never be persisted.
			if s.Type == "action" {
				continue
			}
			if s.Value != s.Default {
				cfg.Settings[s.ID] = s.Value
			}
		}
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("settings: encode: %w", err)
	}

	tmp := m.configPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("settings: write temp: %w", err)
	}
	if err := os.Rename(tmp, m.configPath); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("settings: replace config: %w", err)
	}
	return nil
}

// defaultGroups returns the canonical set of settings with their defaults.
func defaultGroups() []SettingsGroup {
	return []SettingsGroup{
		{
			Name: "Dotfiles",
			Settings: []Setting{
				{
					ID:          "dotfiles-import-repo",
					Name:        "Import Dotfiles Repository",
					Description: "Bring your own dotfiles from GitHub/GitLab or an SSH/HTTPS git URL",
					Type:        "action",
					Value:       "",
					Default:     "",
				},
			},
		},
		{
			Name: "Cache & Storage",
			Settings: []Setting{
				{
					ID:          "cache-path",
					Name:        "Cache Directory",
					Description: "Where downloaded packages are cached",
					Type:        "path",
					Value:       "~/.dpm/cache",
					Default:     "~/.dpm/cache",
				},
				{
					ID:          "cache-size-limit",
					Name:        "Cache Size Limit",
					Description: "Maximum cache size in MB (0 = unlimited)",
					Type:        "string",
					Value:       "1024",
					Default:     "1024",
				},
				{
					ID:          "auto-clean",
					Name:        "Auto-clean old packages",
					Description: "Automatically remove packages older than 30 days",
					Type:        "bool",
					Value:       "true",
					Default:     "true",
				},
			},
		},
		{
			Name: "Security",
			Settings: []Setting{
				{
					ID:          "verify-checksums",
					Name:        "Verify Checksums",
					Description: "Always verify SHA256 checksums before installation",
					Type:        "bool",
					Value:       "true",
					Default:     "true",
				},
				{
					ID:          "strict-permissions",
					Name:        "Strict File Permissions",
					Description: "Set 0700 permissions on ~/.dpm directory",
					Type:        "bool",
					Value:       "true",
					Default:     "true",
				},
			},
		},
		{
			Name: "Network",
			Settings: []Setting{
				{
					ID:          "timeout",
					Name:        "Download Timeout",
					Description: "Download timeout in seconds",
					Type:        "string",
					Value:       "300",
					Default:     "300",
				},
				{
					ID:          "offline-mode",
					Name:        "Offline Mode",
					Description: "Work in offline mode (no network requests)",
					Type:        "bool",
					Value:       "false",
					Default:     "false",
				},
			},
		},
	}
}

// GetMockSettings is kept for backward compatibility with TUI code that hasn't
// migrated to Manager yet. New code should use NewManager instead.
func GetMockSettings() []SettingsGroup {
	return defaultGroups()
}

// GetSettingByID returns a setting by its ID from a slice of groups.
func GetSettingByID(groups []SettingsGroup, id string) *Setting {
	for i := range groups {
		for j := range groups[i].Settings {
			if groups[i].Settings[j].ID == id {
				return &groups[i].Settings[j]
			}
		}
	}
	return nil
}

// ToggleBoolSetting flips the value of a bool setting.
func ToggleBoolSetting(setting *Setting) {
	if setting.Type == "bool" {
		if setting.Value == "true" {
			setting.Value = "false"
		} else {
			setting.Value = "true"
		}
	}
}

// ResetToDefault resets a setting to its default value.
func ResetToDefault(setting *Setting) {
	setting.Value = setting.Default
}
