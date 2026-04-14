//! Search overlay (`/`) — DoubleBorder popup with live filter results.

use ratatui::layout::{Constraint, Direction, Layout, Margin, Rect};
use ratatui::style::{Modifier, Style};
use ratatui::text::{Line, Span};
use ratatui::widgets::{Block, BorderType, Borders, Clear, Paragraph};
use ratatui::Frame;

use super::theme;
use crate::app::{App, SearchKind};

pub fn render(f: &mut Frame<'_>, frame: Rect, app: &App) {
    let t = theme::current();
    let area = centered(frame, 60, 70);
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
            Constraint::Length(3),
            Constraint::Length(1),
            Constraint::Min(1),
        ])
        .split(inner);

    let title = Paragraph::new(Line::from(Span::styled(
        " Search",
        Style::default().fg(t.accent).add_modifier(Modifier::BOLD),
    )))
    .style(Style::default().bg(t.bg_darker));
    f.render_widget(title, chunks[0]);

    let input_box = Block::default()
        .borders(Borders::ALL)
        .border_type(BorderType::Rounded)
        .border_style(Style::default().fg(t.accent))
        .style(Style::default().bg(t.bg_darker));
    let input_text = format!(" {}█", app.search.input);
    let input = Paragraph::new(input_text)
        .block(input_box)
        .style(Style::default().fg(t.fg).bg(t.bg_darker));
    f.render_widget(input, chunks[2]);

    let mut lines: Vec<Line> = Vec::new();
    if app.search.results.is_empty() {
        if !app.search.input.is_empty() {
            lines.push(Line::from(Span::styled(
                "  No results",
                Style::default().fg(t.fg_dim),
            )));
        } else {
            lines.push(Line::from(Span::styled(
                "  type to filter…",
                Style::default().fg(t.fg_dim),
            )));
        }
    } else {
        let max_show = chunks[4].height as usize;
        for (i, hit) in app.search.results.iter().take(max_show).enumerate() {
            let badge = badge(hit.kind, t);
            let pointer = if i == app.search.cursor {
                Span::styled(
                    "▸ ",
                    Style::default().fg(t.accent).add_modifier(Modifier::BOLD),
                )
            } else {
                Span::raw("  ")
            };
            let name_style = if i == app.search.cursor {
                Style::default().fg(t.accent).add_modifier(Modifier::BOLD)
            } else {
                Style::default().fg(t.fg)
            };
            let mut spans = vec![
                pointer,
                badge,
                Span::raw(" "),
                Span::styled(hit.name.clone(), name_style),
            ];
            if !hit.description.is_empty() {
                spans.push(Span::raw("  "));
                spans.push(Span::styled(
                    hit.description.clone(),
                    Style::default().fg(t.fg_dim),
                ));
            }
            lines.push(Line::from(spans));
        }
        if app.search.results.len() > chunks[4].height as usize {
            lines.push(Line::from(Span::styled(
                format!(
                    "  ... +{} more",
                    app.search.results.len() - chunks[4].height as usize
                ),
                Style::default().fg(t.fg_dim),
            )));
        }
    }
    let results = Paragraph::new(lines).style(Style::default().bg(t.bg_darker));
    f.render_widget(results, chunks[4]);
}

fn badge(kind: SearchKind, t: &theme::Theme) -> Span<'static> {
    let (text, bg) = match kind {
        SearchKind::Tool => ("tool", t.bg_selected),
        SearchKind::Profile => ("prof", t.accent),
        SearchKind::Dotfile => ("dot ", t.success_bg),
    };
    let fg = if matches!(kind, SearchKind::Profile) {
        t.on_accent
    } else {
        t.fg
    };
    Span::styled(
        format!(" {} ", text),
        Style::default().fg(fg).bg(bg).add_modifier(Modifier::BOLD),
    )
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
