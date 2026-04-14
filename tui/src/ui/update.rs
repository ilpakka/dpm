//! Update view — shows the latest checkUpdates snapshot.

use ratatui::layout::{Margin, Rect};
use ratatui::style::{Modifier, Style};
use ratatui::text::{Line, Span};
use ratatui::widgets::{Block, BorderType, Borders, Clear, Paragraph};
use ratatui::Frame;

use super::theme;
use crate::app::App;

pub fn render(f: &mut Frame<'_>, area: Rect, app: &App) {
    let t = theme::current();
    f.render_widget(Clear, area);
    let block = Block::default()
        .borders(Borders::ALL)
        .border_type(BorderType::Rounded)
        .border_style(Style::default().fg(t.accent))
        .title(Span::styled(
            " updates ",
            Style::default().fg(t.accent).add_modifier(Modifier::BOLD),
        ));
    let inner = block.inner(area);
    f.render_widget(block, area);

    let body = inner.inner(Margin {
        horizontal: 2,
        vertical: 1,
    });

    let mut lines: Vec<Line> = Vec::new();
    if app.update_status.is_empty() {
        lines.push(Line::from(Span::styled(
            "Checking for updates…",
            Style::default().fg(t.fg_dim),
        )));
    } else {
        let needs: Vec<_> = app
            .update_status
            .iter()
            .filter(|u| u.update_required)
            .collect();
        lines.push(Line::from(vec![
            Span::styled(
                format!("{} ", needs.len()),
                Style::default().fg(t.accent).add_modifier(Modifier::BOLD),
            ),
            Span::styled(
                "tools have updates available",
                Style::default().fg(t.fg),
            ),
        ]));
        lines.push(Line::from(""));
        for (i, u) in app.update_status.iter().enumerate() {
            let is_cursor = i == app.update_cursor;
            let row_style = if is_cursor {
                Style::default()
                    .fg(t.on_accent)
                    .bg(t.accent)
                    .add_modifier(Modifier::BOLD)
            } else {
                Style::default().fg(t.fg)
            };
            let status = if u.not_in_catalog {
                Span::styled(
                    " not in catalog ",
                    if is_cursor { row_style } else { Style::default().fg(t.warning) },
                )
            } else if u.update_required {
                Span::styled(
                    " needs update ",
                    if is_cursor {
                        row_style
                    } else {
                        Style::default()
                            .fg(t.on_accent)
                            .bg(t.accent)
                            .add_modifier(Modifier::BOLD)
                    },
                )
            } else {
                Span::styled(
                    " up-to-date ",
                    if is_cursor { row_style } else { Style::default().fg(t.success) },
                )
            };
            lines.push(Line::from(vec![
                Span::styled("  ", row_style),
                Span::styled(pad(&u.tool_id, 22), row_style.add_modifier(Modifier::BOLD)),
                Span::styled(
                    format!(" {} → {} ", u.installed_ver, u.available_ver),
                    if is_cursor { row_style } else { Style::default().fg(t.fg_dim) },
                ),
                status,
            ]));
        }
        lines.push(Line::from(""));
        lines.push(Line::from(Span::styled(
            "ENTER update one · 'U' update all · ESC close",
            Style::default().fg(t.fg_dim),
        )));
    }

    f.render_widget(Paragraph::new(lines), body);
}

fn pad(s: &str, w: usize) -> String {
    if s.chars().count() >= w {
        s.to_string()
    } else {
        let mut out = s.to_string();
        out.push_str(&" ".repeat(w - s.chars().count()));
        out
    }
}
