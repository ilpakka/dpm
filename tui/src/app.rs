//! App state — single source of truth for the TUI.
//!
//! Mutated by `event::handle_key` and read by `ui::draw`.

use std::collections::{HashMap, HashSet};
use std::time::Instant;

use crate::rpc::types::{
    BubbleSession, DetectedConfig, Dotfile, DoctorReport, InstalledTool, Profile, SettingsGroup,
    Tool, UpdateStatus,
};

/// Top-level tabs in display order.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Tab {
    Profiles,
    Tools,
    Installed,
    Dotfiles,
    Bubble,
}

impl Tab {
    pub fn label(&self) -> &'static str {
        match self {
            Tab::Profiles => "Profiles",
            Tab::Tools => "Tools",
            Tab::Installed => "Installed",
            Tab::Dotfiles => "Dotfiles",
            Tab::Bubble => "Bubble",
        }
    }
}

pub const ALL_TABS: &[Tab] = &[
    Tab::Profiles,
    Tab::Tools,
    Tab::Installed,
    Tab::Dotfiles,
    Tab::Bubble,
];

/// What kind of action is queued behind a confirm popup.
#[derive(Debug, Clone)]
pub enum PendingAction {
    InstallTool { tool_id: String, version: String },
    RemoveTool { tool_id: String, version: String },
    ApplyProfile { profile_id: String },
    InstallDotfile { dotfile_id: String },
    InstallCart,
    AddPath,
    StartBubble,
    BatchRemove { items: Vec<(String, String)> },
    Restore,
    UpdateAll,
    UpdateOne { tool_id: String },
    OpenTool { tool_id: String },
    ImportDotfiles {
        repo_dir: String,
        configs: Vec<DetectedConfig>,
    },
}

/// Sub-view inside the current tab — overrides the body content.
#[derive(Debug, Clone)]
pub enum SubView {
    None,
    AddCustomRepo {
        input: String,
    },
    DotfilesImport {
        repo_dir: String,
        configs: Vec<DetectedConfig>,
        cursor: usize,
        selected: HashSet<usize>,
    },
}

/// Global view that takes over the body regardless of the current tab.
#[derive(Debug, Clone)]
pub enum GlobalView {
    None,
    Update,
    Doctor,
    Help,
    Menu { cursor: usize },
}

/// Transient toast at the bottom of the frame.
#[derive(Debug, Clone)]
pub struct StatusMessage {
    pub text: String,
    pub kind: MsgKind,
    pub expires_at: Instant,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum MsgKind {
    Info,
    Success,
    Error,
}

/// Single cart entry — what we'll send to the backend on Ctrl-A.
#[derive(Debug, Clone)]
pub struct CartItem {
    pub kind: CartKind,
    pub id: String,
    pub name: String,
    pub version: String,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub enum CartKind {
    Tool,
    Dotfile,
}

/// Modal popup state.
#[derive(Debug, Clone)]
pub enum Popup {
    Confirm {
        title: String,
        message: String,
        /// True = Yes button highlighted, False = No.
        yes_focused: bool,
    },
    Progress {
        title: String,
        message: String,
        log: Vec<String>,
        spinner_idx: usize,
    },
    Result {
        title: String,
        message: String,
        ok: bool,
    },
}

/// Search overlay state — tri-source filter (tools, profiles, dotfiles).
#[derive(Debug, Clone, Default)]
pub struct SearchState {
    pub open: bool,
    pub input: String,
    pub cursor: usize,
    pub results: Vec<SearchHit>,
}

#[derive(Debug, Clone)]
pub struct SearchHit {
    pub kind: SearchKind,
    pub id: String,
    pub name: String,
    pub description: String,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum SearchKind {
    Tool,
    Profile,
    Dotfile,
}

/// Settings overlay state.
#[derive(Debug, Clone, Default)]
pub struct SettingsState {
    pub open: bool,
    pub groups: Vec<SettingsGroup>,
    pub group_idx: usize,
    pub cursor: usize,
    /// True while a non-bool setting is being edited.
    pub editing: bool,
    /// Buffer holding the in-progress edit.
    pub edit_buffer: String,
}

pub struct App {
    pub tab_index: usize,
    pub depth: usize,

    pub profiles: Vec<Profile>,
    pub tools: Vec<Tool>,
    pub installed: Vec<InstalledTool>,
    pub dotfiles: Vec<Dotfile>,

    pub profile_cursor: usize,
    pub tool_cursor: usize,
    pub version_cursor: usize,
    pub installed_cursor: usize,
    pub dotfile_cursor: usize,

    pub popup: Option<Popup>,
    pub pending: Option<PendingAction>,

    pub search: SearchState,
    pub settings: SettingsState,

    /// Multi-select cart, keyed by composite "kind:id".
    pub cart: HashMap<String, CartItem>,

