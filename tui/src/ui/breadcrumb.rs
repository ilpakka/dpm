use ratatui::layout::Rect;
use ratatui::style::Style;
use ratatui::text::{Line, Span};
use ratatui::widgets::Paragraph;
use ratatui::Frame;

use super::theme;
use crate::app::App;

/// `tabname / itemname / version` style breadcrumb.
pub fn render(f: &mut Frame<'_>, area: Rect, app: &App) {
    let t = theme::current();
    let mut spans: Vec<Span> = Vec::new();
    spans.push(Span::raw(" "));
    spans.push(Span::styled(
        app.tab().label(),
        Style::default().fg(t.accent),
    ));

    if app.depth >= 1 {
        if let Some(name) = app.current_item_name() {
            spans.push(Span::styled(" / ", Style::default().fg(t.fg_muted)));
            spans.push(Span::styled(name, Style::default().fg(t.accent)));
        }
    }
    if app.depth >= 2 {
        if let Some(ver) = app.current_version_name() {
            spans.push(Span::styled(" / ", Style::default().fg(t.fg_muted)));
            spans.push(Span::styled(ver, Style::default().fg(t.accent)));
        }
    }

    let para = Paragraph::new(Line::from(spans)).style(Style::default().bg(t.bg));
    f.render_widget(para, area);
}
