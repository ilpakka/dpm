//! Static help screen — keybind reference grouped by section.

use ratatui::layout::{Margin, Rect};
use ratatui::style::{Modifier, Style};
use ratatui::text::{Line, Span};
use ratatui::widgets::{Block, BorderType, Borders, Clear, Paragraph};
use ratatui::Frame;

use super::theme;

pub fn render(f: &mut Frame<'_>, area: Rect) {
    let t = theme::current();
    f.render_widget(Clear, area);
    let block = Block::default()
        .borders(Borders::ALL)
        .border_type(BorderType::Rounded)
        .border_style(Style::default().fg(t.accent))
        .title(Span::styled(
            " help ",
            Style::default().fg(t.accent).add_modifier(Modifier::BOLD),
        ));
    let inner = block.inner(area);
    f.render_widget(block, area);

    let body = inner.inner(Margin {
        horizontal: 2,
        vertical: 1,
    });

    let sections: &[(&str, &[(&str, &str)])] = &[
        (
            "Global",
            &[
                ("q / Ctrl-C", "quit"),
                ("?  /  F1", "this help"),
                ("Ctrl-K", "open menu (Update / Doctor / Restore / Help / Quit)"),
                ("Ctrl-U", "check + show updates"),
                (",", "settings"),
                ("/", "search"),
                ("t", "cycle theme"),
            ],
        ),
        (
            "Navigation",
            &[
                ("←/→  h/l", "switch tab"),
                ("↑/↓  j/k", "move cursor"),
                ("Enter", "open / confirm"),
                ("Esc", "back / close"),
                ("Tab (Tools)", "focus version list"),
            ],
        ),
        (
            "Cart",
            &[
                ("Space", "toggle current in cart"),
                ("Ctrl-A", "install everything in the cart"),
            ],
        ),
        (
            "Installed",
            &[
                ("Space", "toggle row in batch select"),
                ("d", "remove (single or batch)"),
                ("o", "open / launch the binary"),
            ],
        ),
        (
            "Dotfiles",
            &[
                ("Enter", "install curated dotfile"),
                ("a", "add custom git repo"),
            ],
        ),
        (
            "Settings",
            &[
                ("Enter", "toggle bool / edit text"),
                ("r", "reset to default"),
                ("t", "cycle theme"),
                ("Esc (in edit)", "cancel edit"),
            ],
        ),
        (
            "Bubble",
            &[
                ("Enter", "spawn ephemeral DPM session"),
                ("exit (in shell)", "leave bubble"),
            ],
        ),
    ];

    let mut lines: Vec<Line> = Vec::new();
    for (title, rows) in sections {
        lines.push(Line::from(Span::styled(
            (*title).to_string(),
            Style::default().fg(t.accent).add_modifier(Modifier::BOLD),
        )));
        for (k, d) in *rows {
            lines.push(Line::from(vec![
                Span::raw("  "),
                Span::styled(
                    pad(k, 16),
                    Style::default().fg(t.fg).add_modifier(Modifier::BOLD),
                ),
                Span::styled((*d).to_string(), Style::default().fg(t.fg_dim)),
            ]));
        }
        lines.push(Line::from(""));
    }
    lines.push(Line::from(Span::styled(
        "ESC close",
        Style::default().fg(t.fg_dim),
    )));
    f.render_widget(Paragraph::new(lines), body);
}

fn pad(s: &str, w: usize) -> String {
    if s.chars().count() >= w {
        s.to_string()
    } else {
        let mut out = s.to_string();
        out.push_str(&" ".repeat(w - s.chars().count()));
        out
    }
}
