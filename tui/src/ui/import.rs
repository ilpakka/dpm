//! Sub-views for importing dotfiles from a custom git repository.
//!
//! Two phases:
//! 1. AddCustomRepo — single text input for the repo URL.
//! 2. DotfilesImport — multi-select list of detected configs to apply.

use ratatui::layout::{Constraint, Direction, Layout, Margin, Rect};
use ratatui::style::{Modifier, Style};
use ratatui::text::{Line, Span};
use ratatui::widgets::{Block, BorderType, Borders, Clear, Paragraph};
use ratatui::Frame;

use super::theme;
use crate::app::{App, SubView};
use crate::rpc::types::DetectedConfig;

pub fn render(f: &mut Frame<'_>, area: Rect, app: &App) {
    let t = theme::current();
    f.render_widget(Clear, area);
    let bg = ratatui::widgets::Block::default().style(Style::default().bg(t.bg));
    f.render_widget(bg, area);

    match &app.sub_view {
        SubView::None => {}
        SubView::AddCustomRepo { input } => render_add_repo(f, area, input, t),
        SubView::DotfilesImport {
            repo_dir,
            configs,
            cursor,
            selected,
        } => render_import_list(f, area, repo_dir, configs, *cursor, selected, t),
    }
}

fn render_add_repo(f: &mut Frame<'_>, area: Rect, input: &str, t: &theme::Theme) {
    let block = Block::default()
        .borders(Borders::ALL)
        .border_type(BorderType::Rounded)
        .border_style(Style::default().fg(t.accent))
        .title(Span::styled(
            " import dotfiles ",
            Style::default().fg(t.accent).add_modifier(Modifier::BOLD),
        ));
    let inner = block.inner(area);
    f.render_widget(block, area);

    let chunks = Layout::default()
        .direction(Direction::Vertical)
        .constraints([
            Constraint::Length(2),
            Constraint::Length(3),
            Constraint::Length(2),
            Constraint::Min(0),
        ])
        .split(inner.inner(Margin {
            horizontal: 2,
            vertical: 1,
        }));

    f.render_widget(
        Paragraph::new(Line::from(Span::styled(
            "Paste a git URL (e.g. https://github.com/user/dotfiles)",
            Style::default().fg(t.fg_dim),
        ))),
        chunks[0],
    );

    let input_block = Block::default()
        .borders(Borders::ALL)
        .border_type(BorderType::Rounded)
        .border_style(Style::default().fg(t.accent));
    let input_inner = input_block.inner(chunks[1]);
    f.render_widget(input_block, chunks[1]);
    f.render_widget(
        Paragraph::new(Line::from(vec![
            Span::raw(" "),
            Span::styled(
                input.to_string(),
                Style::default().fg(t.fg).add_modifier(Modifier::BOLD),
            ),
            Span::styled(
                "_",
                Style::default().fg(t.accent).add_modifier(Modifier::BOLD),
            ),
        ])),
        input_inner,
    );

    f.render_widget(
        Paragraph::new(Line::from(Span::styled(
            "ENTER scan · ESC cancel",
            Style::default().fg(t.fg_dim),
        ))),
        chunks[2],
    );
}

fn render_import_list(
    f: &mut Frame<'_>,
    area: Rect,
    repo_dir: &str,
    configs: &[DetectedConfig],
    cursor: usize,
    selected: &std::collections::HashSet<usize>,
    t: &theme::Theme,
) {
    let block = Block::default()
        .borders(Borders::ALL)
        .border_type(BorderType::Rounded)
        .border_style(Style::default().fg(t.accent))
        .title(Span::styled(
            " select configs to import ",
            Style::default().fg(t.accent).add_modifier(Modifier::BOLD),
        ));
    let inner = block.inner(area);
    f.render_widget(block, area);

    let chunks = Layout::default()
        .direction(Direction::Vertical)
        .constraints([
            Constraint::Length(2),
            Constraint::Min(1),
            Constraint::Length(2),
        ])
        .split(inner.inner(Margin {
            horizontal: 2,
            vertical: 1,
        }));

    f.render_widget(
        Paragraph::new(Line::from(vec![
            Span::styled("repo: ", Style::default().fg(t.fg_dim)),
            Span::styled(repo_dir.to_string(), Style::default().fg(t.fg)),
            Span::styled(
                format!("    {} selected", selected.len()),
                Style::default().fg(t.accent),
            ),
        ])),
        chunks[0],
    );

    let mut lines: Vec<Line> = Vec::new();
    if configs.is_empty() {
        lines.push(Line::from(Span::styled(
            "  No configs detected in the repository.",
            Style::default().fg(t.fg_dim),
        )));
    } else {
        for (i, c) in configs.iter().enumerate() {
            let is_cursor = i == cursor;
            let is_sel = selected.contains(&i);
            let row_style = if is_cursor {
                Style::default()
                    .fg(t.on_accent)
                    .bg(t.accent)
                    .add_modifier(Modifier::BOLD)
            } else {
                Style::default().fg(t.fg)
            };
            let checkbox = if is_sel { "[x] " } else { "[ ] " };
            let kind = if c.is_script {
                Span::styled(
                    " script ",
                    if is_cursor {
                        row_style
                    } else {
                        Style::default().fg(t.warning).add_modifier(Modifier::BOLD)
                    },
                )
            } else {
                Span::styled(
                    " config ",
                    if is_cursor { row_style } else { Style::default().fg(t.fg_dim) },
                )
            };
            lines.push(Line::from(vec![
                Span::styled("  ", row_style),
                Span::styled(checkbox, row_style),
                kind,
                Span::styled(" ", row_style),
                Span::styled(c.name.clone(), row_style.add_modifier(Modifier::BOLD)),
                Span::styled("  ", row_style),
                Span::styled(
                    format!("→ {}", c.target),
                    if is_cursor { row_style } else { Style::default().fg(t.fg_dim) },
                ),
            ]));
        }
    }
    f.render_widget(Paragraph::new(lines), chunks[1]);

    f.render_widget(
        Paragraph::new(Line::from(Span::styled(
            "SPACE toggle · a all · n none · ENTER apply · ESC cancel",
            Style::default().fg(t.fg_dim),
        ))),
        chunks[2],
    );
}
