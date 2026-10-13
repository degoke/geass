#!/usr/bin/env python3
"""Regenerate the Geass brand assets from font outlines.

The identity is a lowercase `geass` wordmark (Geist 700, tracking -5.5%) followed by a
solid command-cursor block that is exactly one x-height tall. All text is converted to
paths so the SVGs render identically everywhere (an <img> SVG cannot load web fonts).

Dependencies (install in a throwaway venv, they are not project dependencies):
    pip install fonttools brotli uharfbuzz
Fonts come from dashboard-ui/node_modules (run `pnpm install` there first).

Usage:
    python3 geass-brand/tools/build_brand.py        # writes SVGs
    node geass-brand/tools/render_png.cjs           # rasterises PNGs (needs @resvg/resvg-js, see its header)
"""
import io
import pathlib
import shutil

import uharfbuzz as hb
from fontTools.pens.boundsPen import BoundsPen
from fontTools.pens.svgPathPen import SVGPathPen
from fontTools.pens.transformPen import TransformPen
from fontTools.ttLib import TTFont
from fontTools.varLib import instancer

ROOT = pathlib.Path(__file__).resolve().parents[2]
FONTS = ROOT / "dashboard-ui/node_modules/@fontsource-variable"
BRAND = ROOT / "geass-brand"
PUBLIC = ROOT / "dashboard-ui/public"

GREEN = "#7CE0A0"  # C.C. green: accent on dark
DEEP = "#16935A"   # deep green: accent on light
VOID = "#0A0A0A"
SURFACE = "#151515"
LINE = "#262626"
TEXT = "#F2F2F2"
MUTED = "#8F8F8F"
CRITICAL = "#FF8A8A"

TRACKING = -0.055   # em
CURSOR_W = 0.30     # em
CURSOR_GAP = 0.075  # em, ink-to-ink between the last "s" and the cursor


def fmt(v):
    s = f"{v:.2f}".rstrip("0").rstrip(".")
    return "0" if s in ("-0", "") else s


class Face:
    def __init__(self, family, wght):
        font = TTFont(FONTS / family / "files" / f"{family}-latin-wght-normal.woff2")
        font = instancer.instantiateVariableFont(font, {"wght": wght})
        self.upem = font["head"].unitsPerEm
        self.xheight = font["OS/2"].sxHeight
        self.ascent = font["hhea"].ascent
        self.descent = -font["hhea"].descent
        self.gap = font["hhea"].lineGap
        self.order = font.getGlyphOrder()
        self.glyphs = font.getGlyphSet()
        font.flavor = None  # HarfBuzz reads plain sfnt, not the WOFF2 wrapper
        buf = io.BytesIO()
        font.save(buf)
        self.hb = hb.Font(hb.Face(buf.getvalue()))

    def run(self, text, size, tracking=0.0):
        """Shape text; returns [(glyph, x, y)] in px and the pen advance."""
        buf = hb.Buffer()
        buf.add_str(text)
        buf.guess_segment_properties()
        hb.shape(self.hb, buf, {"kern": True})
        k = size / self.upem
        x, out = 0.0, []
        for info, pos in zip(buf.glyph_infos, buf.glyph_positions):
            out.append((self.order[info.codepoint], x + pos.x_offset * k, -pos.y_offset * k))
            x += pos.x_advance * k + tracking * size
        return out, x

    def draw(self, text, size, x=0.0, y=0.0, tracking=0.0, anchor="start"):
        """Return (svg path d, ink bounds (x0, y0, x1, y1), advance) with the baseline at y."""
        glyphs, adv = self.run(text, size, tracking)
        adv_ink = adv - tracking * size
        if anchor == "middle":
            x -= adv_ink / 2
        elif anchor == "end":
            x -= adv_ink
        k = size / self.upem
        pen = SVGPathPen(self.glyphs, ntos=fmt)
        bp = BoundsPen(None)
        for name, gx, gy in glyphs:
            t = (k, 0, 0, -k, x + gx, y + gy)
            self.glyphs[name].draw(TransformPen(pen, t))
            bounds = BoundsPen(self.glyphs)
            self.glyphs[name].draw(TransformPen(bounds, t))
            if bounds.bounds:
                bp.bounds = _union(bp.bounds, bounds.bounds)
        return pen.getCommands(), bp.bounds, adv_ink


