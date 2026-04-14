//! Entrypoint for `dpm-tui`.
//!
//! Spawns `dpm serve --stdio`, sets up the terminal, runs the event loop, and
//! routes async backend results back to the UI.

mod app;
mod event;
mod rpc;
mod ui;

use std::io;
use std::sync::Arc;
use std::time::Duration;

use anyhow::{anyhow, Context, Result};
use crossterm::event::{self as cevent, Event, KeyEventKind};
use crossterm::execute;
use crossterm::terminal::{
    disable_raw_mode, enable_raw_mode, EnterAlternateScreen, LeaveAlternateScreen,
};
use ratatui::backend::CrosstermBackend;
use ratatui::Terminal;
use serde_json::{json, Value};
use tokio::sync::{mpsc, Mutex};

use crate::app::{App, CartKind, MsgKind, Popup, SearchHit, SearchKind};
use crate::event::Action;
use crate::rpc::client::{Notification, RpcClient};
use crate::rpc::types::{
    BubbleSession, DoctorReport, Dotfile, DotfileScanResult, InstalledTool, Profile,
    SettingsGroup, Tool, UpdateStatus,
};

/// Result of a backend call that should be reflected in the UI.
#[derive(Debug)]
enum BackendEvent {
    Log(String),
    /// Action finished — close the progress popup and surface a result.
    Done {
        ok: bool,
        message: String,
    },
    /// Refresh data after an action completes.
    DataRefreshed {
        tools: Vec<Tool>,
        profiles: Vec<Profile>,
        installed: Vec<InstalledTool>,
        dotfiles: Vec<Dotfile>,
    },
    Settings(Vec<SettingsGroup>),
    Tick,
    /// Update-status snapshot — populates the Update view.
    Updates(Vec<UpdateStatus>),
    /// Doctor report — populates the Doctor view.
    Doctor(DoctorReport),
    /// Scan finished — switch sub_view to DotfilesImport.
    ScanResult(DotfileScanResult),
    /// Transient status message at the bottom of the frame.
    Message {
        text: String,
        kind: MsgKind,
    },
    /// Action that needs the terminal: suspend, run callback, resume.
    SuspendForOpen { binary_path: String, tool_id: String },
    /// Bubble session ready — suspend, exec shell, resume.
    SuspendForBubble(BubbleSession),
}

#[tokio::main]
async fn main() -> Result<()> {
    let dpm_binary = std::env::var("DPM_BIN")
        .unwrap_or_else(|_| dpm_binary_default().unwrap_or_else(|_| "dpm".to_string()));

    let (client, notif_rx) = RpcClient::spawn(&dpm_binary)
        .await
        .with_context(|| format!("could not start `{} serve --stdio`", dpm_binary))?;

    // Initial data load before we touch the terminal so any error is visible.
    let mut app = App::new();
    if let Err(err) = refresh_data(&client, &mut app).await {
        eprintln!("warning: initial data load failed: {err:#}");
    }
    if let Err(err) = fetch_environment(&client, &mut app).await {
        eprintln!("warning: environment probe failed: {err:#}");
    }

    let mut terminal = setup_terminal()?;
    let res = run_loop(&mut terminal, &mut app, client, notif_rx).await;
    restore_terminal(&mut terminal)?;
    res
}

fn dpm_binary_default() -> Result<String> {
    let exe = std::env::current_exe()?;
    // tui/target/release/dpm-tui  → ../../../dpm
    let candidate = exe
        .parent()
        .and_then(|p| p.parent())
        .and_then(|p| p.parent())
        .and_then(|p| p.parent())
        .map(|p| p.join("dpm"));
    match candidate {
        Some(p) if p.exists() => Ok(p.to_string_lossy().to_string()),
        _ => Ok("dpm".to_string()),
    }
}

fn setup_terminal() -> Result<Terminal<CrosstermBackend<io::Stdout>>> {
    enable_raw_mode()?;
    let mut stdout = io::stdout();
    execute!(stdout, EnterAlternateScreen)?;
    let backend = CrosstermBackend::new(stdout);
    Ok(Terminal::new(backend)?)
}

