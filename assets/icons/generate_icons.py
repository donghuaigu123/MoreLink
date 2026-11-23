#!/usr/bin/env python3
# MoreLink icon generator. Produces a unified "interconnect" brand mark and a
# relay variant with a distinct central hub. No external deps beyond Pillow.
import os, struct, math
from PIL import Image, ImageDraw, ImageFilter

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(HERE, "..", ".."))
S = 512                      # design canvas (supersampled)
SS = 4                       # supersample factor -> 2048 draw then downscale

# Brand palette (matches the dark high-contrast UI)
BG_DARK   = (11, 15, 25)     # #0B0F19
BG_LIGHT  = (22, 32, 54)     # #162236
TEAL      = (110, 231, 183)  # #6EE7B7
TEAL_DK   = (52, 211, 153)   # #34D399
BLUE      = (56, 189, 248)   # #38BDF8
BLUE_DK   = (14, 165, 233)   # #0EA5E9
AMBER     = (251, 191, 36)   # #FBBF24  (relay accent)
TEXT      = (230, 237, 247)


def lerp(a, b, t):
    return tuple(int(a[i] + (b[i] - a[i]) * t) for i in range(3))


def rounded(draw, box, r, fill, outline=None, width=0):
    draw.rounded_rectangle(box, radius=r, fill=fill, outline=outline, width=width)


def radial_bg(size):
    """Radial gradient background from BG_LIGHT center to BG_DARK edge."""
    import numpy as np
    yy, xx = np.mgrid[0:size, 0:size]
    cx = cy = size / 2
    dist = np.sqrt((xx - cx) ** 2 + (yy - cy) ** 2) / (size * 0.72)
    dist = np.clip(dist, 0, 1)
    arr = np.zeros((size, size, 3), dtype=np.uint8)
    for i in range(3):
        arr[:, :, i] = (BG_LIGHT[i] * (1 - dist) + BG_DARK[i] * dist).astype(np.uint8)
    return Image.fromarray(arr, "RGB").convert("RGBA")


def base_canvas():
    """Draw the common rounded-square dark badge at supersample resolution."""
    img = radial_bg(S * SS).resize((S, S), Image.LANCZOS)
    mask = Image.new("L", (S, S), 0)
    md = ImageDraw.Draw(mask)
    md.rounded_rectangle((8, 8, S - 8, S - 8), radius=100, fill=255)
    badge = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    badge.paste(img, (0, 0))
    badge.putalpha(mask)
    # Subtle glowing rim
    rim = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    rd = ImageDraw.Draw(rim)
    rd.rounded_rectangle((8, 8, S - 8, S - 8), radius=100, outline=(*TEAL, 120), width=6)
    rim = rim.filter(ImageFilter.GaussianBlur(6))
    return Image.alpha_composite(badge, rim)


def device_phone(draw, cx, cy, color_top, color_bot):
    """Phone silhouette with a screen band."""
    w, h = 96, 168
    x0, y0 = cx - w // 2, cy - h // 2
    # vertical gradient body
    for i in range(h):
        t = i / max(1, h - 1)
        draw.line((x0, y0 + i, x0 + w, y0 + i), fill=lerp(color_top, color_bot, t))
    rounded(draw, (x0, y0, x0 + w, y0 + h), 20, None, outline=(255, 255, 255, 60), width=3)
    # screen
    rounded(draw, (x0 + 12, y0 + 22, x0 + w - 12, y0 + h - 34), 10, BG_DARK)
    # speaker + home
    draw.rounded_rectangle((cx - 14, y0 + 12, cx + 14, y0 + 17), radius=3, fill=(255, 255, 255, 150))
    draw.ellipse((cx - 9, y0 + h - 26, cx + 9, y0 + h - 8), outline=(255, 255, 255, 120), width=3)


