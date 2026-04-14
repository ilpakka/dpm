//! Translates raw key events into mutations on `App`.
//!
//! Returns an optional [`Action`] when a key triggers an async backend call
//! that the main loop must dispatch (install, remove, applyProfile, ...).

use crossterm::event::{KeyCode, KeyEvent, KeyModifiers};

use crate::app::{
    App, CartItem, GlobalView, MsgKind, PendingAction, Popup, SearchKind, SubView, Tab,
};
use crate::rpc::types::DetectedConfig;
use crate::ui::theme;

/// Async work the main loop should kick off after a key event.
#[derive(Debug, Clone)]
pub enum Action {
    InstallTool {
        tool_id: String,
        version: String,
    },
    RemoveTool {
        tool_id: String,
        version: String,
    },
    ApplyProfile {
        profile_id: String,
    },
    InstallDotfile {
        dotfile_id: String,
    },
    InstallCart {
        items: Vec<CartItem>,
    },
    AddPath,
    StartBubble,
    OpenSettings,
    SetSetting {
        id: String,
        value: String,
    },
    ResetSetting {
        id: String,
    },
    SearchRecompute,
    Refresh,
    BatchRemove {
        items: Vec<(String, String)>,
    },
    Restore,
    UpdateAll,
    UpdateOne {
        tool_id: String,
    },
    OpenTool {
        tool_id: String,
    },
    CheckUpdates,
    Doctor,
    ScanDotfiles {
        repo_url: String,
    },
    ImportDotfiles {
        repo_dir: String,
        configs: Vec<DetectedConfig>,
    },
}

pub fn handle_key(app: &mut App, key: KeyEvent) -> Option<Action> {
    // Global quit shortcut.
    if matches!(key.code, KeyCode::Char('c')) && key.modifiers.contains(KeyModifiers::CONTROL) {
        app.should_quit = true;
        return None;
    }

    // Overlays eat input first, in priority order: search > settings > popup
    // > sub_view > global_view.
    if app.search.open {
        return handle_search_key(app, key);
    }
    if app.settings.open {
        return handle_settings_key(app, key);
    }
    if app.popup.is_some() {
        return handle_popup_key(app, key);
    }
    if !matches!(app.sub_view, SubView::None) {
        return handle_subview_key(app, key);
    }
    if !matches!(app.global_view, GlobalView::None) {
        return handle_global_view_key(app, key);
    }

    // Cart install via Ctrl-A.
    if matches!(key.code, KeyCode::Char('a')) && key.modifiers.contains(KeyModifiers::CONTROL) {
        return open_cart_install(app);
    }
    // Ctrl-K → global menu.
    if matches!(key.code, KeyCode::Char('k')) && key.modifiers.contains(KeyModifiers::CONTROL) {
        app.global_view = GlobalView::Menu { cursor: 0 };
        return None;
    }
    // Ctrl-U → check updates and open update view.
    if matches!(key.code, KeyCode::Char('u')) && key.modifiers.contains(KeyModifiers::CONTROL) {
        app.global_view = GlobalView::Update;
        return Some(Action::CheckUpdates);
    }

    match key.code {
        KeyCode::Char('q') => {
            app.should_quit = true;
            None
        }
        KeyCode::Char('?') | KeyCode::F(1) => {
            app.global_view = GlobalView::Help;
            None
        }
        KeyCode::Tab if app.tab() == Tab::Tools && app.depth == 1 => {
            app.version_focus = !app.version_focus;
            None
        }
        KeyCode::Tab | KeyCode::Char('l') | KeyCode::Right if app.depth == 0 => {
            app.next_tab();
            None
        }
        KeyCode::BackTab | KeyCode::Char('h') | KeyCode::Left if app.depth == 0 => {
            app.prev_tab();
            None
        }
        KeyCode::Down | KeyCode::Char('j') => {
            app.cursor_down();
            None
        }
        KeyCode::Up | KeyCode::Char('k') => {
            app.cursor_up();
            None
        }
        KeyCode::Esc => {
            app.version_focus = false;
            app.go_back();
            None
        }
        KeyCode::Enter => handle_enter(app),
        KeyCode::Char('d') => handle_delete(app),
        KeyCode::Char('o') if app.tab() == Tab::Installed && app.depth == 0 => {
            handle_open_tool(app)
        }
        KeyCode::Char('a') if app.tab() == Tab::Dotfiles && app.depth == 0 => {
            app.sub_view = SubView::AddCustomRepo {
                input: String::new(),
            };
            None
        }
        KeyCode::Char(' ') => {
            if app.tab() == Tab::Installed && app.depth == 0 {
                if let Some(i) = app.installed.get(app.installed_cursor) {
                    let key = App::installed_key(&i.tool_id, &i.version);
                    if !app.installed_selected.remove(&key) {
                        app.installed_selected.insert(key);
                    }
                }
                None
            } else {
                app.cart_toggle_current();
                None
            }
        }
        KeyCode::Char('/') => {
            app.search.open = true;
            app.search.input.clear();
            app.search.cursor = 0;
            app.search.results.clear();
            Some(Action::SearchRecompute)
        }
        KeyCode::Char(',') => {
            app.settings.open = true;
            app.settings.cursor = 0;
            app.settings.group_idx = 0;
            Some(Action::OpenSettings)
        }
        KeyCode::Char('t') => {
            let name = theme::next();
            app.set_message(format!("Theme: {}", name), MsgKind::Info);
            None
        }
        _ => None,
    }
}