fn restore_terminal(terminal: &mut Terminal<CrosstermBackend<io::Stdout>>) -> Result<()> {
    disable_raw_mode()?;
    execute!(terminal.backend_mut(), LeaveAlternateScreen)?;
    terminal.show_cursor()?;
    Ok(())
}

async fn run_loop(
    terminal: &mut Terminal<CrosstermBackend<io::Stdout>>,
    app: &mut App,
    client: Arc<RpcClient>,
    mut notif_rx: mpsc::Receiver<Notification>,
) -> Result<()> {
    // Channel that backend tasks use to push results into the UI loop.
    let (be_tx, mut be_rx) = mpsc::channel::<BackendEvent>(64);

    // Crossterm doesn't expose an async event stream out of the box, so we
    // poll it from a blocking task and forward to a tokio channel.
    let (key_tx, mut key_rx) = mpsc::channel::<Event>(64);
    tokio::spawn(async move {
        loop {
            let evt = tokio::task::spawn_blocking(|| {
                cevent::poll(Duration::from_millis(100)).map(|ready| {
                    if ready {
                        cevent::read().ok()
                    } else {
                        None
                    }
                })
            })
            .await;
            match evt {
                Ok(Ok(Some(ev))) => {
                    if key_tx.send(ev).await.is_err() {
                        break;
                    }
                }
                Ok(Ok(None)) => continue,
                _ => break,
            }
        }
    });

    // Forward log notifications into BackendEvent::Log.
    let be_tx_notif = be_tx.clone();
    tokio::spawn(async move {
        while let Some(n) = notif_rx.recv().await {
            if let Notification::Log(line) = n {
                let _ = be_tx_notif.send(BackendEvent::Log(line)).await;
            }
        }
    });

    // Spinner / animation tick (~10Hz).
    let be_tx_tick = be_tx.clone();
    tokio::spawn(async move {
        let mut ticker = tokio::time::interval(Duration::from_millis(100));
        ticker.tick().await;
        loop {
            ticker.tick().await;
            if be_tx_tick.send(BackendEvent::Tick).await.is_err() {
                break;
            }
        }
    });

    // Wrap App in a Mutex so backend tasks can refresh it.
    let app_mu = Arc::new(Mutex::new(std::mem::replace(app, App::new())));

    // First-run PATH check — open a popup if ~/.dpm/bin isn't in PATH.
    {
        let mut a = app_mu.lock().await;
        if !a.in_path {
            a.pending = Some(crate::app::PendingAction::AddPath);
            a.popup = Some(Popup::Confirm {
                title: "PATH not configured".to_string(),
                message:
                    "~/.dpm/bin is not in your PATH. Add it now? (you may need to restart your shell)"
                        .to_string(),
                yes_focused: true,
            });
        }
    }

    loop {
        {
            let app = app_mu.lock().await;
            terminal.draw(|f| ui::draw(f, &app))?;
            if app.should_quit {
                break;
            }
        }

        tokio::select! {
            Some(ev) = key_rx.recv() => {
                if let Event::Key(key) = ev {
                    if key.kind != KeyEventKind::Press {
                        continue;
                    }
                    let action = {
                        let mut app = app_mu.lock().await;
                        event::handle_key(&mut app, key)
                    };
                    if let Some(action) = action {
                        dispatch_action(action, client.clone(), app_mu.clone(), be_tx.clone());
                    }
                }
            }
            Some(be) = be_rx.recv() => {
                match be {
                    BackendEvent::SuspendForOpen { binary_path, tool_id } => {
                        // Close the progress popup before suspending.
                        {
                            let mut app = app_mu.lock().await;
                            app.popup = None;
                        }
                        let exit_code = suspend_and_run(terminal, |_| {
                            let status = std::process::Command::new(&binary_path).status();
                            status.map(|s| s.code().unwrap_or(0)).unwrap_or(-1)
                        })?;
                        let mut app = app_mu.lock().await;
                        app.set_message(
                            format!("{} exited with code {}", tool_id, exit_code),
                            if exit_code == 0 {
                                MsgKind::Success
                            } else {
                                MsgKind::Error
                            },
                        );
                    }
                    BackendEvent::SuspendForBubble(sess) => {
                        {
                            let mut app = app_mu.lock().await;
                            app.popup = None;
                            app.bubble_session = Some(sess.clone());
                        }
                        let shell = if sess.shell.is_empty() {
                            std::env::var("SHELL").unwrap_or_else(|_| "/bin/sh".to_string())
                        } else {
                            sess.shell.clone()
                        };
                        let env = sess.env.clone();
                        let cwd = sess.root_path.clone();
                        let _exit = suspend_and_run(terminal, |_| {
                            let mut cmd = std::process::Command::new(&shell);
                            cmd.current_dir(&cwd);
                            for (k, v) in &env {
                                cmd.env(k, v);
                            }
                            cmd.status().map(|s| s.code().unwrap_or(0)).unwrap_or(-1)
                        })?;
                        // Stop the bubble cleanly on the backend.
                        let root = sess.root_path.clone();
                        let cli = client.clone();
                        let be_tx2 = be_tx.clone();
                        tokio::spawn(async move {
                            let _ = cli
                                .call_void("engine.bubble.stop", json!({ "root_path": root }))
                                .await;
                            let _ = be_tx2
                                .send(BackendEvent::Message {
                                    text: "bubble session ended".to_string(),
                                    kind: MsgKind::Info,
                                })
                                .await;
                        });
                        let mut app = app_mu.lock().await;
                        app.bubble_session = None;
                    }
                    other => {
                        let mut app = app_mu.lock().await;
                        apply_backend_event(&mut app, other);
                    }
                }
            }
        }
    }

    Ok(())
}