    pub platform: String,
    pub in_path: bool,
    /// Animation tick, ~10Hz.
    pub anim_tick: u64,

    pub should_quit: bool,

    /// Sub-view inside the current tab (e.g. dotfile import flow).
    pub sub_view: SubView,
    /// Global view (Update / Doctor / Help / Ctrl-K menu).
    pub global_view: GlobalView,

    /// Composite "tool_id@version" keys selected for batch remove.
    pub installed_selected: HashSet<String>,
    /// True when the right-hand version list is focused on Tools depth 1.
    pub version_focus: bool,
    /// Latest update-check snapshot.
    pub update_status: Vec<UpdateStatus>,
    /// Cursor inside the Update view.
    pub update_cursor: usize,
    /// Latest doctor report.
    pub doctor_report: Option<DoctorReport>,
    /// Transient status message overlaying the footer hint.
    pub message: Option<StatusMessage>,
    /// Active bubble session, set after engine.bubble.start.
    pub bubble_session: Option<BubbleSession>,
    /// True after the first DataRefreshed event — distinguishes "loading" from "empty".
    pub data_loaded: bool,
}

impl App {
    pub fn new() -> Self {
        Self {
            tab_index: 0,
            depth: 0,
            profiles: Vec::new(),
            tools: Vec::new(),
            installed: Vec::new(),
            dotfiles: Vec::new(),
            profile_cursor: 0,
            tool_cursor: 0,
            version_cursor: 0,
            installed_cursor: 0,
            dotfile_cursor: 0,
            popup: None,
            pending: None,
            search: SearchState::default(),
            settings: SettingsState::default(),
            cart: HashMap::new(),
            platform: String::new(),
            in_path: true,
            anim_tick: 0,
            should_quit: false,
            sub_view: SubView::None,
            global_view: GlobalView::None,
            installed_selected: HashSet::new(),
            version_focus: false,
            update_status: Vec::new(),
            update_cursor: 0,
            doctor_report: None,
            message: None,
            bubble_session: None,
            data_loaded: false,
        }
    }

    /// True when an overlay or sub-view is currently capturing input.
    #[allow(dead_code)]
    pub fn has_overlay(&self) -> bool {
        self.search.open
            || self.settings.open
            || self.popup.is_some()
            || !matches!(self.sub_view, SubView::None)
            || !matches!(self.global_view, GlobalView::None)
    }

    pub fn set_message<S: Into<String>>(&mut self, text: S, kind: MsgKind) {
        let lifetime = std::time::Duration::from_secs(3);
        self.message = Some(StatusMessage {
            text: text.into(),
            kind,
            expires_at: Instant::now() + lifetime,
        });
    }

    pub fn clear_expired_message(&mut self) {
        if let Some(m) = &self.message {
            if Instant::now() >= m.expires_at {
                self.message = None;
            }
        }
    }

    pub fn installed_key(tool_id: &str, version: &str) -> String {
        format!("{}@{}", tool_id, version)
    }

    pub fn tab(&self) -> Tab {
        ALL_TABS[self.tab_index]
    }

    pub fn tab_index(&self) -> usize {
        self.tab_index
    }

    pub fn next_tab(&mut self) {
        self.depth = 0;
        self.tab_index = (self.tab_index + 1) % ALL_TABS.len();
    }

    pub fn prev_tab(&mut self) {
        self.depth = 0;
        if self.tab_index == 0 {
            self.tab_index = ALL_TABS.len() - 1;
        } else {
            self.tab_index -= 1;
        }
    }

    pub fn list_len(&self) -> usize {
        match self.tab() {
            Tab::Profiles => self.profiles.len(),
            Tab::Tools => self.tools.len(),
            Tab::Installed => self.installed.len(),
            Tab::Dotfiles => self.dotfiles.len(),
            Tab::Bubble => 0,
        }
    }

    pub fn cursor_down(&mut self) {
        if self.tab() == Tab::Tools && self.depth >= 1 && (self.version_focus || self.depth >= 2) {
            if let Some(t) = self.tools.get(self.tool_cursor) {
                let n = t.versions.len();
                if n > 0 && self.version_cursor + 1 < n {
                    self.version_cursor += 1;
                }
            }
            return;
        }
        let n = self.list_len();
        if n == 0 {
            return;
        }
        let cur = self.cursor();
        if cur + 1 < n {
            self.set_cursor(cur + 1);
        }
    }

    pub fn cursor_up(&mut self) {
        if self.tab() == Tab::Tools && self.depth >= 1 && (self.version_focus || self.depth >= 2) {
            if self.version_cursor > 0 {
                self.version_cursor -= 1;
            }
            return;
        }
        let cur = self.cursor();
        if cur > 0 {
            self.set_cursor(cur - 1);
        }
    }

    pub fn cursor(&self) -> usize {
        match self.tab() {
            Tab::Profiles => self.profile_cursor,
            Tab::Tools => self.tool_cursor,
            Tab::Installed => self.installed_cursor,
            Tab::Dotfiles => self.dotfile_cursor,
            Tab::Bubble => 0,
        }
    }

