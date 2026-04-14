use ratatui::layout::Rect;
use ratatui::style::{Modifier, Style};
use ratatui::text::{Line, Span};
use ratatui::widgets::Paragraph;
use ratatui::Frame;

use super::theme;
use crate::app::{App, ALL_TABS};

/// Tab strip rendered as a single line. Active tab uses accent colour.
pub fn render(f: &mut Frame<'_>, area: Rect, app: &App) {
    let t = theme::current();
    let active = app.tab_index();

    // Line 1: tab labels
    let mut label_spans: Vec<Span> = Vec::new();
    label_spans.push(Span::raw(" "));
    for (i, tab) in ALL_TABS.iter().enumerate() {
        let label = format!(" {} ", tab.label());
        let style = if i == active {
            Style::default()
                .fg(t.accent)
                .add_modifier(Modifier::BOLD)
        } else {
            Style::default().fg(t.fg_muted)
        };
        label_spans.push(Span::styled(label, style));
        if i < ALL_TABS.len() - 1 {
            label_spans.push(Span::raw("  "));
        }
    }

    // Line 2: underline bar
    let mut ul_spans: Vec<Span> = Vec::new();
    ul_spans.push(Span::raw(" "));
    for (i, tab) in ALL_TABS.iter().enumerate() {
        let w = tab.label().len() + 2; // matches " label " padding
        let bar = if i == active {
            Span::styled("─".repeat(w), Style::default().fg(t.accent))
        } else {
            Span::styled(" ".repeat(w), Style::default().fg(t.fg_dim))
        };
        ul_spans.push(bar);
        if i < ALL_TABS.len() - 1 {
            ul_spans.push(Span::raw("  "));
        }
    }

    let lines = vec![Line::from(label_spans), Line::from(ul_spans)];
    let para = Paragraph::new(lines).style(Style::default().bg(t.bg));
    f.render_widget(para, area);
}