/// Tear down ratatui, run a blocking closure that owns the terminal,
/// then re-enter alternate screen + raw mode and force a redraw.
fn suspend_and_run<F, R>(
    terminal: &mut Terminal<CrosstermBackend<io::Stdout>>,
    f: F,
) -> Result<R>
where
    F: FnOnce(&mut Terminal<CrosstermBackend<io::Stdout>>) -> R,
{
    disable_raw_mode()?;
    execute!(terminal.backend_mut(), LeaveAlternateScreen)?;
    terminal.show_cursor()?;
    let result = f(terminal);
    enable_raw_mode()?;
    execute!(terminal.backend_mut(), EnterAlternateScreen)?;
    terminal.hide_cursor().ok();
    terminal.clear()?;
    Ok(result)
}

fn apply_backend_event(app: &mut App, ev: BackendEvent) {
    match ev {
        BackendEvent::Log(line) => {
            if let Some(Popup::Progress { log, .. }) = app.popup.as_mut() {
                log.push(line);
                if log.len() > 256 {
                    let drop = log.len() - 256;
                    log.drain(..drop);
                }
            }
        }
        BackendEvent::Done { ok, message } => {
            app.popup = Some(Popup::Result {
                title: if ok {
                    "Done".to_string()
                } else {
                    "Failed".to_string()
                },
                message: message.clone(),
                ok,
            });
            app.set_message(
                message,
                if ok { MsgKind::Success } else { MsgKind::Error },
            );
        }
        BackendEvent::DataRefreshed {
            tools,
            profiles,
            installed,
            dotfiles,
        } => {
            app.tools = tools;
            app.profiles = profiles;
            app.installed = installed;
            app.dotfiles = dotfiles;
            app.data_loaded = true;
            // Clamp cursors to new list lengths.
            clamp(&mut app.profile_cursor, app.profiles.len());
            clamp(&mut app.tool_cursor, app.tools.len());
            clamp(&mut app.installed_cursor, app.installed.len());
            clamp(&mut app.dotfile_cursor, app.dotfiles.len());
        }
        BackendEvent::Settings(groups) => {
            app.settings.groups = groups;
            if app.settings.group_idx >= app.settings.groups.len() {
                app.settings.group_idx = 0;
            }
        }
        BackendEvent::Tick => {
            app.anim_tick = app.anim_tick.wrapping_add(1);
            if let Some(Popup::Progress { spinner_idx, .. }) = app.popup.as_mut() {
                *spinner_idx = spinner_idx.wrapping_add(1);
            }
            app.clear_expired_message();
        }
        BackendEvent::Updates(snapshot) => {
            app.update_status = snapshot;
            if app.update_cursor >= app.update_status.len() {
                app.update_cursor = 0;
            }
            app.popup = None;
        }
        BackendEvent::Doctor(report) => {
            app.doctor_report = Some(report);
            app.popup = None;
        }
        BackendEvent::ScanResult(scan) => {
            app.popup = None;
            app.sub_view = crate::app::SubView::DotfilesImport {
                repo_dir: scan.repo_dir,
                configs: scan.configs,
                cursor: 0,
                selected: std::collections::HashSet::new(),
            };
        }
        BackendEvent::Message { text, kind } => {
            app.set_message(text, kind);
        }
        BackendEvent::SuspendForOpen { .. } | BackendEvent::SuspendForBubble(_) => {
            // Handled in the run loop, not here.
        }
    }
}

