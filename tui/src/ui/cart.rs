use ratatui::layout::Rect;
use ratatui::style::{Modifier, Style};
use ratatui::text::{Line, Span};
use ratatui::widgets::{Block, BorderType, Borders, Paragraph};
use ratatui::Frame;

use super::theme;
use crate::app::App;

/// Cart bar — shown above the body when at least one item is selected.
pub fn render(f: &mut Frame<'_>, area: Rect, app: &App) {
    if app.cart.is_empty() {
        return;
    }
    let t = theme::current();

    let names = collect_names(app);
    let info = format!("{} selected: {}", app.cart.len(), names);
    let button = " Install all ⏎ ";

    let inner_w = area.width.saturating_sub(4) as usize;
    let info_w = info.chars().count();
    let button_w = button.chars().count();
    let gap = inner_w.saturating_sub(info_w + button_w).max(1);

    let line = Line::from(vec![
        Span::styled(info, Style::default().fg(t.fg)),
        Span::raw(" ".repeat(gap)),
        Span::styled(
            button.to_string(),
            Style::default()
                .fg(t.on_accent)
                .bg(t.accent)
                .add_modifier(Modifier::BOLD),
        ),
    ]);

    let block = Block::default()
        .borders(Borders::ALL)
        .border_type(BorderType::Rounded)
        .border_style(Style::default().fg(t.accent))
        .style(Style::default().bg(t.bg));
    let para = Paragraph::new(line)
        .block(block)
        .style(Style::default().bg(t.bg));
    f.render_widget(para, area);
}

fn collect_names(app: &App) -> String {
    let mut names: Vec<&str> = app.cart.values().map(|c| c.name.as_str()).collect();
    names.sort();
    names.dedup();
    if names.len() <= 4 {
        return names.join(", ");
    }
    let head = names[..4].join(", ");
    format!("{}, +{} more", head, names.len() - 4)
}
