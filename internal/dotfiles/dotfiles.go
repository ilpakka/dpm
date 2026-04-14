package dotfiles

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// FileMapping describes one config file to place on the user's filesystem.
type FileMapping struct {
	Source        string `yaml:"source"         json:"source"`                   // Path inside the dotfiles source (e.g., "tmux.conf")
	Target        string `yaml:"target"         json:"target"`                   // Destination relative to $HOME (e.g., ".tmux.conf")
	MergeStrategy string `yaml:"merge_strategy" json:"merge_strategy,omitempty"` // "backup", "append", "skip", "force"
}

// Dotfile represents a curated dotfile configuration
type Dotfile struct {
	ID          string        `yaml:"id"          json:"id"`
	Name        string        `yaml:"name"        json:"name"`
	Description string        `yaml:"description" json:"description"`
	ToolID      string        `yaml:"tool_id"     json:"tool_id,omitempty"`     // Which tool this config is for (e.g., "tmux")
	Version     string        `yaml:"version"     json:"version,omitempty"`     // Config version
	Files       []string      `yaml:"files"       json:"files,omitempty"`       // List of config files provided (legacy)
	Mappings    []FileMapping `yaml:"mappings"    json:"mappings,omitempty"`    // Detailed file mappings with merge strategies
	SourceRepo  string        `yaml:"source_repo" json:"source_repo,omitempty"` // GitHub repo if custom
	SourceDir   string        `yaml:"source_dir"  json:"source_dir,omitempty"`  // Local directory with source files
	IsCurated   bool          `yaml:"is_curated"  json:"is_curated"`            // DPM official or user custom
	Installed   bool          `json:"installed"`                                // Runtime state
}

// LoadDotfiles loads dotfile definitions from YAML files in dir.
// Falls back to GetMockDotfiles if directory does not exist.
func LoadDotfiles(dir string) ([]Dotfile, error) {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return GetMockDotfiles(), nil
	}
	return LoadDotfilesFromDir(dir)
}

// LoadDotfilesFromDir reads all .yaml files in dir and returns parsed dotfiles.
func LoadDotfilesFromDir(dir string) ([]Dotfile, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, fmt.Errorf("dotfiles: glob %s: %w", dir, err)
	}

	var result []Dotfile
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("dotfiles: read %s: %w", filepath.Base(path), err)
		}
		var df Dotfile
		if err := yaml.Unmarshal(data, &df); err != nil {
			return nil, fmt.Errorf("dotfiles: parse %s: %w", filepath.Base(path), err)
		}
		if df.ID == "" {
			return nil, fmt.Errorf("dotfiles: %s: missing required field 'id'", filepath.Base(path))
		}
		result = append(result, df)
	}
	return result, nil
}

// LoadDotfilesFromFS reads all .yaml files from an embed.FS (or any fs.FS).
func LoadDotfilesFromFS(fsys fs.FS, dir string) ([]Dotfile, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("dotfiles: read embedded dir %s: %w", dir, err)
	}

	var result []Dotfile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		data, err := fs.ReadFile(fsys, dir+"/"+e.Name())
		if err != nil {
			return nil, fmt.Errorf("dotfiles: read embedded %s: %w", e.Name(), err)
		}
		var df Dotfile
		if err := yaml.Unmarshal(data, &df); err != nil {
			return nil, fmt.Errorf("dotfiles: parse embedded %s: %w", e.Name(), err)
		}
		if df.ID == "" {
			return nil, fmt.Errorf("dotfiles: embedded %s: missing required field 'id'", e.Name())
		}
		result = append(result, df)
	}
	return result, nil
}

// GetMockDotfiles returns hardcoded dotfiles for demo
func GetMockDotfiles() []Dotfile {
	return []Dotfile{
		{
			ID:          "tmux-power",
			Name:        "tmux-power",
			Description: "Power user tmux configuration with TPM and sensible defaults",
			ToolID:      "tmux",
			Version:     "2.1.0",
			Files:       []string{".tmux.conf", ".tmux/plugins/tpm"},
			IsCurated:   true,
			Installed:   false,
		},
		{
			ID:          "zsh-ohmy",
			Name:        "zsh-ohmy",
			Description: "Oh My Zsh with powerlevel10k theme and useful plugins",
			ToolID:      "zsh",
			Version:     "1.0.0",
			Files:       []string{".zshrc", ".oh-my-zsh", ".p10k.zsh"},
			IsCurated:   true,
			Installed:   true, // Demo: installed
		},
		{
			ID:          "nvim-lazy",
			Name:        "nvim-lazy",
			Description: "Neovim with lazy.nvim plugin manager and LSP setup",
			ToolID:      "nvim",
			Version:     "1.5.0",
			Files:       []string{".config/nvim/init.lua", ".config/nvim/lua"},
			IsCurated:   true,
			Installed:   false,
		},
		{
			ID:          "oh-my-posh-theme",
			Name:        "oh-my-posh-theme",
			Description: "Oh My Posh with custom nerd font theme",
			ToolID:      "oh-my-posh",
			Version:     "1.0.0",
			Files:       []string{".config/oh-my-posh/theme.json"},
			IsCurated:   true,
			Installed:   false,
		},
	}
}

// FilterDotfiles filters dotfiles by search query (case-insensitive).
func FilterDotfiles(dotfiles []Dotfile, query string) []Dotfile {
	if query == "" {
		return dotfiles
	}

	q := strings.ToLower(query)
	var filtered []Dotfile
	for _, df := range dotfiles {
		if strings.Contains(strings.ToLower(df.Name), q) ||
			strings.Contains(strings.ToLower(df.Description), q) ||
			strings.Contains(strings.ToLower(df.ID), q) ||
			strings.Contains(strings.ToLower(df.ToolID), q) {
			filtered = append(filtered, df)
		}
	}
	return filtered
}

// GetDotfilesByTool returns dotfiles for a specific tool.
func GetDotfilesByTool(dotfiles []Dotfile, toolID string) []Dotfile {
	var result []Dotfile
	for _, df := range dotfiles {
		if df.ToolID == toolID {
			result = append(result, df)
		}
	}
	return result
}