def device_desktop(draw, cx, cy, color_top, color_bot):
    """Desktop monitor silhouette with a stand."""
    w, h = 150, 104
    x0, y0 = cx - w // 2, cy - h // 2 - 10
    for i in range(h):
        t = i / max(1, h - 1)
        draw.line((x0, y0 + i, x0 + w, y0 + i), fill=lerp(color_top, color_bot, t))
    rounded(draw, (x0, y0, x0 + w, y0 + h), 16, None, outline=(255, 255, 255, 60), width=3)
    rounded(draw, (x0 + 12, y0 + 12, x0 + w - 12, y0 + h - 16), 8, BG_DARK)
    # stand
    draw.rounded_rectangle((cx - 10, y0 + h, cx + 10, y0 + h + 26), radius=4, fill=color_bot)
    draw.rounded_rectangle((cx - 42, y0 + h + 24, cx + 42, y0 + h + 32), radius=6, fill=color_top)


def glow_line(draw, points, color, width, glow):
    """Draw a glowing polyline (glow done on a separate blurred layer)."""
    draw.line(points, fill=(*color, 255), width=width, joint="curve")
    for p in points:
        r = width
        draw.ellipse((p[0] - r, p[1] - r, p[0] + r, p[1] + r), fill=(*color, 255))


def draw_link(draw, x1, x2, y, c1, c2):
    """Three-segment link line with渐变 and node dots."""
    segs = 24
    for i in range(segs):
        t0 = i / segs
        t1 = (i + 1) / segs
        xa = int(x1 + (x2 - x1) * t0)
        xb = int(x1 + (x2 - x1) * t1) + 1
        col = lerp(c1, c2, (t0 + t1) / 2)
        draw.line((xa, y, xb, y), fill=(*col, 255), width=14)
    # chain nodes
    for t in (0.25, 0.5, 0.75):
        x = int(x1 + (x2 - x1) * t)
        col = lerp(c1, c2, t)
        draw.ellipse((x - 16, y - 16, x + 16, y + 16), fill=BG_DARK, outline=(*col, 255), width=7)
        draw.ellipse((x - 5, y - 5, x + 5, y + 5), fill=(*col, 255))


def make_icon(relay=False):
    img = base_canvas()
    # Glow layer for the link
    glow = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    gd = ImageDraw.Draw(glow)
    y = 258
    if relay:
        x1, xhub, x2 = 150, 256, 362
        glow_line(gd, [(x1, y), (xhub, y)], TEAL, 18, True)
        glow_line(gd, [(xhub, y), (x2, y)], BLUE, 18, True)
    else:
        x1, x2 = 150, 362
        draw_link(gd, x1, x2, y, TEAL, BLUE)
    glow = glow.filter(ImageFilter.GaussianBlur(14))
    img = Image.alpha_composite(img, glow)

    d = ImageDraw.Draw(img)
    if relay:
        # Devices a bit smaller to leave room for the hub
        device_phone(d, 150, y, TEAL, TEAL_DK)
        device_desktop(d, 362, y, BLUE, BLUE_DK)
        draw_link(d, 196, 224, y, TEAL, TEAL)
        draw_link(d, 288, 316, y, BLUE, BLUE)
        # Central relay hub: amber ring + blue core + bidirectional arrows
        hub = 256
        d.ellipse((hub - 52, y - 52, hub + 52, y + 52), fill=BG_DARK, outline=(*AMBER, 255), width=12)
        d.ellipse((hub - 34, y - 34, hub + 34, y + 34), fill=BLUE_DK)
        # double arrows (⇄)
        d.polygon([(hub - 24, y - 12), (hub - 4, y - 12), (hub + 6, y), (hub - 4, y + 12), (hub - 24, y + 12), (hub - 14, y)], fill=TEXT)
        d.polygon([(hub + 24, y + 12), (hub + 4, y + 12), (hub - 6, y), (hub + 4, y - 12), (hub + 24, y - 12), (hub + 14, y)], fill=TEXT)
        # small relay waves above
        for r, a in ((20, 200), (34, 140)):
            d.arc((hub - r, y - 78 - r, hub + r, y - 78 + r), 200, 340, fill=(*AMBER, a), width=6)
    else:
        device_phone(d, 150, y, TEAL, TEAL_DK)
        device_desktop(d, 362, y, BLUE, BLUE_DK)
        draw_link(d, 198, 314, y, TEAL, BLUE)
        # small signal arcs above the link to emphasize wireless互联
        for r, a in ((26, 180), (44, 120)):
            d.arc((256 - r, y - 70 - r, 256 + r, y - 70 + r), 205, 335, fill=(*TEAL, a), width=6)

    return img


