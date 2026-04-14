//! Multi-theme colour system.
//!
//! Every UI module calls `theme::current()` once at the top of its render
//! function and uses the returned `&Theme` for all styling decisions.
//!
//! Switching theme at runtime: call `theme::set(index)`.  The index is into
//! [`THEMES`].

use std::sync::atomic::{AtomicUsize, Ordering};

use ratatui::style::Color;

// ── Theme struct ────────────────────────────────────────────────────

/// Complete colour palette for the TUI.
#[derive(Debug, Clone, Copy)]
pub struct Theme {
    pub name: &'static str,

    // Backgrounds
    pub bg: Color,          // main body
    pub bg_darker: Color,   // bars, popups, overlays
    pub bg_selected: Color, // hover / selection row

    // Brand accent — active tab, focused border, badges
    pub accent: Color,
    /// Text rendered *on top of* the accent colour (buttons, chips).
    pub on_accent: Color,

    // Foreground text hierarchy
    pub fg: Color,       // primary
    pub fg_muted: Color, // secondary (descriptions, hints)
    pub fg_dim: Color,   // tertiary (meta, timestamps)

    // Semantic status
    pub success: Color,
    pub success_bg: Color,
    pub error: Color,
    pub info: Color,
    pub warning: Color,

    // Rainbow for progress animation
    pub rainbow: &'static [Color],
}

// ── Predefined themes ───────────────────────────────────────────────

static RAINBOW_DEFAULT: &[Color] = &[
    Color::Rgb(0xff, 0x00, 0x00),
    Color::Rgb(0xff, 0x7f, 0x00),
    Color::Rgb(0xff, 0xff, 0x00),
    Color::Rgb(0x00, 0xff, 0x00),
    Color::Rgb(0x00, 0x00, 0xff),
    Color::Rgb(0x4b, 0x00, 0x82),
    Color::Rgb(0x94, 0x00, 0xd3),
];

static RAINBOW_NORD: &[Color] = &[
    Color::Rgb(0x8f, 0xbc, 0xbb),
    Color::Rgb(0x88, 0xc0, 0xd0),
    Color::Rgb(0x81, 0xa1, 0xc1),
    Color::Rgb(0x5e, 0x81, 0xac),
    Color::Rgb(0xa3, 0xbe, 0x8c),
    Color::Rgb(0xeb, 0xcb, 0x8b),
    Color::Rgb(0xd0, 0x87, 0x70),
];

static RAINBOW_DRACULA: &[Color] = &[
    Color::Rgb(0xff, 0x55, 0x55),
    Color::Rgb(0xff, 0xb8, 0x6c),
    Color::Rgb(0xf1, 0xfa, 0x8c),
    Color::Rgb(0x50, 0xfa, 0x7b),
    Color::Rgb(0x8b, 0xe9, 0xfd),
    Color::Rgb(0xbd, 0x93, 0xf9),
    Color::Rgb(0xff, 0x79, 0xc6),
];

static RAINBOW_CATPPUCCIN: &[Color] = &[
    Color::Rgb(0xf3, 0x8b, 0xa8),
    Color::Rgb(0xfa, 0xb3, 0x87),
    Color::Rgb(0xf9, 0xe2, 0xaf),
    Color::Rgb(0xa6, 0xe3, 0xa1),
    Color::Rgb(0x89, 0xb4, 0xfa),
    Color::Rgb(0xcb, 0xa6, 0xf7),
    Color::Rgb(0xf5, 0xc2, 0xe7),
];

/// Default — dark background, yellow accent (original DPM look).
pub const THEME_DEFAULT: Theme = Theme {
    name: "Default",
    bg: Color::Rgb(0x2a, 0x2a, 0x2e),
    bg_darker: Color::Rgb(0x1e, 0x1e, 0x22),
    bg_selected: Color::Rgb(0x33, 0x33, 0x3a),
    accent: Color::Rgb(0xff, 0xe6, 0x00),
    on_accent: Color::Rgb(0x00, 0x00, 0x00),
    fg: Color::Rgb(0xe8, 0xe8, 0xe8),
    fg_muted: Color::Rgb(0x77, 0x77, 0x77),
    fg_dim: Color::Rgb(0x55, 0x55, 0x55),
    success: Color::Rgb(0x7e, 0xc4, 0x7e),
    success_bg: Color::Rgb(0x1a, 0x2e, 0x1a),
    error: Color::Rgb(0xe0, 0x5f, 0x5f),
    info: Color::Rgb(0x67, 0xc1, 0xe8),
    warning: Color::Rgb(0xf0, 0xb0, 0x40),
    rainbow: RAINBOW_DEFAULT,
};

