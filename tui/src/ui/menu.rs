//! Ctrl-K global menu — small centered popup.

use ratatui::layout::{Constraint, Direction, Layout, Margin, Rect};
use ratatui::style::{Modifier, Style};
use ratatui::text::{Line, Span};
use ratatui::widgets::{Block, BorderType, Borders, Clear, Paragraph};
use ratatui::Frame;

use super::theme;
use crate::app::{App, GlobalView};

pub fn render(f: &mut Frame<'_>, frame: Rect, app: &App) {
    let t = theme::current();
    let cursor = match app.global_view {
        GlobalView::Menu { cursor } => cursor,
        _ => return,
    };
    let area = centered(frame, 32, 9);
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
        .constraints([Constraint::Length(1), Constraint::Length(1), Constraint::Min(1)])
        .split(inner);

    f.render_widget(
        Paragraph::new(Line::from(Span::styled(
            " menu ",
            Style::default().fg(t.accent).add_modifier(Modifier::BOLD),
        )))
        .style(Style::default().bg(t.bg_darker)),
        chunks[0],
    );

    let items = ["Update", "Doctor", "Restore", "Help", "Quit"];
    let mut lines: Vec<Line> = Vec::new();
    for (i, label) in items.iter().enumerate() {
        let style = if i == cursor {
            Style::default()
                .fg(t.on_accent)
                .bg(t.accent)
                .add_modifier(Modifier::BOLD)
        } else {
            Style::default().fg(t.fg)
        };
        lines.push(Line::from(Span::styled(format!("  {}  ", label), style)));
    }
    f.render_widget(
        Paragraph::new(lines).style(Style::default().bg(t.bg_darker)),
        chunks[2],
    );
}

fn centered(area: Rect, w: u16, h: u16) -> Rect {
    let x = area.x + area.width.saturating_sub(w) / 2;
    let y = area.y + area.height.saturating_sub(h) / 2;
    Rect {
        x,
        y,
        width: w.min(area.width),
        height: h.min(area.height),
    }
}