fn clamp(cursor: &mut usize, len: usize) {
    if len == 0 {
        *cursor = 0;
    } else if *cursor >= len {
        *cursor = len - 1;
    }
}

fn dispatch_action(
    action: Action,
    client: Arc<RpcClient>,
    app_mu: Arc<Mutex<App>>,
    be_tx: mpsc::Sender<BackendEvent>,
) {
    tokio::spawn(async move {
        // Side-channel actions that don't go through the Done/Refresh flow.
        match action {
            Action::SearchRecompute => {
                let mut app = app_mu.lock().await;
                recompute_search(&mut app);
                return;
            }
            Action::OpenSettings => {
                match client
                    .call::<Vec<SettingsGroup>>("engine.settings.groups", Value::Null)
                    .await
                {
                    Ok(groups) => {
                        let _ = be_tx.send(BackendEvent::Settings(groups)).await;
                    }
                    Err(err) => {
                        let _ = be_tx
                            .send(BackendEvent::Log(format!("settings error: {err:#}")))
                            .await;
                    }
                }
                return;
            }
            Action::SetSetting { id, value } => {
                let _ = client
                    .call_void("engine.settings.set", json!({"id": id, "value": value}))
                    .await;
                return;
            }
            Action::ResetSetting { id } => {
                let _ = client
                    .call_void("engine.settings.reset", json!({"id": id}))
                    .await;
                return;
            }
            Action::CheckUpdates => {
                match client.call::<Vec<UpdateStatus>>("engine.checkUpdates", Value::Null).await {
                    Ok(snap) => {
                        let _ = be_tx.send(BackendEvent::Updates(snap)).await;
                    }
                    Err(err) => {
                        let _ = be_tx
                            .send(BackendEvent::Message {
                                text: format!("update check failed: {err:#}"),
                                kind: MsgKind::Error,
                            })
                            .await;
                    }
                }
                return;
            }
            Action::Doctor => {
                match client.call::<DoctorReport>("engine.doctor", Value::Null).await {
                    Ok(report) => {
                        let _ = be_tx.send(BackendEvent::Doctor(report)).await;
                    }
                    Err(err) => {
                        let _ = be_tx
                            .send(BackendEvent::Message {
                                text: format!("doctor failed: {err:#}"),
                                kind: MsgKind::Error,
                            })
                            .await;
                    }
                }
                return;
            }
            Action::ScanDotfiles { ref repo_url } => {
                match client
                    .call::<DotfileScanResult>(
                        "engine.dotfiles.scan",
                        json!({ "repo_url": repo_url }),
                    )
                    .await
                {
                    Ok(scan) => {
                        let _ = be_tx.send(BackendEvent::ScanResult(scan)).await;
                    }
                    Err(err) => {
                        let _ = be_tx
                            .send(BackendEvent::Done {
                                ok: false,
                                message: format!("scan failed: {err:#}"),
                            })
                            .await;
                    }
                }
                return;
            }
            Action::OpenTool { ref tool_id } => {
                match client
                    .call::<String>("engine.binaryPath", json!({ "tool_id": tool_id }))
                    .await
                {
                    Ok(binary_path) => {
                        let _ = be_tx
                            .send(BackendEvent::SuspendForOpen {
                                binary_path,
                                tool_id: tool_id.clone(),
                            })
                            .await;
                    }
                    Err(err) => {
                        let _ = be_tx
                            .send(BackendEvent::Message {
                                text: format!("open failed: {err:#}"),
                                kind: MsgKind::Error,
                            })
                            .await;
                    }
                }
                return;
            }
            Action::StartBubble => {
                match client
                    .call::<BubbleSession>("engine.bubble.start", Value::Null)
                    .await
                {
                    Ok(sess) => {
                        let _ = be_tx.send(BackendEvent::SuspendForBubble(sess)).await;
                    }
                    Err(err) => {
                        let _ = be_tx
                            .send(BackendEvent::Done {
                                ok: false,
                                message: format!("bubble start failed: {err:#}"),
                            })
                            .await;
                    }
                }
                return;
            }
            _ => {}
        }

        let result = run_action(action, client.clone()).await;
        let (ok, message) = match result {
            Ok(msg) => (true, msg),
            Err(err) => (false, format!("{err:#}")),
        };
        let _ = be_tx.send(BackendEvent::Done { ok, message }).await;

        // Refresh data so the UI shows the new installed state.
        match refresh_data_pure(&client).await {
            Ok((tools, profiles, installed, dotfiles)) => {
                let _ = be_tx
                    .send(BackendEvent::DataRefreshed {
                        tools,
                        profiles,
                        installed,
                        dotfiles,
                    })
                    .await;
                let _ = app_mu.lock().await; // touch to keep mu alive in this scope
            }
            Err(err) => {
                let _ = be_tx
                    .send(BackendEvent::Log(format!("refresh error: {err:#}")))
                    .await;
            }
        }
    });
}