fn handle_open_tool(app: &mut App) -> Option<Action> {
    let i = app.installed.get(app.installed_cursor)?;
    let tool_id = i.tool_id.clone();
    app.pending = Some(PendingAction::OpenTool {
        tool_id: tool_id.clone(),
    });
    fire_pending(app)
}

fn open_cart_install(app: &mut App) -> Option<Action> {
    if app.cart.is_empty() {
        return None;
    }
    let items: Vec<CartItem> = app.cart.values().cloned().collect();
    app.pending = Some(PendingAction::InstallCart);
    app.popup = Some(Popup::Confirm {
        title: "Install cart".to_string(),
        message: format!("Install {} selected items?", items.len()),
        yes_focused: true,
    });
    None
}

fn handle_enter(app: &mut App) -> Option<Action> {
    match app.tab() {
        Tab::Profiles => {
            if app.depth == 0 {
                app.enter_deeper();
                return None;
            }
            // depth >= 1 → confirm applyProfile
            let prof = app.profiles.get(app.profile_cursor)?;
            let id = prof.id.clone();
            app.pending = Some(PendingAction::ApplyProfile {
                profile_id: id.clone(),
            });
            app.popup = Some(Popup::Confirm {
                title: "Apply profile".to_string(),
                message: format!("Install all tools and dotfiles for '{}'?", prof.name),
                yes_focused: true,
            });
            None
        }
        Tab::Tools => {
            match app.depth {
                0 => {
                    app.enter_deeper();
                    None
                }
                1 => {
                    // Confirm install of selected version.
                    let tool = app.tools.get(app.tool_cursor)?;
                    let ver = tool.versions.get(app.version_cursor)?;
                    let action = if ver.installed {
                        format!("Reinstall {} {}?", tool.name, ver.version)
                    } else {
                        format!("Install {} {}?", tool.name, ver.version)
                    };
                    app.pending = Some(PendingAction::InstallTool {
                        tool_id: tool.id.clone(),
                        version: ver.version.clone(),
                    });
                    app.popup = Some(Popup::Confirm {
                        title: "Install".to_string(),
                        message: action,
                        yes_focused: true,
                    });
                    None
                }
                _ => None,
            }
        }
        Tab::Installed => None,
        Tab::Dotfiles => {
            let df = app.dotfiles.get(app.dotfile_cursor)?;
            app.pending = Some(PendingAction::InstallDotfile {
                dotfile_id: df.id.clone(),
            });
            app.popup = Some(Popup::Confirm {
                title: "Install dotfile".to_string(),
                message: format!("Install '{}'?", df.name),
                yes_focused: true,
            });
            None
        }
        Tab::Bubble => {
            app.pending = Some(PendingAction::StartBubble);
            app.popup = Some(Popup::Confirm {
                title: "Start bubble".to_string(),
                message: "Spawn an ephemeral DPM session?".to_string(),
                yes_focused: true,
            });
            None
        }
    }
}

