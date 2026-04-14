use ratatui::layout::Rect;
use ratatui::style::{Modifier, Style};
use ratatui::text::{Line, Span};
use ratatui::widgets::Paragraph;
use ratatui::Frame;

use super::theme;
use crate::app::{App, CartKind};
use crate::rpc::types::Dotfile;

pub fn render(f: &mut Frame<'_>, area: Rect, app: &App) {
    let t = theme::current();
    let mut lines: Vec<Line> = Vec::new();

    let header = format!("{} dotfiles", app.dotfiles.len());
    lines.push(Line::from(Span::styled(
        format!("  {}", header),
        Style::default().fg(t.fg_dim),
    )));
    lines.push(Line::from(""));

    if app.dotfiles.is_empty() {
        lines.push(Line::from(Span::styled(
            "  No dotfiles available.",
            Style::default().fg(t.fg_dim),
        )));
    } else {
        for (i, d) in app.dotfiles.iter().enumerate() {
            let in_cart = app.cart_contains(CartKind::Dotfile, &d.id);
            lines.push(format_dotfile_line(d, i == app.dotfile_cursor, in_cart, t));
        }
    }

    let para = Paragraph::new(lines).style(Style::default().bg(t.bg));
    f.render_widget(para, area);
}

fn format_dotfile_line<'a>(d: &'a Dotfile, is_cursor: bool, in_cart: bool, t: &theme::Theme) -> Line<'a> {
    let row_style = if is_cursor {
        Style::default()
            .fg(t.on_accent)
            .bg(t.accent)
            .add_modifier(Modifier::BOLD)
    } else {
        Style::default().fg(t.fg)
    };

    let select_icon = if in_cart { "◆" } else { "◇" };
    let select_color = if in_cart { t.accent } else { t.fg_muted };

    let badge = if d.is_curated { "curated" } else { "custom" };
    let badge_color = if d.is_curated { t.success } else { t.fg_muted };

    let installed = if d.installed { " ★" } else { "" };
    let label = format!("{}{}", d.name, installed);

    Line::from(vec![
        Span::styled("  ", row_style),
        Span::styled(
            select_icon.to_string(),
            if is_cursor { row_style } else { Style::default().fg(select_color) },
        ),
        Span::styled("  ", row_style),
        Span::styled(pad_right(&label, 22), row_style.add_modifier(Modifier::BOLD)),
        Span::styled(" ", row_style),
        Span::styled(
            format!("[{}]", badge),
            if is_cursor { row_style } else { Style::default().fg(badge_color) },
        ),
        Span::styled("  ", row_style),
        Span::styled(
            d.description.clone(),
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