def _union(a, b):
    if a is None:
        return b
    return (min(a[0], b[0]), min(a[1], b[1]), max(a[2], b[2]), max(a[3], b[3]))


GEIST = Face("geist", 700)
GROTESK = {w: Face("space-grotesk", w) for w in (500, 600, 700)}
MONO = {w: Face("jetbrains-mono", w) for w in (400, 500)}


def wordmark(size=100.0, x=0.0, y=0.0):
    """`geass` + cursor, baseline at y. Returns letters d, cursor rect, ink bbox."""
    d, bounds, _ = GEIST.draw("geass", size, x, y, TRACKING)
    cx = bounds[2] + CURSOR_GAP * size
    ch = GEIST.xheight * size / GEIST.upem
    cursor = (cx, y - ch, CURSOR_W * size, ch)
    bbox = (bounds[0], min(bounds[1], cursor[1]), cx + cursor[2], bounds[3])
    return d, cursor, bbox


def rect(c, fill):
    return f'<rect x="{fmt(c[0])}" y="{fmt(c[1])}" width="{fmt(c[2])}" height="{fmt(c[3])}" fill="{fill}"/>'


def svg(viewbox, body, title, desc=None, extra=""):
    d = f"<desc>{desc}</desc>" if desc else ""
    return (
        f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="{viewbox}" role="img" aria-label="{title}"{extra}>\n'
        f"  <title>{title}</title>{d}\n{body}\n</svg>\n"
    )


def tight(bbox, pad):
    x0, y0, x1, y1 = bbox
    return f"{fmt(x0 - pad)} {fmt(y0 - pad)} {fmt(x1 - x0 + 2 * pad)} {fmt(y1 - y0 + 2 * pad)}"


def horizontal(letter, cursor_fill, title):
    d, cursor, bbox = wordmark(100, 0, 0)
    body = f'  <path d="{d}" fill="{letter}"/>\n  {rect(cursor, cursor_fill)}'
    return svg(tight(bbox, 6), body, title)


def symbol_parts(box, ink_ratio=0.56):
    """`g` + cursor, centred in a square of side `box`. Returns (path d, cursor rect)."""
    size = 100.0
    d0, b0, _ = GEIST.draw("g", size, 0, 0)
    ch = GEIST.xheight * size / GEIST.upem
    cx = b0[2] + CURSOR_GAP * size
    cursor0 = (cx, -ch, CURSOR_W * size, ch)
    x0, y0, x1, y1 = b0[0], min(b0[1], cursor0[1]), cx + cursor0[2], b0[3]
    k = box * ink_ratio / (x1 - x0)
    ox = (box - (x1 - x0) * k) / 2 - x0 * k
    oy = (box - (y1 - y0) * k) / 2 - y0 * k
    d, _, _ = GEIST.draw("g", size * k, ox, oy)
    cursor = (ox + cursor0[0] * k, oy + cursor0[1] * k, cursor0[2] * k, cursor0[3] * k)
    return d, cursor


def symbol(letter, cursor_fill, title):
    d, cursor = symbol_parts(64)
    body = f'  <path d="{d}" fill="{letter}"/>\n  {rect(cursor, cursor_fill)}'
    return svg("0 0 64 64", body, title)


def app_icon():
    d, cursor = symbol_parts(128)
    body = (
        f'  <rect width="128" height="128" rx="28" fill="#000000"/>\n'
        f'  <rect x="1" y="1" width="126" height="126" rx="27" fill="none" stroke="#333333" stroke-width="2"/>\n'
        f'  <path d="{d}" fill="{TEXT}"/>\n  {rect(cursor, GREEN)}'
    )
    return svg("0 0 128 128", body, "Geass app icon")


def favicon():
    w, h = 32 * 0.30, 32 * 0.56
    body = (
        f'  <rect width="32" height="32" rx="7" fill="{GREEN}"/>\n'
        f'  <rect x="{fmt((32 - w) / 2)}" y="{fmt((32 - h) / 2)}" width="{fmt(w)}" height="{fmt(h)}" fill="{VOID}"/>'
    )
    return svg("0 0 32 32", body, "Geass favicon")


def text_el(face, s, size, x, y, fill, tracking=0.0, anchor="start"):
    d, bounds, adv = face.draw(s, size, x, y, tracking, anchor)
    return f'<path d="{d}" fill="{fill}"/>', bounds, adv


