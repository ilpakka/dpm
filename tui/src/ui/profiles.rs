use ratatui::layout::{Constraint, Direction, Layout, Rect};
use ratatui::style::{Modifier, Style};
use ratatui::text::{Line, Span};
use ratatui::widgets::Paragraph;
use ratatui::Frame;

use super::theme;
use crate::app::{App, CartKind};
use crate::rpc::types::Profile;

pub fn render(f: &mut Frame<'_>, area: Rect, app: &App) {
    let t = theme::current();
    if app.profiles.is_empty() {
        let lines = vec![
            Line::from(Span::styled(
                format!("  {} profiles", app.profiles.len()),
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
    let cursor = app.profile_cursor;
    let lines: Vec<Line> = app
        .profiles
        .iter()
        .enumerate()
        .map(|(i, p)| format_profile_line(app, p, i == cursor, t))
        .collect();
    let para = Paragraph::new(lines).style(Style::default().bg(t.bg));
    f.render_widget(para, area);
}

fn format_profile_line<'a>(app: &App, p: &'a Profile, is_cursor: bool, t: &theme::Theme) -> Line<'a> {
    let tool_ids = p.all_tool_ids();
    let all_in_cart = !tool_ids.is_empty()
        && tool_ids.iter().all(|id| app.cart_contains(CartKind::Tool, id));
    let select_icon = if all_in_cart { "◆" } else { "◇" };
    let select_color = if all_in_cart { t.accent } else { t.fg_muted };

    let row_style = if is_cursor {
        Style::default()
            .fg(t.on_accent)
            .bg(t.accent)
            .add_modifier(Modifier::BOLD)
    } else {
        Style::default().fg(t.fg)
    };

    let count = format!("{} tools", tool_ids.len());
    let cat = format!("[{}]", p.category);

    let all_installed = !tool_ids.is_empty()
        && tool_ids.iter().all(|id| app.installed.iter().any(|i| i.tool_id == *id));

    let mut spans = vec![
        Span::styled("  ", row_style),
        Span::styled(
            select_icon.to_string(),
            if is_cursor { row_style } else { Style::default().fg(select_color) },
        ),
        Span::styled("  ", row_style),
        Span::styled(pad_right(&p.name, 22), row_style.add_modifier(Modifier::BOLD)),
        Span::styled(" ", row_style),
        Span::styled(
            cat,
            if is_cursor { row_style } else { Style::default().fg(t.fg) },
        ),
        Span::styled("  ", row_style),
        Span::styled(
            count,
            if is_cursor { row_style } else { Style::default().fg(t.fg_dim) },
        ),
    ];
    if all_installed {
        spans.push(Span::styled("  ", row_style));
        spans.push(Span::styled(
            " installed ",
            if is_cursor {
                row_style
            } else {
                Style::default()
                    .fg(t.success)
                    .bg(t.success_bg)
                    .add_modifier(Modifier::BOLD)
            },
        ));
    }
    Line::from(spans)
}

fn render_detail(f: &mut Frame<'_>, area: Rect, app: &App, t: &theme::Theme) {
    let Some(prof) = app.profiles.get(app.profile_cursor) else {
        return;
    };

    let chunks = Layout::default()
        .direction(Direction::Vertical)
        .constraints([Constraint::Length(6), Constraint::Min(1)])
        .split(area);

    let header = Paragraph::new(vec![
        Line::from(""),
        Line::from(vec![
            Span::raw("  "),
            Span::styled(
                prof.name.clone(),
                Style::default().fg(t.accent).add_modifier(Modifier::BOLD),
            ),
            Span::raw("  "),
            Span::styled(format!("[{}]", prof.category), Style::default().fg(t.fg)),
        ]),
        Line::from(vec![
            Span::raw("  "),
            Span::styled(prof.description.clone(), Style::default().fg(t.fg_dim)),
        ]),
        Line::from(vec![
            Span::raw("  "),
            Span::styled(
                format!("course: {}", prof.course_code),
                Style::default().fg(t.fg_dim),
            ),
        ]),
        Line::from(vec![
            Span::raw("  "),
            Span::styled(
                format!("version: v{}", prof.version),
                Style::default().fg(t.fg_dim),
            ),
        ]),
    ])
    .style(Style::default().bg(t.bg));
    f.render_widget(header, chunks[0]);

    let mut lines: Vec<Line> = Vec::new();
    let ids = prof.all_tool_ids();
    lines.push(Line::from(Span::styled(
        format!("  Tools ({}):", ids.len()),
        Style::default().fg(t.fg_dim),
    )));
    for id in ids {
        lines.push(Line::from(vec![
            Span::raw("    "),
            Span::styled("• ", Style::default().fg(t.accent)),
            Span::styled(id, Style::default().fg(t.fg)),
        ]));
    }
    if !prof.dotfiles.is_empty() {
        lines.push(Line::from(""));
        lines.push(Line::from(Span::styled(
            format!("  Dotfiles ({}):", prof.dotfiles.len()),
            Style::default().fg(t.fg_dim),
        )));
        for d in &prof.dotfiles {
            lines.push(Line::from(vec![
                Span::raw("    "),
                Span::styled("• ", Style::default().fg(t.accent)),
                Span::styled(d.clone(), Style::default().fg(t.fg)),
            ]));
        }
    }
    lines.push(Line::from(""));
    lines.push(Line::from(Span::styled(
        "  ENTER apply profile · ESC back",
        Style::default().fg(t.fg_dim),
    )));
    let para = Paragraph::new(lines).style(Style::default().bg(t.bg));
    f.render_widget(para, chunks[1]);
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
