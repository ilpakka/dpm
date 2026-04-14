use ratatui::layout::Rect;
use ratatui::style::{Modifier, Style};
use ratatui::text::{Line, Span};
use ratatui::widgets::Paragraph;
use ratatui::Frame;
use unicode_width::UnicodeWidthStr;

use super::theme;
use crate::app::{App, Popup};

/// Top brand bar — `Dumb  Pckt  Mang` + `R` plus right-aligned status.
pub fn render(f: &mut Frame<'_>, area: Rect, app: &App) {
    let t = theme::current();
    let brand_left = "Dumb  Pckt  Mang";
    let brand_r = "R";

    let (status_text, status_color) = match &app.popup {
        Some(Popup::Progress { spinner_idx, .. }) => {
            let frame = theme::SPINNER_FRAMES[spinner_idx % theme::SPINNER_FRAMES.len()];
            (format!("{} Working...", frame), t.accent)
        }
        _ => ("[*] Normal".to_string(), t.accent),
    };

    let version = " v0.51 · ";
    let plat = if app.platform.is_empty() {
        "—"
    } else {
        app.platform.as_str()
    };
    let dots = "  ···";

    let left_w = (brand_left.width() + brand_r.width()) as u16 + 2;
    let right_w = (status_text.width() + version.width() + plat.width() + dots.width()) as u16;
    let total_w = left_w + right_w;
    let gap = area.width.saturating_sub(total_w).max(1);

    let line = Line::from(vec![
        Span::raw(" "),
        Span::styled(
            brand_left,
            Style::default()
                .fg(t.fg)
                .add_modifier(Modifier::BOLD | Modifier::ITALIC),
        ),
        Span::styled(
            brand_r,
            Style::default()
                .fg(t.accent)
                .add_modifier(Modifier::BOLD | Modifier::ITALIC),
        ),
        Span::raw(" ".repeat(gap as usize)),
        Span::styled(
            status_text,
            Style::default().fg(status_color).add_modifier(Modifier::BOLD),
        ),
        Span::styled(version, Style::default().fg(t.fg_dim)),
        Span::styled(plat.to_string(), Style::default().fg(t.fg_dim)),
        Span::styled(dots, Style::default().fg(t.fg_dim)),
    ]);
    let para = Paragraph::new(line).style(Style::default().bg(t.bg_darker));
    f.render_widget(para, area);
}
