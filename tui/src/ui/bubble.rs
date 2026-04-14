use ratatui::layout::Rect;
use ratatui::style::{Modifier, Style};
use ratatui::text::{Line, Span};
use ratatui::widgets::Paragraph;
use ratatui::Frame;

use super::theme;
use crate::app::App;

pub fn render(f: &mut Frame<'_>, area: Rect, app: &App) {
    let t = theme::current();
    let installed_n = app.installed.len();
    let dotfile_n = app.dotfiles.iter().filter(|d| d.installed).count();

    let lines = vec![
        Line::from(""),
        Line::from(vec![
            Span::raw("  "),
            Span::styled(
                "Bubble — ephemeral DPM session",
                Style::default().fg(t.accent).add_modifier(Modifier::BOLD),
            ),
        ]),
        Line::from(""),
        Line::from(vec![
            Span::raw("  "),
            Span::styled(
                "Spawn a temporary HOME with your DPM tools and dotfiles.",
                Style::default().fg(t.fg),
            ),
        ]),
        Line::from(vec![
            Span::raw("  "),
            Span::styled(
                "Everything disappears when you `exit` the shell.",
                Style::default().fg(t.fg_muted),
            ),
        ]),
        Line::from(""),
        Line::from(vec![
            Span::raw("  "),
            Span::styled("would mount: ", Style::default().fg(t.fg_dim)),
            Span::styled(
                format!("{} tools, {} dotfiles", installed_n, dotfile_n),
                Style::default().fg(t.fg),
            ),
        ]),
        Line::from(vec![
            Span::raw("  "),
            Span::styled("HOME: ", Style::default().fg(t.fg_dim)),
            Span::styled("/tmp/dpm-bubble-<id>", Style::default().fg(t.fg)),
        ]),
        Line::from(""),
        Line::from(vec![
            Span::raw("  "),
            Span::styled("Press ", Style::default().fg(t.fg_dim)),
            Span::styled(
                " ENTER ",
                Style::default()
                    .fg(t.on_accent)
                    .bg(t.accent)
                    .add_modifier(Modifier::BOLD),
            ),
            Span::styled(" to start a bubble.", Style::default().fg(t.fg_dim)),
        ]),
    ];
    let para = Paragraph::new(lines).style(Style::default().bg(t.bg));
    f.render_widget(para, area);
}
