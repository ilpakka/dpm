//! Top-level UI dispatcher. Each tab has its own renderer in this module.
//!
//! Layout (matches the Bubble Tea TUI):
//!
//! 1. Compute a centered "frame" rect, clamped to MAX_W × MAX_H.
//! 2. Accent rounded border around the frame.
//! 3. Inside: header (1) / tab strip (1) / breadcrumb (1) / cart bar (0-3)
//!    / body (min) / message (0-1) / footer (1).
//! 4. Popup / search / settings overlays render on top of the same frame.

pub mod theme;

mod breadcrumb;
mod bubble;
mod cart;
mod doctor;
mod dotfiles;
mod footer;
mod header;
mod help;
mod import;
mod installed;
mod menu;
mod packages;
mod popup;
mod profiles;
mod search;
mod settings;
mod tabs;
mod update;

use ratatui::layout::{Constraint, Direction, Layout, Rect};
use ratatui::style::Style;
use ratatui::widgets::{Block, BorderType, Borders, Clear};
use ratatui::Frame;

use crate::app::{App, GlobalView, SubView, Tab};

/// Main draw entrypoint. Called every frame.
pub fn draw(f: &mut Frame<'_>, app: &App) {
    let t = theme::current();
    let term = f.area();

    let bg = Block::default().style(Style::default().bg(t.bg));
    f.render_widget(bg, term);

    let frame = compute_frame(term);

    let border = Block::default()
        .borders(Borders::ALL)
        .border_type(BorderType::Rounded)
        .border_style(Style::default().fg(t.accent))
        .style(Style::default().bg(t.bg));
    f.render_widget(border, frame);

    let inner = inner_rect(frame);

    let chunks = Layout::default()
        .direction(Direction::Vertical)
        .constraints([
            Constraint::Length(1), // header
            Constraint::Length(1), // spacer above tabs
            Constraint::Length(2), // tabs (label + underline)
            Constraint::Length(1), // spacer below tabs
            Constraint::Length(1), // breadcrumb
            Constraint::Length(if app.cart.is_empty() { 0 } else { 3 }), // cart bar
            Constraint::Min(3),    // body
            Constraint::Length(1), // footer
        ])
        .split(inner);

    header::render(f, chunks[0], app);
    // chunks[1] = spacer above tabs
    tabs::render(f, chunks[2], app);
    // chunks[3] = spacer below tabs
    breadcrumb::render(f, chunks[4], app);
    if !app.cart.is_empty() {
        cart::render(f, chunks[5], app);
    }
    render_body(f, chunks[6], app);
    footer::render(f, chunks[7], app);

    // Overlays — drawn over the framed content.
    if app.search.open {
        search::render(f, frame, app);
    } else if app.settings.open {
        settings::render(f, frame, app);
    } else if matches!(app.global_view, GlobalView::Menu { .. }) {
        menu::render(f, frame, app);
    } else if app.popup.is_some() {
        popup::render(f, frame, app);
    }
}

/// Returns the centered frame rect inside `term`, clamped to MAX_W × MAX_H.
pub fn compute_frame(term: Rect) -> Rect {
    let mut w = term.width.saturating_sub(2);
    if w < theme::MIN_W {
        w = theme::MIN_W.min(term.width);
    }
    if w > theme::MAX_W {
        w = theme::MAX_W;
    }
    let mut h = term.height.saturating_sub(2);
    if h < theme::MIN_H {
        h = theme::MIN_H.min(term.height);
    }
    if h > theme::MAX_H {
        h = theme::MAX_H;
    }
    let x = term.x + (term.width.saturating_sub(w)) / 2;
    let y = term.y + (term.height.saturating_sub(h)) / 2;
    Rect {
        x,
        y,
        width: w,
        height: h,
    }
}

/// Inner area of the frame: strip the 1-cell border and add a 1-cell side pad.
fn inner_rect(frame: Rect) -> Rect {
    let inset = Rect {
        x: frame.x + 1,
        y: frame.y + 1,
        width: frame.width.saturating_sub(2),
        height: frame.height.saturating_sub(2),
    };
    Rect {
        x: inset.x + 1,
        y: inset.y,
        width: inset.width.saturating_sub(2),
        height: inset.height,
    }
}

fn render_body(f: &mut Frame<'_>, area: Rect, app: &App) {
    let t = theme::current();
    f.render_widget(Clear, area);
    let bg = Block::default().style(Style::default().bg(t.bg));
    f.render_widget(bg, area);

    match &app.global_view {
        GlobalView::Help => {
            help::render(f, area);
            return;
        }
        GlobalView::Doctor => {
            doctor::render(f, area, app);
            return;
        }
        GlobalView::Update => {
            update::render(f, area, app);
            return;
        }
        _ => {}
    }

    if !matches!(app.sub_view, SubView::None) {
        import::render(f, area, app);
        return;
    }

    match app.tab() {
        Tab::Profiles => profiles::render(f, area, app),
        Tab::Tools => packages::render(f, area, app),
        Tab::Installed => installed::render(f, area, app),
        Tab::Dotfiles => dotfiles::render(f, area, app),
        Tab::Bubble => bubble::render(f, area, app),
    }
}
