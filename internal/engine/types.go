package engine

// DoctorReport summarises the system health checks run by Doctor().
type DoctorReport struct {
	Platform string        `json:"platform"`
	InPATH   bool          `json:"in_path"`
	DPMRoot  string        `json:"dpm_root"`
	Checks   []DoctorCheck `json:"checks"`
}

// DoctorCheck is one entry in a DoctorReport.
type DoctorCheck struct {
	Name     string `json:"name"`
	OK       bool   `json:"ok"`
	Severity string `json:"severity"` // "info" | "warn" | "error"
	Message  string `json:"message"`
}

// BubbleSession describes a freshly created bubble (ephemeral DPM root)
// that the caller (TUI) can spawn a shell into.
type BubbleSession struct {
	RootPath string            `json:"root_path"`
	Shell    string            `json:"shell"`
	Env      map[string]string `json:"env"`
}

// DotfileScanResult is what ScanGitDotfiles returns: the cloned repo dir
// plus every config file we recognised inside it.
type DotfileScanResult struct {
	RepoDir string                 `json:"repo_dir"`
	Commit  string                 `json:"commit,omitempty"`
	Configs []ScannedDotfileConfig `json:"configs"`
}

// ScannedDotfileConfig is the JSON-friendly version of dotfiles.DetectedConfig.
// We define it locally so we can ship a stable JSON shape independent of
// internal field naming.
type ScannedDotfileConfig struct {
	Name          string `json:"name"`
	Source        string `json:"source"`
	Target        string `json:"target"`
	MergeStrategy string `json:"merge_strategy"`
	IsScript      bool   `json:"is_script"`
	AllowScript   bool   `json:"allow_script,omitempty"`
}
