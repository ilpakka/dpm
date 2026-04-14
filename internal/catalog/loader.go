package catalog

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadToolsFromDir reads all *.yaml files from dir and its subdirectories,
// parses them into Tools, and validates that no two files share the same tool ID.
// Files named "template.yaml" or starting with "_" are skipped.
func LoadToolsFromDir(dir string) ([]Tool, error) {
	var tools []Tool
	seen := make(map[string]string) // tool ID → file path that defined it

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".yaml") {
			return nil
		}
		if isTemplateName(name) {
			return nil
		}

		t, err := loadToolFile(path)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		if prev, dup := seen[t.ID]; dup {
			return fmt.Errorf("duplicate tool id %q: defined in both %s and %s", t.ID, prev, path)
		}
		seen[t.ID] = path
		tools = append(tools, t)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return tools, nil
}

// LoadToolsFromFS reads all *.yaml files from an embed.FS (or any fs.FS),
// including subdirectories. Template files and duplicate IDs are rejected.
func LoadToolsFromFS(fsys fs.FS, dir string) ([]Tool, error) {
	var tools []Tool
	seen := make(map[string]string) // tool ID → path that defined it

	err := fs.WalkDir(fsys, dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".yaml") {
			return nil
		}
		if isTemplateName(name) {
			return nil
		}

		data, err := fs.ReadFile(fsys, path)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", path, err)
		}
		var t Tool
		if err := yaml.Unmarshal(data, &t); err != nil {
			return fmt.Errorf("parse embedded %s: %w", path, err)
		}
		if err := validateTool(t); err != nil {
			return fmt.Errorf("embedded %s: %w", path, err)
		}
		if prev, dup := seen[t.ID]; dup {
			return fmt.Errorf("duplicate tool id %q: defined in both %s and %s", t.ID, prev, path)
		}
		seen[t.ID] = path
		tools = append(tools, t)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return tools, nil
}

func loadToolFile(path string) (Tool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Tool{}, err
	}

	var t Tool
	if err := yaml.Unmarshal(data, &t); err != nil {
		return Tool{}, err
	}

	if err := validateTool(t); err != nil {
		return Tool{}, err
	}
	return t, nil
}

// isTemplateName returns true for YAML filenames that should be skipped by the
// loader: "template.yaml", any file starting with "_", or "example.yaml".
func isTemplateName(name string) bool {
	return name == "template.yaml" || name == "example.yaml" || strings.HasPrefix(name, "_")
}

// validateTool checks that a parsed Tool has the fields required for installation.
func validateTool(t Tool) error {
	if strings.TrimSpace(t.ID) == "" {
		return fmt.Errorf("missing required field 'id'")
	}
	if !toolIDPattern.MatchString(t.ID) {
		return fmt.Errorf("tool id %q: must match ^[a-z0-9][a-z0-9-]*$ (lowercase, no spaces)", t.ID)
	}
	if strings.TrimSpace(t.Name) == "" {
		return fmt.Errorf("tool %q: missing required field 'name'", t.ID)
	}
	latestCount := 0
	for i, v := range t.Versions {
		if strings.TrimSpace(v.Version) == "" {
			return fmt.Errorf("tool %q: version[%d]: missing required field 'version'", t.ID, i)
		}
		if v.IsLatest {
			latestCount++
		}
		for j, m := range v.InstallMethods {
			if err := validateInstallMethod(t.ID, v.Version, j, m); err != nil {
				return err
			}
		}
	}
	if latestCount > 1 {
		return fmt.Errorf("tool %q: more than one version has is_latest: true", t.ID)
	}
	return nil
}

// toolIDPattern enforces lowercase alphanumeric tool IDs, optionally with hyphens.
var toolIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

var knownMethodTypes = map[MethodType]bool{
	MethodHTTP:  true,
	MethodApt:   true,
	MethodBrew:  true,
	MethodPip:   true,
	MethodCargo: true,
}

func validateInstallMethod(toolID, version string, idx int, m InstallMethod) error {
	if !knownMethodTypes[m.Type] {
		return fmt.Errorf("tool %q version %q: install_methods[%d]: unknown type %q", toolID, version, idx, m.Type)
	}
	if m.Type == MethodHTTP && strings.TrimSpace(m.URL) == "" {
		return fmt.Errorf("tool %q version %q: install_methods[%d]: http method requires a url", toolID, version, idx)
	}
	if len(m.Platforms) == 0 {
		return fmt.Errorf("tool %q version %q: install_methods[%d]: at least one platform is required", toolID, version, idx)
	}
	return nil
}