async fn run_action(action: Action, client: Arc<RpcClient>) -> Result<String> {
    match action {
        Action::InstallTool { tool_id, version } => {
            let v: Value = client
                .request(
                    "engine.installTool",
                    json!({ "tool_id": tool_id, "version_str": version }),
                )
                .await?;
            Ok(format_install_result(&v).unwrap_or_else(|| format!("installed {}", tool_id)))
        }
        Action::RemoveTool { tool_id, version } => {
            client
                .request(
                    "engine.removeTool",
                    json!({ "tool_id": tool_id, "version": version }),
                )
                .await?;
            Ok(format!("removed {} {}", tool_id, version))
        }
        Action::ApplyProfile { profile_id } => {
            let v: Value = client
                .request("engine.applyProfile", json!({ "profile_id": profile_id }))
                .await?;
            Ok(format_profile_result(&v).unwrap_or_else(|| format!("applied {}", profile_id)))
        }
        Action::InstallDotfile { dotfile_id } => {
            client
                .request("engine.installDotfile", json!({ "dotfile_id": dotfile_id }))
                .await?;
            Ok(format!("installed dotfile {}", dotfile_id))
        }
        Action::InstallCart { items } => {
            let mut ok_n = 0usize;
            let mut fail_n = 0usize;
            let mut last_err = String::new();
            for it in items {
                let res = match it.kind {
                    CartKind::Tool => {
                        client
                            .request(
                                "engine.installTool",
                                json!({ "tool_id": it.id, "version_str": it.version }),
                            )
                            .await
                    }
                    CartKind::Dotfile => {
                        client
                            .request("engine.installDotfile", json!({ "dotfile_id": it.id }))
                            .await
                    }
                };
                match res {
                    Ok(_) => ok_n += 1,
                    Err(e) => {
                        fail_n += 1;
                        last_err = format!("{e:#}");
                    }
                }
            }
            if fail_n == 0 {
                Ok(format!("cart installed: {} ok", ok_n))
            } else {
                Err(anyhow!(
                    "cart installed: {} ok, {} failed (last: {})",
                    ok_n,
                    fail_n,
                    last_err
                ))
            }
        }
        Action::AddPath => {
            client.call_void("engine.addToPath", Value::Null).await?;
            Ok("PATH updated — restart your shell to pick it up".to_string())
        }
        Action::BatchRemove { items } => {
            let mut ok_n = 0usize;
            let mut fail_n = 0usize;
            let mut last_err = String::new();
            for (tool_id, version) in items {
                let res = client
                    .request(
                        "engine.removeTool",
                        json!({ "tool_id": tool_id, "version": version }),
                    )
                    .await;
                match res {
                    Ok(_) => ok_n += 1,
                    Err(e) => {
                        fail_n += 1;
                        last_err = format!("{e:#}");
                    }
                }
            }
            if fail_n == 0 {
                Ok(format!("removed {} tools", ok_n))
            } else {
                Err(anyhow!(
                    "removed {} ok, {} failed (last: {})",
                    ok_n,
                    fail_n,
                    last_err
                ))
            }
        }
        Action::Restore => {
            client.call_void("engine.restore", Value::Null).await?;
            Ok("restore complete — DPM state cleared".to_string())
        }
        Action::UpdateAll => {
            // Backend has updateAll? If not, loop client-side using checkUpdates.
            let snap: Vec<UpdateStatus> = client
                .call("engine.checkUpdates", Value::Null)
                .await?;
            let needs: Vec<String> = snap
                .into_iter()
                .filter(|u| u.update_required)
                .map(|u| u.tool_id)
                .collect();
            if needs.is_empty() {
                return Ok("nothing to update".to_string());
            }
            let mut ok_n = 0usize;
            let mut fail_n = 0usize;
            let mut last_err = String::new();
            for id in &needs {
                let res = client
                    .request("engine.updateTool", json!({ "tool_id": id }))
                    .await;
                match res {
                    Ok(_) => ok_n += 1,
                    Err(e) => {
                        fail_n += 1;
                        last_err = format!("{e:#}");
                    }
                }
            }
            if fail_n == 0 {
                Ok(format!("updated {} tools", ok_n))
            } else {
                Err(anyhow!(
                    "updated {} ok, {} failed (last: {})",
                    ok_n,
                    fail_n,
                    last_err
                ))
            }
        }
        Action::UpdateOne { tool_id } => {
            client
                .request("engine.updateTool", json!({ "tool_id": tool_id }))
                .await?;
            Ok(format!("updated {}", tool_id))
        }
        Action::ImportDotfiles { repo_dir, configs } => {
            let cfg_value = serde_json::to_value(&configs)?;
            let v: Value = client
                .request(
                    "engine.dotfiles.applyImported",
                    json!({ "repo_dir": repo_dir, "configs": cfg_value }),
                )
                .await?;
            let applied = v
                .get("applied")
                .and_then(|x| x.as_array())
                .map(|a| a.len())
                .unwrap_or(0);
            Ok(format!("imported {} dotfile configs", applied))
        }
        Action::StartBubble => {
            // Handled in dispatch_action via SuspendForBubble.
            Ok(String::new())
        }
        Action::OpenTool { .. }
        | Action::CheckUpdates
        | Action::Doctor
        | Action::ScanDotfiles { .. } => {
            // Handled in dispatch_action.
            Ok(String::new())
        }
        Action::Refresh => Ok("refreshed".to_string()),
        Action::OpenSettings
        | Action::SetSetting { .. }
        | Action::ResetSetting { .. }
        | Action::SearchRecompute => {
            // handled earlier in dispatch_action
            Ok(String::new())
        }
    }
}