fn handle_delete(app: &mut App) -> Option<Action> {
    match app.tab() {
        Tab::Tools => {
            // Only meaningful at depth ≥ 1, on a version that's installed.
            if app.depth < 1 {
                return None;
            }
            let tool = app.tools.get(app.tool_cursor)?;
            let ver = tool.versions.get(app.version_cursor)?;
            if !ver.installed {
                return None;
            }
            app.pending = Some(PendingAction::RemoveTool {
                tool_id: tool.id.clone(),
                version: ver.version.clone(),
            });
            app.popup = Some(Popup::Confirm {
                title: "Remove".to_string(),
                message: format!("Remove {} {}?", tool.name, ver.version),
                yes_focused: false,
            });
            None
        }
        Tab::Installed => {
            // Batch delete if any rows are selected.
            if !app.installed_selected.is_empty() {
                let items: Vec<(String, String)> = app
                    .installed
                    .iter()
                    .filter(|i| {
                        app.installed_selected
                            .contains(&App::installed_key(&i.tool_id, &i.version))
                    })
                    .map(|i| (i.tool_id.clone(), i.version.clone()))
                    .collect();
                if items.is_empty() {
                    return None;
                }
                let count = items.len();
                app.pending = Some(PendingAction::BatchRemove { items });
                app.popup = Some(Popup::Confirm {
                    title: "Batch remove".to_string(),
                    message: format!("Remove {} selected tools?", count),
                    yes_focused: false,
                });
                return None;
            }
            let i = app.installed.get(app.installed_cursor)?;
            app.pending = Some(PendingAction::RemoveTool {
                tool_id: i.tool_id.clone(),
                version: i.version.clone(),
            });
            app.popup = Some(Popup::Confirm {
                title: "Remove".to_string(),
                message: format!("Remove {} {}?", i.tool_id, i.version),
                yes_focused: false,
            });
            None
        }
        _ => None,
    }
}

fn handle_popup_key(app: &mut App, key: KeyEvent) -> Option<Action> {
    let popup = app.popup.as_ref()?.clone();
    match popup {
        Popup::Confirm { yes_focused, .. } => match key.code {
            KeyCode::Left | KeyCode::Right | KeyCode::Char('h') | KeyCode::Char('l') => {
                if let Some(Popup::Confirm { yes_focused: yf, .. }) = app.popup.as_mut() {
                    *yf = !*yf;
                }
                None
            }
            KeyCode::Char('y') | KeyCode::Char('Y') => fire_pending(app),
            KeyCode::Char('n') | KeyCode::Char('N') => {
                app.popup = None;
                app.pending = None;
                None
            }
            KeyCode::Enter => {
                if yes_focused {
                    fire_pending(app)
                } else {
                    app.popup = None;
                    app.pending = None;
                    None
                }
            }
            KeyCode::Esc | KeyCode::Char('q') => {
                app.popup = None;
                app.pending = None;
                None
            }
            _ => None,
        },
        Popup::Progress { .. } => None,
        Popup::Result { .. } => {
            app.popup = None;
            Some(Action::Refresh)
        }
    }
}

fn fire_pending(app: &mut App) -> Option<Action> {
    let pending = app.pending.take()?;
    let action = match pending {
        PendingAction::InstallTool { tool_id, version } => {
            Action::InstallTool { tool_id, version }
        }
        PendingAction::RemoveTool { tool_id, version } => Action::RemoveTool { tool_id, version },
        PendingAction::ApplyProfile { profile_id } => Action::ApplyProfile { profile_id },
        PendingAction::InstallDotfile { dotfile_id } => Action::InstallDotfile { dotfile_id },
        PendingAction::InstallCart => {
            let items: Vec<CartItem> = app.cart.values().cloned().collect();
            app.cart_clear();
            Action::InstallCart { items }
        }
        PendingAction::AddPath => Action::AddPath,
        PendingAction::StartBubble => Action::StartBubble,
        PendingAction::BatchRemove { items } => {
            app.installed_selected.clear();
            Action::BatchRemove { items }
        }
        PendingAction::Restore => Action::Restore,
        PendingAction::UpdateAll => Action::UpdateAll,
        PendingAction::UpdateOne { tool_id } => Action::UpdateOne { tool_id },
        PendingAction::OpenTool { tool_id } => {
            // OpenTool suspends the TUI in main.rs — no progress popup needed.
            return Some(Action::OpenTool { tool_id });
        }
        PendingAction::ImportDotfiles { repo_dir, configs } => {
            app.sub_view = SubView::None;
            Action::ImportDotfiles { repo_dir, configs }
        }
    };
    app.popup = Some(Popup::Progress {
        title: "Working…".to_string(),
        message: "talking to dpm engine".to_string(),
        log: Vec::new(),
        spinner_idx: 0,
    });
    Some(action)
}

