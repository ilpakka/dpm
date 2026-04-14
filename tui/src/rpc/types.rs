//! Plain serde structs that mirror the Go-side JSON output.
//!
//! Field naming follows the JSON tags added in `internal/catalog`,
//! `internal/profiles`, `internal/dotfiles`, `internal/metadata`, and
//! `internal/engine`. Anything that's missing or unknown is silently ignored
//! at deserialization time so the frontend keeps working when the backend
//! adds new fields.

use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Default, Deserialize, Serialize)]
pub struct InstallMethod {
    #[serde(rename = "type")]
    pub method_type: String,
    #[serde(default)]
    pub platforms: Vec<String>,
    #[serde(default)]
    pub bubble_compatible: bool,
    #[serde(default)]
    pub url: String,
    #[serde(default)]
    pub sha256: String,
    #[serde(default)]
    pub package: String,
}

#[derive(Debug, Clone, Default, Deserialize, Serialize)]
pub struct ToolVersion {
    pub version: String,
    #[serde(default)]
    pub major_version: i32,
    #[serde(default)]
    pub is_latest: bool,
    #[serde(default)]
    pub release_date: String,
    #[serde(default)]
    pub install_methods: Vec<InstallMethod>,
    #[serde(default)]
    pub installed: bool,
}

#[derive(Debug, Clone, Default, Deserialize, Serialize)]
pub struct Tool {
    pub id: String,
    pub name: String,
    #[serde(default)]
    pub description: String,
    #[serde(default)]
    pub category: String,
    #[serde(default)]
    pub tags: Vec<String>,
    #[serde(default)]
    pub versions: Vec<ToolVersion>,
}

impl Tool {
    /// First install method type across all versions, used as a "src" badge.
    pub fn primary_method(&self) -> String {
        self.versions
            .iter()
            .find_map(|v| v.install_methods.first().map(|m| m.method_type.clone()))
            .unwrap_or_else(|| "—".to_string())
    }

    pub fn is_installed(&self) -> bool {
        self.versions.iter().any(|v| v.installed)
    }

    pub fn installed_version(&self) -> Option<&str> {
        self.versions
            .iter()
            .find(|v| v.installed)
            .map(|v| v.version.as_str())
    }
}

#[derive(Debug, Clone, Default, Deserialize, Serialize)]
pub struct ToolRef {
    pub id: String,
    #[serde(default)]
    pub major_version: i32,
}

#[derive(Debug, Clone, Default, Deserialize, Serialize)]
pub struct Profile {
    pub id: String,
    pub name: String,
    #[serde(default)]
    pub description: String,
    #[serde(default)]
    pub category: String,
    #[serde(default)]
    pub course_code: String,
    #[serde(default)]
    pub tools: Vec<String>,
    #[serde(default)]
    pub tool_refs: Vec<ToolRef>,
    #[serde(default)]
    pub dotfiles: Vec<String>,
    #[serde(default)]
    pub version: String,
    #[serde(default)]
    pub installed: bool,
}

impl Profile {
    pub fn all_tool_ids(&self) -> Vec<String> {
        let mut seen = std::collections::HashSet::new();
        let mut out = Vec::new();
        for id in &self.tools {
            if seen.insert(id.clone()) {
                out.push(id.clone());
            }
        }
        for r in &self.tool_refs {
            if seen.insert(r.id.clone()) {
                out.push(r.id.clone());
            }
        }
        out
    }
}

#[derive(Debug, Clone, Default, Deserialize, Serialize)]
pub struct Dotfile {
    pub id: String,
    pub name: String,
    #[serde(default)]
    pub description: String,
    #[serde(default)]
    pub tool_id: String,
    #[serde(default)]
    pub source_repo: String,
    #[serde(default)]
    pub source_dir: String,
    #[serde(default)]
    pub is_curated: bool,
    #[serde(default)]
    pub installed: bool,
}

#[derive(Debug, Clone, Default, Deserialize, Serialize)]
pub struct InstalledTool {
    pub tool_id: String,
    #[serde(default)]
    pub tool_name: String,
    pub version: String,
    #[serde(default)]
    pub platform: String,
    #[serde(default)]
    pub install_dir: String,
    #[serde(default)]
    pub installed_at: String,
    #[serde(default)]
    pub sha256: String,
    #[serde(default)]
    pub verified: bool,
    #[serde(default)]
    pub method: String,
}


#[derive(Debug, Clone, Default, Deserialize, Serialize)]
pub struct Setting {
    pub id: String,
    pub name: String,
    #[serde(default)]
    pub description: String,
    #[serde(rename = "type", default)]
    pub kind: String,
    #[serde(default)]
    pub value: String,
    #[serde(default)]
    pub default: String,
}

#[derive(Debug, Clone, Default, Deserialize, Serialize)]
pub struct SettingsGroup {
    pub name: String,
    #[serde(default)]
    pub settings: Vec<Setting>,
}

/// One health-check entry inside a [`DoctorReport`].
#[derive(Debug, Clone, Default, Deserialize, Serialize)]
pub struct DoctorCheck {
    pub name: String,
    #[serde(default)]
    pub ok: bool,
    #[serde(default)]
    pub severity: String,
    #[serde(default)]
    pub message: String,
}

/// System-health snapshot returned by `engine.doctor`.
#[derive(Debug, Clone, Default, Deserialize, Serialize)]
pub struct DoctorReport {
    #[serde(default)]
    pub platform: String,
    #[serde(default)]
    pub in_path: bool,
    #[serde(default)]
    pub dpm_root: String,
    #[serde(default)]
    pub checks: Vec<DoctorCheck>,
}

/// Bubble session metadata returned by `engine.bubble.start`.
#[derive(Debug, Clone, Default, Deserialize, Serialize)]
pub struct BubbleSession {
    pub root_path: String,
    #[serde(default)]
    pub shell: String,
    #[serde(default)]
    pub env: std::collections::HashMap<String, String>,
}

/// One detected dotfile config inside a scanned git repo.
#[derive(Debug, Clone, Default, Deserialize, Serialize)]
pub struct DetectedConfig {
    pub name: String,
    pub source: String,
    pub target: String,
    #[serde(default)]
    pub merge_strategy: String,
    #[serde(default)]
    pub is_script: bool,
}

/// Result of `engine.dotfiles.scan`.
#[derive(Debug, Clone, Default, Deserialize, Serialize)]
pub struct DotfileScanResult {
    pub repo_dir: String,
    #[serde(default)]
    pub configs: Vec<DetectedConfig>,
}

/// Update status for one tool, mirrors `engine.UpdateStatus` on the Go side.
#[derive(Debug, Clone, Default, Deserialize, Serialize)]
pub struct UpdateStatus {
    pub tool_id: String,
    #[serde(default)]
    pub installed_ver: String,
    #[serde(default)]
    pub available_ver: String,
    #[serde(default)]
    pub update_required: bool,
    #[serde(default)]
    pub not_in_catalog: bool,
}