fn format_install_result(v: &Value) -> Option<String> {
    let id = v.get("tool_id")?.as_str()?;
    let ver = v.get("version")?.as_str()?;
    Some(format!("installed {} {}", id, ver))
}

fn format_profile_result(v: &Value) -> Option<String> {
    let installed = v
        .get("tools_installed")
        .and_then(|x| x.as_u64())
        .unwrap_or(0);
    let failed = v.get("tools_failed").and_then(|x| x.as_u64()).unwrap_or(0);
    Some(format!(
        "profile applied: {} installed, {} failed",
        installed, failed
    ))
}

async fn refresh_data(client: &Arc<RpcClient>, app: &mut App) -> Result<()> {
    let (tools, profiles, installed, dotfiles) = refresh_data_pure(client).await?;
    app.tools = tools;
    app.profiles = profiles;
    app.installed = installed;
    app.dotfiles = dotfiles;
    app.data_loaded = true;
    clamp(&mut app.profile_cursor, app.profiles.len());
    clamp(&mut app.tool_cursor, app.tools.len());
    clamp(&mut app.installed_cursor, app.installed.len());
    clamp(&mut app.dotfile_cursor, app.dotfiles.len());
    Ok(())
}

async fn refresh_data_pure(
    client: &Arc<RpcClient>,
) -> Result<(Vec<Tool>, Vec<Profile>, Vec<InstalledTool>, Vec<Dotfile>)> {
    let tools: Vec<Tool> = call_or_empty(client, "engine.catalog").await;
    let profiles: Vec<Profile> = call_or_empty(client, "engine.profiles").await;
    let installed: Vec<InstalledTool> = call_or_empty(client, "engine.installed").await;
    let dotfiles: Vec<Dotfile> = call_or_empty(client, "engine.dotfiles").await;
    Ok((tools, profiles, installed, dotfiles))
}