    pub fn set_cursor(&mut self, v: usize) {
        match self.tab() {
            Tab::Profiles => self.profile_cursor = v,
            Tab::Tools => self.tool_cursor = v,
            Tab::Installed => self.installed_cursor = v,
            Tab::Dotfiles => self.dotfile_cursor = v,
            Tab::Bubble => {}
        }
    }

    pub fn enter_deeper(&mut self) {
        let max = match self.tab() {
            Tab::Tools => 2,
            _ => 1,
        };
        if self.depth < max {
            self.depth += 1;
            if self.depth == 1 && self.tab() == Tab::Tools {
                self.version_cursor = 0;
            }
        }
    }

    pub fn go_back(&mut self) {
        if self.depth > 0 {
            self.depth -= 1;
            self.version_focus = false;
        }
    }

    pub fn current_item_name(&self) -> Option<String> {
        match self.tab() {
            Tab::Profiles => self.profiles.get(self.profile_cursor).map(|p| p.name.clone()),
            Tab::Tools => self.tools.get(self.tool_cursor).map(|t| t.name.clone()),
            Tab::Installed => self.installed.get(self.installed_cursor).map(|i| {
                if !i.tool_name.is_empty() {
                    i.tool_name.clone()
                } else {
                    i.tool_id.clone()
                }
            }),
            Tab::Dotfiles => self.dotfiles.get(self.dotfile_cursor).map(|d| d.name.clone()),
            Tab::Bubble => None,
        }
    }

    pub fn current_version_name(&self) -> Option<String> {
        if self.tab() != Tab::Tools {
            return None;
        }
        let tool = self.tools.get(self.tool_cursor)?;
        tool.versions.get(self.version_cursor).map(|v| v.version.clone())
    }

    // ----- cart helpers --------------------------------------------------

    pub fn cart_key(kind: CartKind, id: &str) -> String {
        let prefix = match kind {
            CartKind::Tool => "tool",
            CartKind::Dotfile => "dotfile",
        };
        format!("{}:{}", prefix, id)
    }

    pub fn cart_contains(&self, kind: CartKind, id: &str) -> bool {
        self.cart.contains_key(&Self::cart_key(kind, id))
    }

    pub fn cart_toggle_current(&mut self) {
        match self.tab() {
            Tab::Tools => {
                let Some(tool) = self.tools.get(self.tool_cursor) else { return };
                let key = Self::cart_key(CartKind::Tool, &tool.id);
                if self.cart.remove(&key).is_none() {
                    let version = tool
                        .versions
                        .iter()
                        .find(|v| v.is_latest)
                        .or_else(|| tool.versions.first())
                        .map(|v| v.version.clone())
                        .unwrap_or_default();
                    self.cart.insert(
                        key,
                        CartItem {
                            kind: CartKind::Tool,
                            id: tool.id.clone(),
                            name: tool.name.clone(),
                            version,
                        },
                    );
                }
            }
            Tab::Profiles => {
                let Some(prof) = self.profiles.get(self.profile_cursor).cloned() else { return };
                let ids = prof.all_tool_ids();
                let all_in = ids
                    .iter()
                    .all(|id| self.cart_contains(CartKind::Tool, id));
                if all_in {
                    for id in &ids {
                        self.cart.remove(&Self::cart_key(CartKind::Tool, id));
                    }
                } else {
                    for id in &ids {
                        if self.cart_contains(CartKind::Tool, id) {
                            continue;
                        }
                        let (name, version) = self
                            .tools
                            .iter()
                            .find(|t| t.id == *id)
                            .map(|t| {
                                let v = t
                                    .versions
                                    .iter()
                                    .find(|v| v.is_latest)
                                    .or_else(|| t.versions.first())
                                    .map(|v| v.version.clone())
                                    .unwrap_or_default();
                                (t.name.clone(), v)
                            })
                            .unwrap_or_else(|| (id.clone(), String::new()));
                        self.cart.insert(
                            Self::cart_key(CartKind::Tool, id),
                            CartItem {
                                kind: CartKind::Tool,
                                id: id.clone(),
                                name,
                                version,
                            },
                        );
                    }
                }
            }
            Tab::Dotfiles => {
                let Some(df) = self.dotfiles.get(self.dotfile_cursor) else { return };
                let key = Self::cart_key(CartKind::Dotfile, &df.id);
                if self.cart.remove(&key).is_none() {
                    self.cart.insert(
                        key,
                        CartItem {
                            kind: CartKind::Dotfile,
                            id: df.id.clone(),
                            name: df.name.clone(),
                            version: String::new(),
                        },
                    );
                }
            }
            _ => {}
        }
    }

    pub fn cart_clear(&mut self) {
        self.cart.clear();
    }
}
