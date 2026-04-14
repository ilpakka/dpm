use ratatui::layout::{Constraint, Direction, Layout, Rect};
use ratatui::style::{Modifier, Style};
use ratatui::text::{Line, Span};
use ratatui::widgets::{Block, BorderType, Borders, Paragraph};
use ratatui::Frame;

use super::theme;
use crate::app::{App, CartKind};
use crate::rpc::types::{Tool, ToolVersion};

pub fn render(f: &mut Frame<'_>, area: Rect, app: &App) {
    let t = theme::current();
    if app.tools.is_empty() {
        let lines = vec![
            Line::from(Span::styled(
                format!("  {} tools", app.tools.len()),
                Style::default().fg(t.fg_dim),
            )),
            Line::from(""),
            Line::from(Span::styled(
                "  Dpm is still empty - building around.",
                Style::default().fg(t.fg_dim),
            )),
        ];
        let p = Paragraph::new(lines).style(Style::default().bg(t.bg));
        f.render_widget(p, area);
        return;
    }

    match app.depth {
        0 => render_list(f, area, app, t),
        _ => render_detail(f, area, app, t),
    }
}

fn render_list(f: &mut Frame<'_>, area: Rect, app: &App, t: &theme::Theme) {
    let cursor = app.tool_cursor;
    let lines: Vec<Line> = app
        .tools
        .iter()
        .enumerate()
        .map(|(i, tool)| format_tool_line(tool, i == cursor, app.cart_contains(CartKind::Tool, &tool.id), t))
        .collect();
    let para = Paragraph::new(lines).style(Style::default().bg(t.bg));
    f.render_widget(para, area);
}

fn format_tool_line<'a>(tool: &'a Tool, is_cursor: bool, in_cart: bool, t: &theme::Theme) -> Line<'a> {
    let select_icon = if in_cart { "◆" } else { "◇" };
    let select_color = if in_cart { t.accent } else { t.fg_muted };

    let name = pad_right(&tool.name, 22);
    let method = format!("[{}]", tool.primary_method());
    let installed = if tool.is_installed() {
        format!("v{}", tool.installed_version().unwrap_or(""))
    } else {
        "—".to_string()
    };
    let installed_color = if tool.is_installed() { t.success } else { t.fg_dim };

    let row_style = if is_cursor {
        Style::default()
            .fg(t.on_accent)
            .bg(t.accent)
            .add_modifier(Modifier::BOLD)
    } else {
        Style::default().fg(t.fg)
    };

    Line::from(vec![
        Span::styled("  ", row_style),
        Span::styled(
            select_icon.to_string(),
            if is_cursor { row_style } else { Style::default().fg(select_color) },
        ),
        Span::styled("  ", row_style),
        Span::styled(name, row_style.add_modifier(Modifier::BOLD)),
        Span::styled(" ", row_style),
        Span::styled(
            method,
            if is_cursor { row_style } else { Style::default().fg(t.fg_muted) },
        ),
        Span::styled("  ", row_style),
        Span::styled(
            installed,
            if is_cursor { row_style } else { Style::default().fg(installed_color) },
        ),
        Span::styled("  ", row_style),
        Span::styled(
            tool.description.clone(),
            if is_cursor { row_style } else { Style::default().fg(t.fg_dim) },
        ),
    ])
}

