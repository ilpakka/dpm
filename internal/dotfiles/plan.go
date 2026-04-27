package dotfiles

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dpm.fi/dpm/internal/adapter"
)

type RiskLevel string

const (
	RiskLow    RiskLevel = "low"
	RiskMedium RiskLevel = "medium"
	RiskHigh   RiskLevel = "high"
	RiskBlock  RiskLevel = "block"
)

type PlanItem struct {
	Source        string              `json:"source"`
	Target        string              `json:"target"`
	MergeStrategy string              `json:"merge_strategy"`
	Action        string              `json:"action"`
	Risk          RiskLevel           `json:"risk"`
	Issues        []string            `json:"issues,omitempty"`
	Exists        bool                `json:"exists"`
	Symlink       bool                `json:"symlink"`
	Spec          adapter.DotfileSpec `json:"-"`
}

type PlanResult struct {
	DotfileID string     `json:"dotfile_id"`
	SourceDir string     `json:"source_dir"`
	Commit    string     `json:"commit,omitempty"`
	Items     []PlanItem `json:"items"`
	Blocked   bool       `json:"blocked"`
}

func (p *PlanResult) Specs() []adapter.DotfileSpec {
	if p == nil || p.Blocked {
		return nil
	}
	specs := make([]adapter.DotfileSpec, 0, len(p.Items))
	for _, item := range p.Items {
		if item.Risk == RiskBlock {
			continue
		}
		specs = append(specs, item.Spec)
	}
	return specs
}

func Plan(df Dotfile, opts ApplyOptions) (*PlanResult, error) {
	sourceDir, err := resolveSource(df, opts.DPMRoot)
	if err != nil {
		return nil, fmt.Errorf("dotfiles: resolve source for %s: %w", df.ID, err)
	}

	if err := mergeManifest(&df, sourceDir); err != nil {
		return nil, fmt.Errorf("dotfiles: load manifest for %s: %w", df.ID, err)
	}

	specs := plannedSpecs(df, sourceDir, opts.HomeDir)
	return PlanSpecs(df.ID, sourceDir, opts.HomeDir, specs), nil
}

func PlanSpecs(dotfileID, sourceDir, homeDir string, specs []adapter.DotfileSpec) *PlanResult {
	plan := &PlanResult{
		DotfileID: dotfileID,
		SourceDir: sourceDir,
		Commit:    commitForDir(sourceDir),
		Items:     make([]PlanItem, 0, len(specs)),
	}
	for _, spec := range specs {
		item := validateSpec(spec, sourceDir, homeDir)
		if item.Risk == RiskBlock {
			plan.Blocked = true
		}
		plan.Items = append(plan.Items, item)
	}
	return plan
}

func plannedSpecs(df Dotfile, sourceDir, homeDir string) []adapter.DotfileSpec {
	var specs []adapter.DotfileSpec
	if len(df.Mappings) > 0 {
		for _, m := range df.Mappings {
			sourcePath := filepath.Join(sourceDir, m.Source)
			targetPath, err := ExpandTarget(m.Target, homeDir)
			if err != nil {
				targetPath = m.Target
			}
			specs = append(specs, adapter.DotfileSpec{
				SourcePath:    sourcePath,
				TargetPath:    targetPath,
				MergeStrategy: parseMergeStrategy(m.MergeStrategy),
			})
		}
		return specs
	}

	for _, file := range df.Files {
		sourcePath := findSourceFile(sourceDir, file)
		if sourcePath == "" {
			sourcePath = filepath.Join(sourceDir, strings.TrimPrefix(file, string(os.PathSeparator)))
		}
		targetPath, err := ExpandTarget(file, homeDir)
		if err != nil {
			targetPath = file
		}
		specs = append(specs, adapter.DotfileSpec{
			SourcePath:    sourcePath,
			TargetPath:    targetPath,
			MergeStrategy: adapter.MergeBackup,
		})
	}
	return specs
}

