//! Settings overlay — DoubleBorder popup with grouped settings.

use ratatui::layout::{Constraint, Direction, Layout, Margin, Rect};
use ratatui::style::{Modifier, Style};
use ratatui::text::{Line, Span};
use ratatui::widgets::{Block, BorderType, Borders, Clear, Paragraph};
use ratatui::Frame;

use super::theme;
use crate::app::App;

pub fn render(f: &mut Frame<'_>, frame: Rect, app: &App) {
    let t = theme::current();
    let area = centered(frame, 70, 70);
    f.render_widget(Clear, area);

    let block = Block::default()
        .borders(Borders::ALL)
        .border_type(BorderType::Double)
        .border_style(Style::default().fg(t.accent))
        .style(Style::default().bg(t.bg_darker));
    f.render_widget(block, area);

    let inner = area.inner(Margin {
        horizontal: 2,
        vertical: 1,
    });

    let chunks = Layout::default()
        .direction(Direction::Vertical)
        .constraints([
            Constraint::Length(1),
            Constraint::Length(1),
            Constraint::Length(1),
            Constraint::Length(1),
            Constraint::Min(1),
            Constraint::Length(1),
        ])
        .split(inner);

    let title = Paragraph::new(Line::from(Span::styled(
        " Settings",
        Style::default().fg(t.accent).add_modifier(Modifier::BOLD),
    )))
    .style(Style::default().bg(t.bg_darker));
    f.render_widget(title, chunks[0]);

    // Group tabs.
    let mut tab_spans: Vec<Span> = Vec::new();
    for (i, g) in app.settings.groups.iter().enumerate() {
        if i > 0 {
            tab_spans.push(Span::raw("  "));
        }
        let style = if i == app.settings.group_idx {
            Style::default()
                .fg(t.on_accent)
                .bg(t.accent)
                .add_modifier(Modifier::BOLD)
        } else {
            Style::default().fg(t.fg_muted)
        };
        tab_spans.push(Span::styled(format!(" {} ", g.name), style));
    }
    f.render_widget(
        Paragraph::new(Line::from(tab_spans)).style(Style::default().bg(t.bg_darker)),
        chunks[2],
    );

    // Settings list.
    let mut lines: Vec<Line> = Vec::new();
    if let Some(group) = app.settings.groups.get(app.settings.group_idx) {
        for (i, s) in group.settings.iter().enumerate() {
            let cursor = if i == app.settings.cursor { "▌ " } else { "  " };
            let val = match s.kind.as_str() {
                "bool" => {
                    if s.value == "true" {
                        Span::styled(
                            " [on]  ",
                            Style::default().fg(t.success).add_modifier(Modifier::BOLD),
                        )
                    } else {
                        Span::styled(" [off] ", Style::default().fg(t.fg_dim))
                    }
                }
                "action" => Span::styled(
                    " open ⏎ ",
                    Style::default()
                        .fg(t.on_accent)
                        .bg(t.accent)
                        .add_modifier(Modifier::BOLD),
                ),
                _ => {
                    if i == app.settings.cursor && app.settings.editing {
                        Span::styled(
                            format!(" {}_ ", app.settings.edit_buffer),
                            Style::default()
                                .fg(t.on_accent)
                                .bg(t.accent)
                                .add_modifier(Modifier::BOLD),
                        )
                    } else {
                        Span::styled(format!(" {} ", s.value), Style::default().fg(t.fg))
                    }
                }
            };
            let modified = if s.value != s.default {
                Span::styled(
                    " [modified]",
                    Style::default().fg(t.accent).add_modifier(Modifier::BOLD),
                )
            } else {
                Span::raw("")
            };
            let name_style = if i == app.settings.cursor {
                Style::default().fg(t.accent).add_modifier(Modifier::BOLD)
            } else {
                Style::default().fg(t.fg)
            };
            lines.push(Line::from(vec![
                Span::styled(cursor, Style::default().fg(t.accent)),
                Span::styled(format!("{:<28}", s.name), name_style),
                val,
                modified,
            ]));
            lines.push(Line::from(Span::styled(
                format!("    {}", s.description),
                Style::default().fg(t.fg_dim),
            )));
        }
    }
    f.render_widget(
        Paragraph::new(lines).style(Style::default().bg(t.bg_darker)),
        chunks[4],
    );

    // Hint.
    let hint_text = if app.settings.editing {
        "ESC cancel · ENTER save · type to edit"
    } else {
        "ESC close · ENTER toggle/edit · r reset · t theme"
    };
    let hint = Paragraph::new(Line::from(Span::styled(
        hint_text,
        Style::default().fg(t.fg_dim),
    )))
    .style(Style::default().bg(t.bg_darker));
    f.render_widget(hint, chunks[5]);
}

fn centered(area: Rect, percent_x: u16, percent_y: u16) -> Rect {
    let v = Layout::default()
        .direction(Direction::Vertical)
        .constraints([
            Constraint::Percentage((100 - percent_y) / 2),
            Constraint::Percentage(percent_y),
            Constraint::Percentage((100 - percent_y) / 2),
        ])
        .split(area);
    Layout::default()
        .direction(Direction::Horizontal)
        .constraints([
            Constraint::Percentage((100 - percent_x) / 2),
            Constraint::Percentage(percent_x),
            Constraint::Percentage((100 - percent_x) / 2),
        ])
        .split(v[1])[1]
}