def stacked():
    d, cursor, bbox = wordmark(64, 0, 0)
    width = bbox[2] - bbox[0]
    dx = 160 - bbox[0] - width / 2
    d, cursor, bbox = wordmark(64, dx, 64)
    tag, _, _ = text_el(MONO[500], "ABSOLUTE COMMAND OVER SERVERS YOU ALREADY OWN", 8.6, 160, 118, MUTED, 0.08, "middle")
    body = f'  <path d="{d}" fill="{TEXT}"/>\n  {rect(cursor, GREEN)}\n  {tag}'
    return svg("0 0 320 140", body, "Geass stacked logo for dark backgrounds")


GRID = (
    '<pattern id="grid" width="48" height="48" patternUnits="userSpaceOnUse">'
    '<path d="M48 0H0V48" fill="none" stroke="#FFFFFF" stroke-opacity=".045"/></pattern>'
)


SOCIAL_GRID = (
    '<pattern id="grid" width="40" height="40" patternUnits="userSpaceOnUse">'
    f'<path d="M39.5 0V40M0 39.5H40" fill="none" stroke="{VOID}" stroke-opacity=".08"/></pattern>'
)


def baseline(face, top, size, line_height=None):
    """Baseline y for a line box starting at `top`, placed the way CSS centres the font's content area."""
    asc, desc = face.ascent * size / face.upem, face.descent * size / face.upem
    natural = asc + desc + face.gap * size / face.upem
    lh = natural if line_height is None else line_height
    return top + (lh - (asc + desc)) / 2 + asc, natural


def social():
    """1200x630 link-preview card: a solid C.C. green field with a giant black wordmark, the tagline and three facts."""
    k = 210 / 83  # the horizontal logo's viewBox is 83 units tall; draw it 210px high at (64, 96)
    d, cursor, _ = wordmark(100 * k, 64 + 2.32 * k, 96 + 60.8 * k)
    parts = [
        f"<defs>{SOCIAL_GRID}</defs>",
        f'<rect width="1200" height="630" fill="{GREEN}"/>',
        '<rect width="1200" height="630" fill="url(#grid)"/>',
        f'<path d="{d}" fill="#000000"/>',
        rect(cursor, "#000000"),
    ]
    size, tracking, lh, top = 56, -0.035, 56 * 1.06, 350
    for i, line in enumerate(["Absolute command over servers", "you already own."]):
        y, _ = baseline(GROTESK[700], top + lh * i, size, lh)
        l_d, _, _ = GROTESK[700].draw(line, size, 68, y, tracking)
        parts.append(f'<path d="{l_d}" fill="{VOID}"/>')
    fsize, ftrack = 20, 0.10
    y0, natural = baseline(MONO[400], 0, fsize)
    top_f = 630 - 52 - natural
    x = 68
    for label in ["SELF-HOSTED", "VPS & BARE METAL", "ONE CLOUD TO MANAGE THEM ALL"]:
        parts.append(f'<rect x="{x}" y="{fmt(top_f + natural / 2 - 5)}" width="10" height="10" fill="{VOID}"/>')
        t, _, adv = text_el(MONO[400], label, fsize, x + 22, top_f + y0, VOID, ftrack)
        parts.append(t)
        x += 22 + adv + ftrack * fsize + 28
    return svg("0 0 1200 630", "\n".join("  " + p for p in parts), "Geass social card",
               "Absolute command over servers you already own.")


