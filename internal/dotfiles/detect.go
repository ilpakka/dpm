package dotfiles

import (
	"os"
	"path/filepath"
	"strings"
)

// DetectedConfig represents a config file found in a dotfiles repo.
type DetectedConfig struct {
	Name          string // Human-readable name (e.g., "Oh My Posh theme")
	Source        string // Path relative to repo root (e.g., "ohmyposh/theme.toml")
	Target        string // Suggested target relative to $HOME (e.g., ".config/ohmyposh/dpm-theme.toml")
	MergeStrategy string // Suggested merge strategy
	IsScript      bool   // true if this is an install script to run
	Selected      bool   // User selection state (for TUI)
}

// pattern defines a known config file pattern.
type pattern struct {
	name     string
	globs    []string            // file patterns to match
	target   func(string) string // returns target path given matched file
	merge    string
	isScript bool
}

var knownPatterns = []pattern{
	{
		name:  "Oh My Posh theme",
		globs: []string{"*.omp.toml", "*.omp.json", "*.omp.yaml", "**/*.omp.toml", "**/*.omp.json"},
		target: func(f string) string {
			return ".config/ohmyposh/" + filepath.Base(f)
		},
		merge: "backup",
	},
	{
		name:  "tmux config",
		globs: []string{"tmux.conf", ".tmux.conf", "tmux/*.conf"},
		target: func(string) string {
			return ".tmux.conf"
		},
		merge: "backup",
	},
	{
		name:  "Bash aliases",
		globs: []string{"aliases.sh", "bash_aliases", ".bash_aliases", "aliases"},
		target: func(string) string {
			return ".bash_aliases"
		},
		merge: "append",
	},
	{
		name:  "Bash config",
		globs: []string{".bashrc", "bashrc"},
		target: func(string) string {
			return ".bashrc"
		},
		merge: "append",
	},
	{
		name:  "Zsh config",
		globs: []string{".zshrc", "zshrc"},
		target: func(string) string {
			return ".zshrc"
		},
		merge: "append",
	},
	{
		name:  "Starship prompt",
		globs: []string{"starship.toml", ".config/starship.toml"},
		target: func(string) string {
			return ".config/starship.toml"
		},
		merge: "backup",
	},
	{
		name:  "Neovim config",
		globs: []string{"init.lua", "init.vim", ".config/nvim/init.lua", "nvim/init.lua"},
		target: func(f string) string {
			if strings.Contains(f, "/") {
				return ".config/nvim/init.lua"
			}
			return ".config/nvim/" + filepath.Base(f)
		},
		merge: "backup",
	},
	{
		name:  "Vim config",
		globs: []string{".vimrc", "vimrc"},
		target: func(string) string {
			return ".vimrc"
		},
		merge: "backup",
	},
	{
		name:  "Git config",
		globs: []string{".gitconfig", "gitconfig"},
		target: func(string) string {
			return ".gitconfig"
		},
		merge: "backup",
	},
	{
		name:     "Install script",
		globs:    []string{"install.sh", "setup.sh", "bootstrap.sh"},
		target:   func(f string) string { return f },
		merge:    "skip",
		isScript: true,
	},
}

// DetectConfigs scans a directory for known config file patterns.
func DetectConfigs(repoDir string) []DetectedConfig {
	var detected []DetectedConfig
	seen := make(map[string]bool) // avoid duplicates

	for _, p := range knownPatterns {
		for _, glob := range p.globs {
			matches, err := filepath.Glob(filepath.Join(repoDir, glob))
			if err != nil {
				continue
			}
			for _, match := range matches {
				rel, err := filepath.Rel(repoDir, match)
				if err != nil {
					continue
				}
				// Skip .git internals.
				if strings.HasPrefix(rel, ".git/") || rel == ".git" {
					continue
				}
				info, err := os.Stat(match)
				if err != nil || info.IsDir() {
					continue
				}
				key := p.name + ":" + rel
				if seen[key] {
					continue
				}
				seen[key] = true
				detected = append(detected, DetectedConfig{
					Name:          p.name,
					Source:        rel,
					Target:        p.target(rel),
					MergeStrategy: p.merge,
					IsScript:      p.isScript,
					Selected:      !p.isScript, // select configs by default, scripts opt-in
				})
			}
		}
	}

	return detected
}