fn handle_subview_key(app: &mut App, key: KeyEvent) -> Option<Action> {
    let sub = app.sub_view.clone();
    match sub {
        SubView::None => None,
        SubView::AddCustomRepo { mut input } => match key.code {
            KeyCode::Esc => {
                app.sub_view = SubView::None;
                None
            }
            KeyCode::Backspace => {
                input.pop();
                app.sub_view = SubView::AddCustomRepo { input };
                None
            }
            KeyCode::Enter => {
                if input.trim().is_empty() {
                    return None;
                }
                let repo_url = input.trim().to_string();
                app.popup = Some(Popup::Progress {
                    title: "Scanning repo…".to_string(),
                    message: format!("git clone {}", repo_url),
                    log: Vec::new(),
                    spinner_idx: 0,
                });
                Some(Action::ScanDotfiles { repo_url })
            }
            KeyCode::Char(c) => {
                input.push(c);
                app.sub_view = SubView::AddCustomRepo { input };
                None
            }
            _ => None,
        },
        SubView::DotfilesImport {
            repo_dir,
            configs,
            mut cursor,
            mut selected,
        } => match key.code {
            KeyCode::Esc => {
                app.sub_view = SubView::None;
                None
            }
            KeyCode::Down | KeyCode::Char('j') => {
                if cursor + 1 < configs.len() {
                    cursor += 1;
                }
                app.sub_view = SubView::DotfilesImport {
                    repo_dir,
                    configs,
                    cursor,
                    selected,
                };
                None
            }
            KeyCode::Up | KeyCode::Char('k') => {
                if cursor > 0 {
                    cursor -= 1;
                }
                app.sub_view = SubView::DotfilesImport {
                    repo_dir,
                    configs,
                    cursor,
                    selected,
                };
                None
            }
            KeyCode::Char(' ') => {
                if !selected.remove(&cursor) {
                    selected.insert(cursor);
                }
                app.sub_view = SubView::DotfilesImport {
                    repo_dir,
                    configs,
                    cursor,
                    selected,
                };
                None
            }
            KeyCode::Char('a') => {
                for i in 0..configs.len() {
                    selected.insert(i);
                }
                app.sub_view = SubView::DotfilesImport {
                    repo_dir,
                    configs,
                    cursor,
                    selected,
                };
                None
            }
            KeyCode::Char('n') => {
                selected.clear();
                app.sub_view = SubView::DotfilesImport {
                    repo_dir,
                    configs,
                    cursor,
                    selected,
                };
                None
            }
            KeyCode::Enter => {
                if selected.is_empty() {
                    return None;
                }
                let chosen: Vec<DetectedConfig> = configs
                    .iter()
                    .enumerate()
                    .filter_map(|(i, c)| {
                        if selected.contains(&i) {
                            Some(c.clone())
                        } else {
                            None
                        }
                    })
                    .collect();
                let count = chosen.len();
                app.pending = Some(PendingAction::ImportDotfiles {
                    repo_dir: repo_dir.clone(),
                    configs: chosen,
                });
                app.popup = Some(Popup::Confirm {
                    title: "Import dotfiles".to_string(),
                    message: format!("Apply {} selected configs?", count),
                    yes_focused: true,
                });
                None
            }
            _ => None,
        },
    }
}