def save_png(img, path, size):
    img.resize((size, size), Image.LANCZOS).save(path)


def make_ico(img, path):
    sizes = [(16, 16), (24, 24), (32, 32), (48, 48), (64, 64), (128, 128), (256, 256)]
    img.save(path, format="ICO", sizes=sizes)


def dib_from_img(img, size):
    """Traditional BMP-format DIB (Win7 compatible) for a given size."""
    im = img.resize(size, Image.LANCZOS).convert("RGBA")
    w, h = size
    pix = im.load()
    header = struct.pack("<IiiHHIIiiII", 40, w, h * 2, 1, 32, 0, w * h * 4, 0, 0, 0, 0)
    xor = bytearray()
    for yy in range(h - 1, -1, -1):       # bottom-up
        for xx in range(w):
            r, g, b, a = pix[xx, yy]
            xor += bytes((b, g, r, a))
    and_row = ((w + 31) // 32) * 4
    and_mask = bytearray(and_row * h)     # alpha handled by 32bpp XOR
    return header + bytes(xor) + bytes(and_mask)


def build_syso(ico_entries, path):
    """Build a COFF .syso containing RT_ICON + RT_GROUP_ICON resources."""
    # ico_entries: list of (size, dib_bytes) with unique resource ids
    ids = list(range(2, 2 + len(ico_entries)))
    group_id = 1
    icon_data = {}
    for (size, dib), rid in zip(ico_entries, ids):
        icon_data[rid] = dib

    # GROUP_ICON data
    group = struct.pack("<HHH", 0, 1, len(ico_entries))  # reserved, type=1, count
    for (size, dib), rid in zip(ico_entries, ids):
        b = size[0] if size[0] < 256 else 0
        group += struct.pack("<BBBBHHII", b, b, 0, 0, 1, 32, len(dib), rid)
    group_data = group

    # Build resource directory tree. All offsets relative to rsrc start.
    # Layout: we compute byte offsets as we emit.
    def directory(entries, subdirs, data_index):
        # entries: list of (id, target) where target is ('dir', builder) or ('data', data_key)
        pass

    # Simpler explicit layout. We'll assemble sections in order:
    # [dir_type][dir_name_group][dir_lang_group][dir_name_icon...][dir_lang_icon...]
    # [data_entries][data blobs]
    # First compute sizes.
    n = len(ico_entries)
    # Type dir: 2 entries (GROUP=14, ICON=3)
    # Name dirs: group(1 entry) + n icon(1 entry each)
    # Lang dirs: group + n icons (1 entry each)
    # Data entries: group + n icons (16 bytes each)
    off = 0
    def alloc(sz):
        nonlocal off
        o = off
        off += sz
        return o

    off_type = alloc(16 + 2 * 8)
    off_name_group = alloc(16 + 1 * 8)
    off_lang_group = alloc(16 + 1 * 8)
    off_name_icon = [alloc(16 + 1 * 8) for _ in range(n)]
    off_lang_icon = [alloc(16 + 1 * 8) for _ in range(n)]
    off_de_group = alloc(16)
    off_de_icon = [alloc(16) for _ in range(n)]
    off_data_group = alloc(len(group_data))
    off_data_icon = []
    for (size, dib) in ico_entries:
        off_data_icon.append(alloc(len(dib)))
    rsrc_size = off

    buf = bytearray(rsrc_size)

    def put(off_, b):
        buf[off_:off_ + len(b)] = b

    def put_dir(off_, id_entries):
        # id_entries: list of (id, is_dir, target_offset)
        b = struct.pack("<IIHHHH", 0, 0, 0, 0, 0, len(id_entries))
        for eid, isdir, target in id_entries:
            t = target | (0x80000000 if isdir else 0)
            b += struct.pack("<II", eid, t)
        put(off_, b)

    # Type directory
    put_dir(off_type, [(14, True, off_name_group), (3, True, off_name_icon[0])])
    # group name -> lang dir
    put_dir(off_name_group, [(group_id, True, off_lang_group)])
    put_dir(off_lang_group, [(0x409, False, off_de_group)])
    put(off_de_group, struct.pack("<IIII", off_data_group, len(group_data), 0, 0))
    put(off_data_group, group_data)
    # icons: ICON type name chain points to first icon name dir; each icon name dir
    # holds one id whose lang dir points to its data entry. The type dir's ICON
    # entry points to off_name_icon[0]; we chain subsequent icons by making the
    # ICON name directory contain all n ids (standard layout).
    # Rebuild: single ICON name directory with n entries.
    # (We already allocated n name dirs; use the first as the full list, others unused.)
    # Overwrite type dir entry for ICON to a merged name dir we lay out now.
    # To keep it simple, place all icon ids in off_name_icon[0].
    entries = []
    for i, rid in enumerate(ids):
        entries.append((rid, True, off_lang_icon[i]))
    put_dir(off_name_icon[0], entries)
    for i, rid in enumerate(ids):
        put_dir(off_lang_icon[i], [(0x409, False, off_de_icon[i])])
        dib = icon_data[rid]
        put(off_de_icon[i], struct.pack("<IIII", off_data_icon[i], len(dib), 0, 0))
        put(off_data_icon[i], dib)

    # COFF header + .rsrc section header
    coff = struct.pack("<HHIIIHH", 0x8664, 1, 0, 0, 0, 0, 0)
    sec = bytearray(40)
    sec[0:8] = b".rsrc\0\0\0"
    struct.pack_into("<I", sec, 8, rsrc_size)      # VirtualSize
    struct.pack_into("<I", sec, 12, 0)             # VirtualAddress (filled by linker)
    struct.pack_into("<I", sec, 16, rsrc_size)     # SizeOfRawData
    struct.pack_into("<I", sec, 20, 60)            # PointerToRawData
    struct.pack_into("<I", sec, 36, 0x40000040)    # INITIALIZED_DATA | READ
    with open(path, "wb") as f:
        f.write(coff + bytes(sec) + bytes(buf))
    return rsrc_size


def main():
    out_icons = HERE
    res = os.path.join(ROOT, "android", "app", "src", "main", "res")

    main_img = make_icon(relay=False)
    relay_img = make_icon(relay=True)

    # High-res sources
    save_png(main_img, os.path.join(out_icons, "morelink-512.png"), 512)
    save_png(relay_img, os.path.join(out_icons, "morelink-relay-512.png"), 512)

    # Windows ICO + COFF syso
    ico_entries = []
    for sz in (16, 24, 32, 48, 64, 128, 256):
        ico_entries.append(((sz, sz), dib_from_img(main_img, (sz, sz))))
    make_ico(main_img, os.path.join(out_icons, "morelink.ico"))
    build_syso(ico_entries, os.path.join(out_icons, "morelink.syso"))

    ico_entries_r = []
    for sz in (16, 24, 32, 48, 64, 128, 256):
        ico_entries_r.append(((sz, sz), dib_from_img(relay_img, (sz, sz))))
    make_ico(relay_img, os.path.join(out_icons, "morelink-relay.ico"))
    build_syso(ico_entries_r, os.path.join(out_icons, "morelink-relay.syso"))

    # Android launcher icons at all densities (main brand; app icon uses main)
    densities = {"mdpi": 48, "hdpi": 72, "xhdpi": 96, "xxhdpi": 144, "xxxhdpi": 192}
    for name, sz in densities.items():
        d = os.path.join(res, "mipmap-" + name)
        os.makedirs(d, exist_ok=True)
        save_png(main_img, os.path.join(d, "ic_launcher.png"), sz)

    print("ICONS_OK")
    for f in ("morelink-512.png", "morelink-relay-512.png", "morelink.ico",
              "morelink-relay.ico", "morelink.syso", "morelink-relay.syso"):
        p = os.path.join(out_icons, f)
        print(" ", f, os.path.getsize(p), "bytes")


if __name__ == "__main__":
    main()
