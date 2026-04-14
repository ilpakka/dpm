use ratatui::layout::Rect;
use ratatui::style::{Modifier, Style};
use ratatui::text::{Line, Span};
use ratatui::widgets::Paragraph;
use ratatui::Frame;
use unicode_width::UnicodeWidthStr;

use super::theme;
use crate::app::{App, MsgKind, Popup, Tab};

/// Bottom hint bar — accent chip keys + muted descriptions on the right,
/// animated star row on the left. If a status message is active it overrides
/// the hint line for that frame.
pub fn render(f: &mut Frame<'_>, area: Rect, app: &App) {
    let t = theme::current();

    if let Some(msg) = app.message.as_ref() {
        let (fg, bg) = match msg.kind {
            MsgKind::Info => (t.on_accent, t.info),
            MsgKind::Success => (t.on_accent, t.success),
            MsgKind::Error => (t.fg, t.error),
        };
        let chip = Span::styled(
            format!(" {} ", msg.text),
            Style::default().fg(fg).bg(bg).add_modifier(Modifier::BOLD),
        );
        let para = Paragraph::new(Line::from(vec![Span::raw("  "), chip]))
            .style(Style::default().bg(t.bg));
        f.render_widget(para, area);
        return;
    }

    let stars = star_spans(app, t);
    let hints = hint_spans(app, t);

    let stars_w: usize = stars.iter().map(|s| s.content.width()).sum();
    let hints_w: usize = hints.iter().map(|s| s.content.width()).sum();
    let pad = (area.width as usize)
        .saturating_sub(stars_w + hints_w + 4)
        .max(1);

    let mut spans = Vec::with_capacity(stars.len() + hints.len() + 3);
    spans.push(Span::raw("  "));
    spans.extend(stars);
    spans.push(Span::raw(" ".repeat(pad)));
    spans.extend(hints);
    spans.push(Span::raw("  "));

    let para = Paragraph::new(Line::from(spans)).style(Style::default().bg(t.bg_darker));
    f.render_widget(para, area);
}

fn star_spans(app: &App, t: &theme::Theme) -> Vec<Span<'static>> {
    let star_str = "★★★★★★★";
    if matches!(app.popup, Some(Popup::Progress { .. })) {
        let mut out = Vec::new();
        for (i, ch) in star_str.chars().enumerate() {
            let color = t.rainbow[(app.anim_tick as usize + i) % t.rainbow.len()];
            out.push(Span::styled(ch.to_string(), Style::default().fg(color)));
        }
        out
    } else {
        vec![Span::styled(
            star_str.to_string(),
            Style::default().fg(t.accent),
        )]
    }
}

fn hint_spans(app: &App, t: &theme::Theme) -> Vec<Span<'static>> {
    let mut out: Vec<(&'static str, &'static str)> = Vec::new();

    if app.search.open {
        out.push(("ESC", "close"));
        out.push(("ENTER", "go"));
        out.push(("↑↓", "nav"));
    } else if app.settings.open {
        out.push(("ESC", "close"));
        out.push(("ENTER", "toggle"));
        out.push(("r", "reset"));
    } else if app.popup.is_some() {
        out.push(("?", ""));
    } else if app.depth > 0 {
        out.push(("ESC", "back"));
        out.push(("ENTER", "action"));
        if matches!(app.tab(), Tab::Tools | Tab::Installed) {
            out.push(("d", "remove"));
        }
    } else {
        out.push(("TAB", "tab"));
        out.push(("ENTER", "open"));
        out.push(("SPACE", "select"));
        out.push(("/", "search"));
        out.push((",", "settings"));
        if matches!(app.tab(), Tab::Installed) {
            out.push(("o", "open"));
            out.push(("d", "remove"));
        }
        if matches!(app.tab(), Tab::Dotfiles) {
            out.push(("a", "add repo"));
        }
        if !app.cart.is_empty() {
            out.push(("^A", "install all"));
        }
    }

    let mut spans: Vec<Span<'static>> = Vec::new();
    for (i, (k, d)) in out.iter().enumerate() {
        if i > 0 {
            spans.push(Span::raw("  "));
        }
        spans.push(Span::styled(
            format!(" {} ", k),
            Style::default()
                .fg(t.on_accent)
                .bg(t.accent)
                .add_modifier(Modifier::BOLD),
        ));
        if !d.is_empty() {
            spans.push(Span::raw(" "));
            spans.push(Span::styled(d.to_string(), Style::default().fg(t.fg_dim)));
        }
    }
    spans
}
