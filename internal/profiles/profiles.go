package profiles

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ToolRef references a specific tool and optionally a major version.
// If MajorVersion is 0, the engine picks the default (latest) version.
type ToolRef struct {
	ID           string `yaml:"id"                       json:"id"`
	MajorVersion int    `yaml:"major_version,omitempty"  json:"major_version,omitempty"` // 0 = use default
}

// Profile represents a curated tool bundle (course profile / "toolbook").
type Profile struct {
	ID          string    `yaml:"id"          json:"id"`
	Name        string    `yaml:"name"        json:"name"`
	Description string    `yaml:"description" json:"description"`
	Category    string    `yaml:"category"    json:"category,omitempty"`    // e.g., "security", "development", "data"
	CourseCode  string    `yaml:"course_code" json:"course_code,omitempty"` // official course code, e.g. "ICI012AS3A"
	Tools       []string  `yaml:"tools"       json:"tools,omitempty"`       // Simple tool ID list (backward compat)
	ToolRefs    []ToolRef `yaml:"tool_refs"   json:"tool_refs,omitempty"`   // Detailed tool references with optional version
	Dotfiles    []string  `yaml:"dotfiles"    json:"dotfiles,omitempty"`    // List of dotfile IDs to install
	Version     string    `yaml:"version"     json:"version,omitempty"`     // Profile version
	Installed   bool      `json:"installed"`                                // Runtime state
}

// AllToolIDs returns a deduplicated list of tool IDs from both Tools and ToolRefs.
func (p Profile) AllToolIDs() []string {
	seen := make(map[string]struct{})
	var ids []string
	for _, id := range p.Tools {
		if _, ok := seen[id]; !ok {
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	for _, ref := range p.ToolRefs {
		if _, ok := seen[ref.ID]; !ok {
			seen[ref.ID] = struct{}{}
			ids = append(ids, ref.ID)
		}
	}
	return ids
}

// GetToolRef returns the ToolRef for a given tool ID, or a default with MajorVersion=0.
func (p Profile) GetToolRef(toolID string) ToolRef {
	for _, ref := range p.ToolRefs {
		if ref.ID == toolID {
			return ref
		}
	}
	return ToolRef{ID: toolID}
}

// LoadProfiles loads profiles from YAML files in dir.
// Returns an empty list (not an error) if the directory does not exist.
func LoadProfiles(dir string) ([]Profile, error) {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return nil, nil
	}
	return LoadProfilesFromDir(dir)
}

// LoadProfilesFromDir reads all *.yaml files from dir and its subdirectories.
// Files named "template.yaml" or starting with "_" are skipped.
func LoadProfilesFromDir(dir string) ([]Profile, error) {
	var profs []Profile

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".yaml") || isTemplateName(name) {
			return nil
		}
		p, err := loadProfileFile(path)
		if err != nil {
			return fmt.Errorf("profiles: load %s: %w", path, err)
		}
		if p.ID == "" {
			return fmt.Errorf("profiles: %s: missing required field 'id'", path)
		}
		profs = append(profs, p)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return profs, nil
}

// isTemplateName returns true for file names that the loader should skip.
func isTemplateName(name string) bool {
	return name == "template.yaml" || name == "example.yaml" || strings.HasPrefix(name, "_")
}

// LoadProfilesFromFS reads all *.yaml files from an embed.FS (or any fs.FS),
// including subdirectories. Template files are skipped.
func LoadProfilesFromFS(fsys fs.FS, dir string) ([]Profile, error) {
	var profs []Profile

	err := fs.WalkDir(fsys, dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".yaml") || isTemplateName(name) {
			return nil
		}
		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return fmt.Errorf("profiles: read embedded %s: %w", path, err)
		}
		var p Profile
		if err := yaml.Unmarshal(data, &p); err != nil {
			return fmt.Errorf("profiles: parse embedded %s: %w", path, err)
		}
		if p.ID == "" {
			return fmt.Errorf("profiles: embedded %s: missing required field 'id'", path)
		}
		profs = append(profs, p)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return profs, nil
}

func loadProfileFile(path string) (Profile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Profile{}, err
	}
	var p Profile
	if err := yaml.Unmarshal(data, &p); err != nil {
		return Profile{}, err
	}
	return p, nil
}

// GetMockProfiles returns an empty list. All profiles are loaded from YAML.
func GetMockProfiles() []Profile {
	return nil
}

// FilterProfiles filters profiles by search query (case-insensitive).
func FilterProfiles(profiles []Profile, query string) []Profile {
	if query == "" {
		return profiles
	}

	q := strings.ToLower(query)
	var filtered []Profile
	for _, p := range profiles {
		if strings.Contains(strings.ToLower(p.Name), q) ||
			strings.Contains(strings.ToLower(p.Description), q) ||
			strings.Contains(strings.ToLower(p.ID), q) ||
			strings.Contains(strings.ToLower(p.Category), q) {
			filtered = append(filtered, p)
		}
	}
	return filtered
}

// FindByCourseCode returns the first profile whose CourseCode or ID matches
// the given code (case-insensitive). Returns nil if nothing matches.
func FindByCourseCode(profiles []Profile, code string) *Profile {
	code = strings.ToUpper(strings.TrimSpace(code))
	for i := range profiles {
		if strings.ToUpper(profiles[i].CourseCode) == code ||
			strings.ToUpper(profiles[i].ID) == code {
			return &profiles[i]
		}
	}
	return nil
}

// GetProfilesByCategory returns profiles for a specific category.
func GetProfilesByCategory(profiles []Profile, category string) []Profile {
	var result []Profile
	for _, p := range profiles {
		if p.Category == category {
			result = append(result, p)
		}
	}
	return result
}