fn handle_global_view_key(app: &mut App, key: KeyEvent) -> Option<Action> {
    let view = app.global_view.clone();
    match view {
        GlobalView::None => None,
        GlobalView::Help => {
            if matches!(key.code, KeyCode::Esc | KeyCode::Char('q') | KeyCode::Char('?')) {
                app.global_view = GlobalView::None;
            }
            None
        }
        GlobalView::Doctor => {
            if matches!(key.code, KeyCode::Esc | KeyCode::Char('q')) {
                app.global_view = GlobalView::None;
            }
            None
        }
        GlobalView::Update => match key.code {
            KeyCode::Esc | KeyCode::Char('q') => {
                app.global_view = GlobalView::None;
                None
            }
            KeyCode::Down | KeyCode::Char('j') => {
                if app.update_cursor + 1 < app.update_status.len() {
                    app.update_cursor += 1;
                }
                None
            }
            KeyCode::Up | KeyCode::Char('k') => {
                if app.update_cursor > 0 {
                    app.update_cursor -= 1;
                }
                None
            }
            KeyCode::Enter => {
                let tool_id = app
                    .update_status
                    .get(app.update_cursor)
                    .map(|u| u.tool_id.clone())?;
                app.pending = Some(PendingAction::UpdateOne { tool_id });
                app.popup = Some(Popup::Confirm {
                    title: "Update tool".to_string(),
                    message: "Update the selected tool?".to_string(),
                    yes_focused: true,
                });
                None
            }
            KeyCode::Char('U') => {
                app.pending = Some(PendingAction::UpdateAll);
                app.popup = Some(Popup::Confirm {
                    title: "Update all".to_string(),
                    message: "Update all tools that have new versions?".to_string(),
                    yes_focused: true,
                });
                None
            }
            _ => None,
        },
        GlobalView::Menu { mut cursor } => {
            const ITEMS: usize = 5; // Update, Doctor, Restore, Help, Quit
            match key.code {
                KeyCode::Esc | KeyCode::Char('q') => {
                    app.global_view = GlobalView::None;
                    None
                }
                KeyCode::Down | KeyCode::Char('j') => {
                    cursor = (cursor + 1) % ITEMS;
                    app.global_view = GlobalView::Menu { cursor };
                    None
                }
                KeyCode::Up | KeyCode::Char('k') => {
                    cursor = if cursor == 0 { ITEMS - 1 } else { cursor - 1 };
                    app.global_view = GlobalView::Menu { cursor };
                    None
                }
                KeyCode::Enter => match cursor {
                    0 => {
                        app.global_view = GlobalView::Update;
                        Some(Action::CheckUpdates)
                    }
                    1 => {
                        app.global_view = GlobalView::Doctor;
                        Some(Action::Doctor)
                    }
                    2 => {
                        app.global_view = GlobalView::None;
                        app.pending = Some(PendingAction::Restore);
                        app.popup = Some(Popup::Confirm {
                            title: "Restore".to_string(),
                            message: "Remove ALL DPM-installed tools and dotfiles?".to_string(),
                            yes_focused: false,
                        });
                        None
                    }
                    3 => {
                        app.global_view = GlobalView::Help;
                        None
                    }
                    4 => {
                        app.should_quit = true;
                        None
                    }
                    _ => None,
                },
                _ => None,
            }
        }
    }
}

fn handle_search_key(app: &mut App, key: KeyEvent) -> Option<Action> {
    match key.code {
        KeyCode::Esc => {
            app.search.open = false;
            None
        }
        KeyCode::Backspace => {
            app.search.input.pop();
            app.search.cursor = 0;
            Some(Action::SearchRecompute)
        }
        KeyCode::Down => {
            if app.search.cursor + 1 < app.search.results.len() {
                app.search.cursor += 1;
            }
            None
        }
        KeyCode::Up => {
            if app.search.cursor > 0 {
                app.search.cursor -= 1;
            }
            None
        }
        KeyCode::Enter => {
            // Jump to the selected hit and close search.
            if let Some(hit) = app.search.results.get(app.search.cursor).cloned() {
                jump_to_hit(app, hit.kind, &hit.id);
            }
            app.search.open = false;
            None
        }
        KeyCode::Char(c) => {
            app.search.input.push(c);
            app.search.cursor = 0;
            Some(Action::SearchRecompute)
        }
        _ => None,
    }
}

fn jump_to_hit(app: &mut App, kind: SearchKind, id: &str) {
    match kind {
        SearchKind::Tool => {
            if let Some(idx) = app.tools.iter().position(|t| t.id == id) {
                app.tab_index = Tab::Tools as usize;
                // Tabs are an enum but tab_index is the actual list index — find it.
                app.tab_index = ALL_TABS_INDEX_TOOLS;
                app.tool_cursor = idx;
                app.depth = 0;
            }
        }
        SearchKind::Profile => {
            if let Some(idx) = app.profiles.iter().position(|p| p.id == id) {
                app.tab_index = ALL_TABS_INDEX_PROFILES;
                app.profile_cursor = idx;
                app.depth = 0;
            }
        }
        SearchKind::Dotfile => {
            if let Some(idx) = app.dotfiles.iter().position(|d| d.id == id) {
                app.tab_index = ALL_TABS_INDEX_DOTFILES;
                app.dotfile_cursor = idx;
                app.depth = 0;
            }
        }
    }
}