def brand_board():
    parts = [f"<defs>{GRID}</defs>", f'<rect width="1440" height="960" fill="{VOID}"/>', '<rect width="1440" height="960" fill="url(#grid)"/>']
    label, _, _ = text_el(MONO[500], "GEASS BRAND SYSTEM / 02 / THE CONTRACT", 13, 72, 82, MUTED, 0.08)
    d, cursor, _ = wordmark(150, 72, 270)
    parts += [label, f'<path d="{d}" fill="{TEXT}"/>', rect(cursor, GREEN)]
    h, hb_, hadv = GROTESK[600].draw("Absolute command", 56, 72, 400, -0.04)
    parts += [f'<rect x="62" y="354" width="{fmt(hadv + 20)}" height="68" fill="{GREEN}"/>', f'<path d="{h}" fill="{VOID}"/>']
    o, _, _ = text_el(GROTESK[600], "over servers you already own.", 56, 72 + hadv + 26, 400, TEXT, -0.04)
    parts.append(o)
    swatches = [("C.C. green", GREEN, "#7CE0A0", VOID), ("Deep green", DEEP, "#16935A", "#FFFFFF"), ("Void", VOID, "#0A0A0A", TEXT),
                ("Surface", SURFACE, "#151515", TEXT), ("Line", LINE, "#262626", TEXT), ("Text", TEXT, "#F2F2F2", VOID)]
    for i, (name, fill, hexv, ink) in enumerate(swatches):
        x = 72 + i * 200
        parts.append(f'<rect x="{x}" y="500" width="184" height="120" fill="{fill}" stroke="#333333"/>')
        n, _, _ = text_el(GROTESK[600], name, 17, x + 16, 584, ink)
        hx, _, _ = text_el(MONO[400], hexv, 13, x + 16, 604, ink)
        parts += [n, hx]
    d2, cur2 = symbol_parts(168)
    parts += [
        '<g transform="translate(72 690)">',
        '<rect width="168" height="168" rx="37" fill="#000000" stroke="#333333"/>',
        f'<path d="{d2}" fill="{TEXT}"/>', rect(cur2, GREEN), "</g>",
    ]
    fav = (f'<g transform="translate(272 690)"><rect width="64" height="64" rx="14" fill="{GREEN}"/>'
           f'<rect x="22.4" y="14.1" width="19.2" height="35.8" fill="{VOID}"/></g>')
    parts.append(fav)
    rules = ["Wordmark: Geist 700, lowercase, tracking -5.5%.", "Cursor: 0.30em wide, one x-height tall, on the baseline.",
             "Below 24 px drop the letters; the cursor alone is the icon.", "Green is the accent on dark; use deep green on light.",
             "Type: Space Grotesk for interface, JetBrains Mono for code."]
    for i, r in enumerate(rules):
        t, _, _ = text_el(MONO[400], r, 14, 560, 716 + i * 32, "#C8C8C8")
        parts.append(t)
    return svg("0 0 1440 960", "\n".join("  " + p for p in parts), "Geass brand board")


def write(path, content):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content)
    print("wrote", path.relative_to(ROOT))


def main():
    out = {
        BRAND / "logos/geass-horizontal-dark.svg": horizontal(TEXT, GREEN, "Geass logo for dark backgrounds"),
        BRAND / "logos/geass-horizontal-light.svg": horizontal(VOID, DEEP, "Geass logo for light backgrounds"),
        BRAND / "logos/geass-horizontal-mono-white.svg": horizontal("#FFFFFF", "#FFFFFF", "Geass one-colour white logo"),
        BRAND / "logos/geass-horizontal-mono-black.svg": horizontal("#000000", "#000000", "Geass one-colour black logo"),
        BRAND / "logos/geass-stacked-dark.svg": stacked(),
        BRAND / "logos/geass-symbol.svg": symbol(TEXT, GREEN, "Geass symbol for dark backgrounds"),
        BRAND / "logos/geass-symbol-light.svg": symbol(VOID, DEEP, "Geass symbol for light backgrounds"),
        BRAND / "logos/geass-symbol-mono-white.svg": symbol("#FFFFFF", "#FFFFFF", "Geass one-colour white symbol"),
        BRAND / "logos/geass-symbol-mono-black.svg": symbol("#000000", "#000000", "Geass one-colour black symbol"),
        BRAND / "icons/geass-app-icon.svg": app_icon(),
        BRAND / "icons/favicon.svg": favicon(),
        BRAND / "social/geass-social-card.svg": social(),
        BRAND / "preview/geass-brand-board.svg": brand_board(),
    }
    for path, content in out.items():
        write(path, content)
    # The dashboard serves its own copies from public/.
    mirror = {
        "logos/geass-horizontal-dark.svg": "geass-logo-dark.svg",
        "logos/geass-horizontal-light.svg": "geass-logo-light.svg",
        "logos/geass-symbol.svg": "geass-symbol.svg",
        "logos/geass-symbol-light.svg": "geass-symbol-light.svg",
        "icons/favicon.svg": "favicon.svg",
    }
    for src, dst in mirror.items():
        shutil.copyfile(BRAND / src, PUBLIC / dst)
        print("copied", (PUBLIC / dst).relative_to(ROOT))


if __name__ == "__main__":
    main()
