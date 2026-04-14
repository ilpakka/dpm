//! Popup overlays — confirm, progress, result.

use ratatui::layout::{Constraint, Direction, Layout, Margin, Rect};
use ratatui::style::{Modifier, Style};
use ratatui::text::{Line, Span};
use ratatui::widgets::{Block, BorderType, Borders, Clear, Paragraph};
use ratatui::Frame;

use super::theme;
use crate::app::{App, Popup};

pub fn render(f: &mut Frame<'_>, full: Rect, app: &App) {
    let t = theme::current();
    let Some(popup) = &app.popup else {
        return;
    };
    let area = centered(full, 60, 60);
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

    match popup {
        Popup::Confirm {
            title,
            message,
            yes_focused,
        } => render_confirm(f, inner, title, message, *yes_focused, t),
        Popup::Progress {
            title,
            message,
            log,
            spinner_idx,
        } => render_progress(f, inner, title, message, log, *spinner_idx, app.anim_tick, t),
        Popup::Result {
            title,
            message,
            ok,
        } => render_result(f, inner, title, message, *ok, t),
    }
}

fn render_confirm(f: &mut Frame<'_>, area: Rect, title: &str, message: &str, yes_focused: bool, t: &theme::Theme) {
    let chunks = Layout::default()
        .direction(Direction::Vertical)
        .constraints([
            Constraint::Length(1),
            Constraint::Length(1),
            Constraint::Min(1),
            Constraint::Length(1),
            Constraint::Length(1),
            Constraint::Length(1),
            Constraint::Length(1),
        ])
        .split(area);

    f.render_widget(
        Paragraph::new(Line::from(Span::styled(
            title.to_string(),
            Style::default().fg(t.accent).add_modifier(Modifier::BOLD),
        )))
        .style(Style::default().bg(t.bg_darker)),
        chunks[0],
    );

    f.render_widget(
        Paragraph::new(Line::from(Span::styled(
            message.to_string(),
            Style::default().fg(t.fg),
        )))
        .style(Style::default().bg(t.bg_darker)),
        chunks[2],
    );

    let yes_style = button_style(yes_focused, t);
    let no_style = button_style(!yes_focused, t);
    let buttons = Line::from(vec![
        Span::styled("    ", Style::default().bg(t.bg_darker)),
        Span::styled("  Yes  ", yes_style),
        Span::styled("    ", Style::default().bg(t.bg_darker)),
        Span::styled("  No  ", no_style),
    ]);
    f.render_widget(
        Paragraph::new(buttons).style(Style::default().bg(t.bg_darker)),
        chunks[4],
    );

    f.render_widget(
        Paragraph::new(Line::from(Span::styled(
            "← → switch · ENTER confirm · ESC cancel",
            Style::default().fg(t.fg_dim),
        )))
        .style(Style::default().bg(t.bg_darker)),
        chunks[6],
    );
}

fn button_style(focused: bool, t: &theme::Theme) -> Style {
    if focused {
        Style::default()
            .fg(t.on_accent)
            .bg(t.accent)
            .add_modifier(Modifier::BOLD)
    } else {
        Style::default()
            .fg(t.fg_muted)
            .bg(t.bg_darker)
            .add_modifier(Modifier::BOLD)
    }
}

fn render_progress(
    f: &mut Frame<'_>,
    area: Rect,
    title: &str,
    message: &str,
    log: &[String],
    spinner_idx: usize,
    anim_tick: u64,
    t: &theme::Theme,
) {
    let chunks = Layout::default()
        .direction(Direction::Vertical)
        .constraints([
            Constraint::Length(1),
            Constraint::Length(1),
            Constraint::Length(1),
            Constraint::Min(1),
        ])
        .split(area);

    f.render_widget(
        Paragraph::new(Line::from(Span::styled(
            title.to_string(),
            Style::default().fg(t.accent).add_modifier(Modifier::BOLD),
        )))
        .style(Style::default().bg(t.bg_darker)),
        chunks[0],
    );

    let frame = theme::SPINNER_FRAMES[spinner_idx % theme::SPINNER_FRAMES.len()];
    let spinner_color = t.rainbow[(anim_tick as usize) % t.rainbow.len()];
    let header = Line::from(vec![
        Span::styled(
            frame.to_string(),
            Style::default().fg(spinner_color).add_modifier(Modifier::BOLD),
        ),
        Span::raw(" "),
        Span::styled(message.to_string(), Style::default().fg(t.fg)),
    ]);
    f.render_widget(
        Paragraph::new(header).style(Style::default().bg(t.bg_darker)),
        chunks[1],
    );

    let max = chunks[3].height as usize;
    let start = log.len().saturating_sub(max);
    let log_lines: Vec<Line> = log[start..]
        .iter()
        .map(|l| {
            Line::from(Span::styled(
                format!("  {}", l),
                Style::default().fg(t.fg_muted),
            ))
        })
        .collect();
    let log_para = Paragraph::new(log_lines).style(Style::default().bg(t.bg_darker));
    f.render_widget(log_para, chunks[3]);
}

fn render_result(f: &mut Frame<'_>, area: Rect, title: &str, message: &str, ok: bool, t: &theme::Theme) {
    let title_color = if ok { t.success } else { t.error };
    let lines = vec![
        Line::from(Span::styled(
            title.to_string(),
            Style::default().fg(title_color).add_modifier(Modifier::BOLD),
        )),
        Line::from(""),
        Line::from(Span::styled(
            message.to_string(),
            Style::default().fg(t.fg),
        )),
        Line::from(""),
        Line::from(Span::styled(
            "press any key to dismiss",
            Style::default().fg(t.fg_dim),
        )),
    ];
    let para = Paragraph::new(lines).style(Style::default().bg(t.bg_darker));
    f.render_widget(para, area);
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
