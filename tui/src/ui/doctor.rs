//! Doctor view — health-check report from `engine.doctor`.

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
            " doctor ",
            Style::default().fg(t.accent).add_modifier(Modifier::BOLD),
        ));
    let inner = block.inner(area);
    f.render_widget(block, area);

    let body = inner.inner(Margin {
        horizontal: 2,
        vertical: 1,
    });

    let mut lines: Vec<Line> = Vec::new();
    if let Some(report) = &app.doctor_report {
        lines.push(Line::from(vec![
            Span::styled("platform: ", Style::default().fg(t.fg_dim)),
            Span::styled(report.platform.clone(), Style::default().fg(t.fg)),
        ]));
        lines.push(Line::from(vec![
            Span::styled("dpm root: ", Style::default().fg(t.fg_dim)),
            Span::styled(report.dpm_root.clone(), Style::default().fg(t.fg)),
        ]));
        lines.push(Line::from(vec![
            Span::styled("in PATH:  ", Style::default().fg(t.fg_dim)),
            Span::styled(
                if report.in_path { "yes" } else { "no" },
                Style::default().fg(if report.in_path { t.success } else { t.error }),
            ),
        ]));
        lines.push(Line::from(vec![
            Span::styled("theme:    ", Style::default().fg(t.fg_dim)),
            Span::styled(t.name, Style::default().fg(t.accent)),
        ]));
        lines.push(Line::from(""));
        lines.push(Line::from(Span::styled(
            "checks:",
            Style::default().fg(t.fg_dim),
        )));
        for c in &report.checks {
            let (icon, color) = match c.severity.as_str() {
                "error" => ("✗", t.error),
                "warn" => ("⚠", t.warning),
                _ => {
                    if c.ok {
                        ("✓", t.success)
                    } else {
                        ("•", t.fg_dim)
                    }
                }
            };
            lines.push(Line::from(vec![
                Span::raw("  "),
                Span::styled(icon.to_string(), Style::default().fg(color)),
                Span::raw("  "),
                Span::styled(
                    pad(&c.name, 22),
                    Style::default().fg(t.fg).add_modifier(Modifier::BOLD),
                ),
                Span::raw("  "),
                Span::styled(c.message.clone(), Style::default().fg(t.fg_dim)),
            ]));
        }
    } else {
        lines.push(Line::from(Span::styled(
            "Running diagnostics…",
            Style::default().fg(t.fg_dim),
        )));
    }
    lines.push(Line::from(""));
    lines.push(Line::from(Span::styled(
        "ESC close",
        Style::default().fg(t.fg_dim),
    )));
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