fn render_detail(f: &mut Frame<'_>, area: Rect, app: &App, t: &theme::Theme) {
    let Some(tool) = app.tools.get(app.tool_cursor) else {
        return;
    };

    let chunks = Layout::default()
        .direction(Direction::Vertical)
        .constraints([Constraint::Length(5), Constraint::Min(1)])
        .split(area);

    let header = Paragraph::new(vec![
        Line::from(""),
        Line::from(vec![
            Span::raw("  "),
            Span::styled(
                tool.name.clone(),
                Style::default().fg(t.accent).add_modifier(Modifier::BOLD),
            ),
            Span::raw("  "),
            Span::styled(format!("[{}]", tool.category), Style::default().fg(t.fg)),
        ]),
        Line::from(vec![
            Span::raw("  "),
            Span::styled(tool.description.clone(), Style::default().fg(t.fg_dim)),
        ]),
        Line::from(vec![
            Span::raw("  "),
            Span::styled(
                format!("source: {}", tool.primary_method()),
                Style::default().fg(t.fg_dim),
            ),
        ]),
    ])
    .style(Style::default().bg(t.bg));
    f.render_widget(header, chunks[0]);

    if app.depth == 1 {
        let split = Layout::default()
            .direction(Direction::Horizontal)
            .constraints([Constraint::Percentage(45), Constraint::Percentage(55)])
            .split(chunks[1]);

        let left_focused = !app.version_focus;
        let right_focused = app.version_focus;

        let left_block = Block::default()
            .borders(Borders::ALL)
            .border_type(BorderType::Rounded)
            .border_style(Style::default().fg(if left_focused { t.accent } else { t.fg_dim }))
            .title(Span::styled(
                " versions ",
                Style::default().fg(if left_focused { t.accent } else { t.fg_dim }),
            ));
        let right_block = Block::default()
            .borders(Borders::ALL)
            .border_type(BorderType::Rounded)
            .border_style(Style::default().fg(if right_focused { t.accent } else { t.fg_dim }))
            .title(Span::styled(
                " details ",
                Style::default().fg(if right_focused { t.accent } else { t.fg_dim }),
            ));

        let cursor = app.version_cursor;
        let lines: Vec<Line> = tool
            .versions
            .iter()
            .enumerate()
            .map(|(i, v)| format_version_line(v, i == cursor, t))
            .collect();

        let inner_left = left_block.inner(split[0]);
        let inner_right = right_block.inner(split[1]);
        f.render_widget(left_block, split[0]);
        f.render_widget(right_block, split[1]);

        let para = Paragraph::new(lines).style(Style::default().bg(t.bg));
        f.render_widget(para, inner_left);

        if let Some(v) = tool.versions.get(cursor) {
            let methods = v
                .install_methods
                .iter()
                .map(|m| m.method_type.clone())
                .collect::<Vec<_>>()
                .join(", ");
            let details = Paragraph::new(vec![
                Line::from(""),
                Line::from(vec![
                    Span::styled("  version: ", Style::default().fg(t.fg_dim)),
                    Span::styled(
                        format!("v{}", v.version),
                        Style::default().fg(t.accent).add_modifier(Modifier::BOLD),
                    ),
                ]),
                Line::from(vec![
                    Span::styled("  methods: ", Style::default().fg(t.fg_dim)),
                    Span::styled(methods, Style::default().fg(t.fg)),
                ]),
                Line::from(vec![
                    Span::styled("  released: ", Style::default().fg(t.fg_dim)),
                    Span::styled(v.release_date.clone(), Style::default().fg(t.fg)),
                ]),
                Line::from(vec![
                    Span::styled("  status: ", Style::default().fg(t.fg_dim)),
                    Span::styled(
                        if v.installed { "installed" } else { "not installed" },
                        Style::default().fg(if v.installed { t.success } else { t.fg_dim }),
                    ),
                ]),
            ])
            .style(Style::default().bg(t.bg));
            f.render_widget(details, inner_right);
        }
        return;
    }

    // Depth 2: just the version list, full-width.
    let cursor = app.version_cursor;
    let lines: Vec<Line> = tool
        .versions
        .iter()
        .enumerate()
        .map(|(i, v)| format_version_line(v, i == cursor, t))
        .collect();
    let para = Paragraph::new(lines).style(Style::default().bg(t.bg));
    f.render_widget(para, chunks[1]);
}

fn format_version_line<'a>(v: &'a ToolVersion, is_cursor: bool, t: &theme::Theme) -> Line<'a> {
    let installed_marker = if v.installed { " ★" } else { "" };
    let label = format!(" v{}{}", v.version, installed_marker);
    let action = if v.installed { "reinstall" } else { "install" };
    let methods = v
        .install_methods
        .iter()
        .map(|m| m.method_type.clone())
        .collect::<Vec<_>>()
        .join(",");

    let row_style = if is_cursor {
        Style::default()
            .fg(t.on_accent)
            .bg(t.accent)
            .add_modifier(Modifier::BOLD)
    } else {
        Style::default().fg(t.fg)
    };

    Line::from(vec![
        Span::styled("  ", row_style),
        Span::styled(label, row_style.add_modifier(Modifier::BOLD)),
        Span::styled("  ", row_style),
        Span::styled(
            format!("[{}]", methods),
            if is_cursor { row_style } else { Style::default().fg(t.fg_muted) },
        ),
        Span::styled("  ", row_style),
        Span::styled(
            action.to_string(),
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
