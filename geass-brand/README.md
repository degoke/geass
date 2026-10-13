# Geass Brand Kit

The Geass identity is **the command cursor**: a lowercase `geass` wordmark followed by a solid block, as if the terminal is waiting for your order. The name comes from the power of absolute command, and that is what the product does: you state the order once and Geass carries it out.

Tagline: **Absolute command over servers you already own.**

## Included assets

### Logos

- `logos/geass-horizontal-dark.svg`: wordmark for dark backgrounds (default for dark UI and the README on dark)
- `logos/geass-horizontal-light.svg`: wordmark for light backgrounds
- `logos/geass-horizontal-mono-white.svg`, `logos/geass-horizontal-mono-black.svg`: one-colour wordmarks
- `logos/geass-stacked-dark.svg`: wordmark with tagline
- `logos/geass-symbol.svg`, `logos/geass-symbol-light.svg`: the `g▮` symbol for dark and light backgrounds
- `logos/geass-symbol-mono-white.svg`, `logos/geass-symbol-mono-black.svg`: one-colour symbols

### Icons

- `icons/geass-app-icon.svg`: app and repository avatar source (`g▮` on black)
- `icons/geass-app-icon-512.png`: raster app icon
- `icons/favicon.svg`: green square with the cursor alone, for 16–32px
- `icons/favicon-32.png`: browser favicon

### Marketing

- `social/geass-social-card.svg` / `.png`: 1200×630 social card
- `preview/geass-brand-board.svg` / `.png`: visual identity overview

## Colour

Canonical tokens live in `tokens/geass-colors.json` and `tokens/geass-tokens.css`. The dashboard theme (`dashboard-ui/src/theme.js`, `dashboard-ui/src/styles.css`) and the website (`website/src/styles/landing.css` for the landing page, `website/src/styles/docs.css` for the docs) use the same values.

| Role | Hex | Use |
| --- | --- | --- |
| C.C. green | `#7CE0A0` | Accent on dark: actions, focus, healthy status |
| Deep green | `#16935A` | Accent on light backgrounds |
| Void | `#0A0A0A` | Canvas |
| Surface | `#151515` | Cards, inputs, code |
| Line | `#262626` | Separators |
| Text | `#F2F2F2` | Primary text on dark |
| Warning | `#E8A84A` | Degraded states |
| Critical | `#FF8A8A` | Failed states (text); solid buttons use `#C93C3C` |

Text placed on a solid green fill is always `#0A0A0A`. A solid green block is an action; a small green dot or text is status.

## Typography

- Interface and marketing: **Space Grotesk Variable**
- Technical content: **JetBrains Mono Variable**
- Wordmark: **Geist 700**, converted to outlines. Do not set live text in it.

## Usage

- Use the horizontal lockup as the default website and documentation logo.
- Use the `g▮` symbol for app icons, repository avatars, and compact navigation.
- Below 24px, use `icons/favicon.svg`: the cursor alone.
- Clear space equals the cursor's width on every side.
- The cursor may blink (1s steps) only while something is deploying.
- Do not rotate, stretch, outline, glow, bevel, recolour, or re-letter the logo, and do not place it over visually noisy imagery.

## Regenerating

Every logo, icon, social card and brand board is generated from font outlines by `tools/build_brand.py`, so the wordmark renders identically everywhere. The script also copies the dashboard's logo files into `dashboard-ui/public/`. See the headers of `tools/build_brand.py` and `tools/render_png.cjs` for the (throwaway) dependencies.
