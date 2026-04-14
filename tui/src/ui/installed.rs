use ratatui::layout::Rect;
use ratatui::style::{Modifier, Style};
use ratatui::text::{Line, Span};
use ratatui::widgets::Paragraph;
use ratatui::Frame;

use super::theme;
use crate::app::App;
use crate::rpc::types::InstalledTool;

pub fn render(f: &mut Frame<'_>, area: Rect, app: &App) {
    let t = theme::current();
    let mut lines: Vec<Line> = Vec::new();

    let batch_mode = !app.installed_selected.is_empty();
    let header = if batch_mode {
        format!(
            "{} installed — {} selected",
            app.installed.len(),
            app.installed_selected.len()
        )
    } else {
        format!("{} installed", app.installed.len())
    };
    lines.push(Line::from(Span::styled(
        format!("  {}", header),
        Style::default().fg(t.fg_dim),
    )));
    lines.push(Line::from(""));

    if app.installed.is_empty() {
        lines.push(Line::from(Span::styled(
            "  Nothing installed yet.",
            Style::default().fg(t.fg_dim),
        )));
        lines.push(Line::from(Span::styled(
            "  Use Tools tab to install tools.",
            Style::default().fg(t.fg_dim),
        )));
    } else {
        for (i, tool) in app.installed.iter().enumerate() {
            let key = App::installed_key(&tool.tool_id, &tool.version);
            let selected = app.installed_selected.contains(&key);
            lines.push(format_installed_line(
                tool,
                i == app.installed_cursor,
                batch_mode,
                selected,
                t,
            ));
        }
    }

    let para = Paragraph::new(lines).style(Style::default().bg(t.bg));
    f.render_widget(para, area);
}

fn format_installed_line<'a>(
    tool: &'a InstalledTool,
    is_cursor: bool,
    batch_mode: bool,
    selected: bool,
    t: &theme::Theme,
) -> Line<'a> {
    let row_style = if is_cursor {
        Style::default()
            .fg(t.on_accent)
            .bg(t.accent)
            .add_modifier(Modifier::BOLD)
    } else {
        Style::default().fg(t.fg)
    };

    let label = if !tool.tool_name.is_empty() {
        tool.tool_name.clone()
    } else {
        tool.tool_id.clone()
    };
    let verify_badge = if tool.verified { " [SHA256 OK] " } else { "" };
    let method = if tool.method.is_empty() {
        String::new()
    } else {
        format!(" via {}", tool.method)
    };

    let checkbox = if batch_mode {
        if selected { "[x] " } else { "[ ] " }
    } else {
        ""
    };

    Line::from(vec![
        Span::styled("  ", row_style),
        Span::styled(checkbox, row_style),
        Span::styled("◇", row_style),
        Span::styled("  ", row_style),
        Span::styled(
            pad_right(&label, 22),
            row_style.add_modifier(Modifier::BOLD),
        ),
        Span::styled(" ", row_style),
        Span::styled(
            format!("v{}", tool.version),
            if is_cursor { row_style } else { Style::default().fg(t.fg_muted) },
        ),
        Span::styled(
            verify_badge.to_string(),
            if is_cursor {
                row_style
            } else {
                Style::default().fg(t.success).add_modifier(Modifier::BOLD)
            },
        ),
        Span::styled(
            method,
            if is_cursor { row_style } else { Style::default().fg(t.fg_dim) },
        ),
    ])
}

fn pad_right(s: &str, w: usize) -> String {
    if s.chars().count() >= w {
        s.to_string()
    } else {
        let mut out = s.to_string();
        out.push_str(&" ".repeat(w - s.chars().count()));
        out
    }
}