func validateSpec(spec adapter.DotfileSpec, sourceDir, homeDir string) PlanItem {
	item := PlanItem{
		Source:        spec.SourcePath,
		Target:        spec.TargetPath,
		MergeStrategy: string(spec.MergeStrategy),
		Spec:          spec,
		Risk:          RiskLow,
	}
	if item.MergeStrategy == "" {
		item.MergeStrategy = string(adapter.MergeBackup)
	}
	item.Action = actionForStrategy(spec.MergeStrategy)

	if strings.TrimSpace(spec.SourcePath) == "" {
		item.block("source path is empty")
	} else if !filepath.IsAbs(spec.SourcePath) {
		item.block("source path is not absolute")
	} else if sourceDir != "" && !pathWithin(spec.SourcePath, sourceDir) {
		item.block("source path escapes the dotfiles source directory")
	} else if info, err := os.Stat(spec.SourcePath); err != nil {
		item.block("source file is missing or unreadable")
	} else if info.IsDir() {
		item.block("source path is a directory, not a file")
	}

	if strings.TrimSpace(spec.TargetPath) == "" {
		item.block("target path is empty")
		return item
	}
	if !filepath.IsAbs(spec.TargetPath) {
		item.block("target path is not absolute after expansion")
		return item
	}
	if !pathWithin(spec.TargetPath, homeDir) {
		item.block("target path escapes the user's home directory")
	}
	if sensitiveTarget(spec.TargetPath, homeDir) {
		item.block("target path is security-sensitive and must not be managed automatically")
	}

	if info, err := os.Lstat(spec.TargetPath); err == nil {
		item.Exists = true
		if info.Mode()&os.ModeSymlink != 0 {
			item.Symlink = true
			item.raise(RiskHigh, "target is a symlink")
			if resolved, evalErr := filepath.EvalSymlinks(spec.TargetPath); evalErr == nil && !pathWithin(resolved, homeDir) {
				item.block("target symlink resolves outside the user's home directory")
			}
		}
	}

	switch spec.MergeStrategy {
	case adapter.MergeForce:
		if item.Exists {
			item.raise(RiskHigh, "force strategy overwrites an existing target")
		}
	case adapter.MergeBackup, "":
		if item.Exists {
			item.raise(RiskMedium, "existing target will be backed up before replacement")
		}
	case adapter.MergeAppend:
		item.raise(RiskMedium, "append strategy modifies an existing config file")
	case adapter.MergeSkip:
		// Safe by default.
	default:
		item.block("unknown merge strategy")
	}

	return item
}

func (i *PlanItem) block(issue string) {
	i.Risk = RiskBlock
	i.Issues = append(i.Issues, issue)
}

func (i *PlanItem) raise(level RiskLevel, issue string) {
	if riskRank(level) > riskRank(i.Risk) {
		i.Risk = level
	}
	i.Issues = append(i.Issues, issue)
}

func riskRank(level RiskLevel) int {
	switch level {
	case RiskLow:
		return 1
	case RiskMedium:
		return 2
	case RiskHigh:
		return 3
	case RiskBlock:
		return 4
	default:
		return 0
	}
}

func actionForStrategy(strategy adapter.MergeStrategy) string {
	switch strategy {
	case adapter.MergeAppend:
		return "append"
	case adapter.MergeSkip:
		return "create-if-missing"
	case adapter.MergeForce:
		return "overwrite"
	default:
		return "backup-and-replace"
	}
}

func pathWithin(path, root string) bool {
	cleanPath := filepath.Clean(path)
	cleanRoot := filepath.Clean(root)
	return cleanPath == cleanRoot || strings.HasPrefix(cleanPath, cleanRoot+string(os.PathSeparator))
}

func sensitiveTarget(targetPath, homeDir string) bool {
	rel, err := filepath.Rel(filepath.Clean(homeDir), filepath.Clean(targetPath))
	if err != nil || strings.HasPrefix(rel, "..") {
		return true
	}
	rel = filepath.ToSlash(rel)
	sensitiveExact := map[string]struct{}{
		".ssh/authorized_keys": {},
		".ssh/config":          {},
		".aws/credentials":     {},
		".netrc":               {},
	}
	if _, ok := sensitiveExact[rel]; ok {
		return true
	}
	return strings.HasPrefix(rel, ".ssh/id_") ||
		strings.HasPrefix(rel, ".gnupg/") ||
		strings.Contains(rel, "credentials")
}

func commitForDir(dir string) string {
	commit, err := gitCommit(dir)
	if err != nil {
		return ""
	}
	return commit
}
