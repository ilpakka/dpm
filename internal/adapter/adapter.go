package adapter

import (
	"time"

	"dpm.fi/dpm/internal/catalog"
)

type MergeStrategy string

const (
	MergeBackup MergeStrategy = "backup"
	MergeAppend MergeStrategy = "append"
	MergeSkip   MergeStrategy = "skip"
	MergeForce  MergeStrategy = "force"
)

type Bundle struct {
	ToolID        string
	ToolName      string
	Version       string
	ArchivePath   string
	Binaries      []string
	BinSubdir     string
	SHA256        string
	Verified      bool
	Method        string
	InstallScript string
	Dotfiles      []DotfileSpec
	DataDirs      []string
}

type DotfileSpec struct {
	SourcePath    string
	TargetPath    string
	MergeStrategy MergeStrategy
}

type InstallOptions struct {
	DryRun      bool
	ConfirmFunc func(prompt string) bool
}

type InstallResult struct {
	ToolID         string
	Version        string
	InstallDir     string
	Symlinks       []string
	DotfileResults []ApplyResult
}

type ApplyResult struct {
	Spec       DotfileSpec
	Applied    bool
	BackupPath string
	Skipped    bool
	Err        error
}

type IAdapter interface {
	InstallBundle(bundle Bundle, opts InstallOptions) (*InstallResult, error)
	ExtractArchive(archivePath, destDir string) error
	RunScript(scriptPath string, env []string) error
	CreateSymlink(target, linkPath string) error

	ApplyDotfile(spec DotfileSpec, opts InstallOptions) (ApplyResult, error)
	ApplyDotfiles(specs []DotfileSpec, opts InstallOptions) ([]ApplyResult, error)
	CreateDataDirs(dirs []string) error

	AddToPATH(dir string) error
	IsInPATH(dir string) bool

	BackupFile(path string) (backupPath string, err error)
	GetDPMRoot() string
	GetTempDir(toolID string) (string, error)
	CleanStaleTempDirs(maxAge time.Duration) error
	Platform() catalog.Platform
}