/// Nord — cool blue palette.
pub const THEME_NORD: Theme = Theme {
    name: "Nord",
    bg: Color::Rgb(0x2e, 0x34, 0x40),
    bg_darker: Color::Rgb(0x24, 0x29, 0x33),
    bg_selected: Color::Rgb(0x3b, 0x42, 0x52),
    accent: Color::Rgb(0x88, 0xc0, 0xd0),
    on_accent: Color::Rgb(0x2e, 0x34, 0x40),
    fg: Color::Rgb(0xec, 0xef, 0xf4),
    fg_muted: Color::Rgb(0x7b, 0x88, 0x9e),
    fg_dim: Color::Rgb(0x5a, 0x65, 0x7a),
    success: Color::Rgb(0xa3, 0xbe, 0x8c),
    success_bg: Color::Rgb(0x2a, 0x35, 0x28),
    error: Color::Rgb(0xbf, 0x61, 0x6a),
    info: Color::Rgb(0x5e, 0x81, 0xac),
    warning: Color::Rgb(0xeb, 0xcb, 0x8b),
    rainbow: RAINBOW_NORD,
};

/// Dracula — purple/green dark palette.
pub const THEME_DRACULA: Theme = Theme {
    name: "Dracula",
    bg: Color::Rgb(0x28, 0x2a, 0x36),
    bg_darker: Color::Rgb(0x1e, 0x1f, 0x29),
    bg_selected: Color::Rgb(0x44, 0x47, 0x5a),
    accent: Color::Rgb(0xbd, 0x93, 0xf9),
    on_accent: Color::Rgb(0x28, 0x2a, 0x36),
    fg: Color::Rgb(0xf8, 0xf8, 0xf2),
    fg_muted: Color::Rgb(0x8e, 0x92, 0xa8),
    fg_dim: Color::Rgb(0x62, 0x72, 0xa4),
    success: Color::Rgb(0x50, 0xfa, 0x7b),
    success_bg: Color::Rgb(0x1a, 0x3a, 0x2a),
    error: Color::Rgb(0xff, 0x55, 0x55),
    info: Color::Rgb(0x8b, 0xe9, 0xfd),
    warning: Color::Rgb(0xf1, 0xfa, 0x8c),
    rainbow: RAINBOW_DRACULA,
};

/// Catppuccin Mocha — warm pastel dark palette.
pub const THEME_CATPPUCCIN: Theme = Theme {
    name: "Catppuccin",
    bg: Color::Rgb(0x1e, 0x1e, 0x2e),
    bg_darker: Color::Rgb(0x18, 0x18, 0x25),
    bg_selected: Color::Rgb(0x31, 0x32, 0x44),
    accent: Color::Rgb(0xcb, 0xa6, 0xf7),
    on_accent: Color::Rgb(0x1e, 0x1e, 0x2e),
    fg: Color::Rgb(0xcd, 0xd6, 0xf4),
    fg_muted: Color::Rgb(0x7f, 0x84, 0x9c),
    fg_dim: Color::Rgb(0x58, 0x5b, 0x70),
    success: Color::Rgb(0xa6, 0xe3, 0xa1),
    success_bg: Color::Rgb(0x1e, 0x32, 0x2a),
    error: Color::Rgb(0xf3, 0x8b, 0xa8),
    info: Color::Rgb(0x89, 0xb4, 0xfa),
    warning: Color::Rgb(0xf9, 0xe2, 0xaf),
    rainbow: RAINBOW_CATPPUCCIN,
};

/// All available themes, in selection order.
pub const THEMES: &[Theme] = &[
    THEME_DEFAULT,
    THEME_NORD,
    THEME_DRACULA,
    THEME_CATPPUCCIN,
];

// ── Runtime selection ───────────────────────────────────────────────

static CURRENT: AtomicUsize = AtomicUsize::new(0);

/// Return the active theme.
#[inline]
pub fn current() -> &'static Theme {
    &THEMES[CURRENT.load(Ordering::Relaxed) % THEMES.len()]
}

/// Set the active theme by index into [`THEMES`].
pub fn set(idx: usize) {
    CURRENT.store(idx % THEMES.len(), Ordering::Relaxed);
}

/// Return the active theme index.
pub fn index() -> usize {
    CURRENT.load(Ordering::Relaxed) % THEMES.len()
}

/// Cycle to the next theme and return its name.
pub fn next() -> &'static str {
    let i = (index() + 1) % THEMES.len();
    set(i);
    THEMES[i].name
}

// ── Layout constants (theme-independent) ────────────────────────────

pub const MAX_W: u16 = 120;
pub const MAX_H: u16 = 35;
pub const MIN_W: u16 = 62;
pub const MIN_H: u16 = 15;

pub const SPINNER_FRAMES: &[&str] = &["⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"];