/// Call an RPC method that returns a JSON array. If the backend returns `null`
/// or deserialization fails, return an empty Vec instead of propagating the error.
async fn call_or_empty<T: for<'de> serde::Deserialize<'de>>(
    client: &Arc<RpcClient>,
    method: &str,
) -> Vec<T> {
    match client.request(method, Value::Null).await {
        Ok(v) if v.is_null() => Vec::new(),
        Ok(v) => serde_json::from_value(v).unwrap_or_default(),
        Err(_) => Vec::new(),
    }
}

async fn fetch_environment(client: &Arc<RpcClient>, app: &mut App) -> Result<()> {
    let plat: String = client.call("engine.platform", Value::Null).await?;
    app.platform = plat;
    let in_path: bool = client.call("engine.isInPath", Value::Null).await?;
    app.in_path = in_path;
    Ok(())
}

fn recompute_search(app: &mut App) {
    let q = app.search.input.to_lowercase();
    let mut hits: Vec<SearchHit> = Vec::new();
    if q.is_empty() {
        app.search.results.clear();
        app.search.cursor = 0;
        return;
    }
    for t in &app.tools {
        if matches_q(&q, &t.name) || matches_q(&q, &t.id) || matches_q(&q, &t.description) {
            hits.push(SearchHit {
                kind: SearchKind::Tool,
                id: t.id.clone(),
                name: t.name.clone(),
                description: t.description.clone(),
            });
        }
    }
    for p in &app.profiles {
        if matches_q(&q, &p.name) || matches_q(&q, &p.id) || matches_q(&q, &p.description) {
            hits.push(SearchHit {
                kind: SearchKind::Profile,
                id: p.id.clone(),
                name: p.name.clone(),
                description: p.description.clone(),
            });
        }
    }
    for d in &app.dotfiles {
        if matches_q(&q, &d.name) || matches_q(&q, &d.id) || matches_q(&q, &d.description) {
            hits.push(SearchHit {
                kind: SearchKind::Dotfile,
                id: d.id.clone(),
                name: d.name.clone(),
                description: d.description.clone(),
            });
        }
    }
    app.search.results = hits;
    if app.search.cursor >= app.search.results.len() {
        app.search.cursor = 0;
    }
}

fn matches_q(q: &str, hay: &str) -> bool {
    hay.to_lowercase().contains(q)
}