// Index constants matching app::ALL_TABS order.
const ALL_TABS_INDEX_PROFILES: usize = 0;
const ALL_TABS_INDEX_TOOLS: usize = 1;
const ALL_TABS_INDEX_DOTFILES: usize = 3;

fn handle_settings_key(app: &mut App, key: KeyEvent) -> Option<Action> {
    // Edit mode swallows most keys.
    if app.settings.editing {
        match key.code {
            KeyCode::Esc => {
                app.settings.editing = false;
                app.settings.edit_buffer.clear();
                None
            }
            KeyCode::Backspace => {
                app.settings.edit_buffer.pop();
                None
            }
            KeyCode::Enter => {
                let id = app
                    .settings
                    .groups
                    .get(app.settings.group_idx)?
                    .settings
                    .get(app.settings.cursor)?
                    .id
                    .clone();
                let val = app.settings.edit_buffer.clone();
                if let Some(s_mut) = app
                    .settings
                    .groups
                    .get_mut(app.settings.group_idx)
                    .and_then(|g| g.settings.get_mut(app.settings.cursor))
                {
                    s_mut.value = val.clone();
                }
                app.settings.editing = false;
                app.settings.edit_buffer.clear();
                Some(Action::SetSetting { id, value: val })
            }
            KeyCode::Char(c) => {
                app.settings.edit_buffer.push(c);
                None
            }
            _ => None,
        }
    } else {
        match key.code {
            KeyCode::Esc => {
                app.settings.open = false;
                None
            }
            KeyCode::Tab | KeyCode::Right => {
                if !app.settings.groups.is_empty() {
                    app.settings.group_idx =
                        (app.settings.group_idx + 1) % app.settings.groups.len();
                    app.settings.cursor = 0;
                }
                None
            }
            KeyCode::BackTab | KeyCode::Left => {
                if !app.settings.groups.is_empty() {
                    if app.settings.group_idx == 0 {
                        app.settings.group_idx = app.settings.groups.len() - 1;
                    } else {
                        app.settings.group_idx -= 1;
                    }
                    app.settings.cursor = 0;
                }
                None
            }
            KeyCode::Down | KeyCode::Char('j') => {
                if let Some(g) = app.settings.groups.get(app.settings.group_idx) {
                    if app.settings.cursor + 1 < g.settings.len() {
                        app.settings.cursor += 1;
                    }
                }
                None
            }
            KeyCode::Up | KeyCode::Char('k') => {
                if app.settings.cursor > 0 {
                    app.settings.cursor -= 1;
                }
                None
            }
            KeyCode::Enter => {
                let s = app
                    .settings
                    .groups
                    .get(app.settings.group_idx)?
                    .settings
                    .get(app.settings.cursor)?
                    .clone();
                match s.kind.as_str() {
                    "bool" => {
                        let new_val = if s.value == "true" { "false" } else { "true" };
                        if let Some(s_mut) = app
                            .settings
                            .groups
                            .get_mut(app.settings.group_idx)
                            .and_then(|g| g.settings.get_mut(app.settings.cursor))
                        {
                            s_mut.value = new_val.to_string();
                        }
                        Some(Action::SetSetting {
                            id: s.id,
                            value: new_val.to_string(),
                        })
                    }
                    "action" => None,
                    _ => {
                        // Enter text-edit mode for any non-bool setting.
                        app.settings.editing = true;
                        app.settings.edit_buffer = s.value.clone();
                        None
                    }
                }
            }
            KeyCode::Char('r') => {
                let s = app
                    .settings
                    .groups
                    .get(app.settings.group_idx)?
                    .settings
                    .get(app.settings.cursor)?
                    .clone();
                if let Some(s_mut) = app
                    .settings
                    .groups
                    .get_mut(app.settings.group_idx)
                    .and_then(|g| g.settings.get_mut(app.settings.cursor))
                {
                    s_mut.value = s.default.clone();
                }
                Some(Action::ResetSetting { id: s.id })
            }
            KeyCode::Char('t') => {
                let name = theme::next();
                app.set_message(format!("Theme: {}", name), MsgKind::Info);
                None
            }
            _ => None,
        }
    }
}
