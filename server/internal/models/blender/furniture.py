# Builds one piece of furniture from its real size and exports it as a GLB.
#
#   blender -b --factory-startup -P furniture.py -- spec.json out.glb [--preview out.png] [--no-draco]
#
# spec.json: {"type": "bookcase", "w": 80, "d": 28, "h": 202, "color": "#f2f1ec",
#             "material": "painted", "options": {"shelves": 5}, "measures": {"seatHeight": 45}}
# Sizes are centimetres. In Blender the front faces -Y and +Z is up; the glTF exporter turns
# that into +Z front, +Y up. The origin is the floor centre of the footprint and the model's
# bounding box is exactly w x h x d.
import json
import math
import random
import sys

import bmesh
import bpy
from mathutils import Matrix, Vector

argv = sys.argv[sys.argv.index("--") + 1:] if "--" in sys.argv else []
if len(argv) < 2:
    print("usage: furniture.py -- spec.json out.glb [--preview out.png] [--no-draco]")
    sys.exit(2)
SPEC_PATH, OUT = argv[0], argv[1]
PREVIEW = argv[argv.index("--preview") + 1] if "--preview" in argv else None
DRACO = "--no-draco" not in argv

with open(SPEC_PATH, encoding="utf-8") as f:
    SPEC = json.load(f)

TYPE = SPEC.get("type", "box")
W = max(SPEC.get("w", 50), 1) / 100
D = max(SPEC.get("d", 50), 1) / 100
H = max(SPEC.get("h", 50), 0.3) / 100
OPT = SPEC.get("options") or {}
MEAS = SPEC.get("measures") or {}
COLOR = SPEC.get("color") or "#e9e7e1"
MATERIAL = SPEC.get("material") or "painted"
random.seed(f"{TYPE}{W}{D}{H}{COLOR}")


def meas(key, default):
    v = MEAS.get(key)
    return v / 100 if isinstance(v, (int, float)) and v > 0 else default


# ---------------------------------------------------------------- materials

def srgb_to_linear(c):
    return c / 12.92 if c <= 0.04045 else ((c + 0.055) / 1.055) ** 2.4


def hex_rgb(h):
    h = h.lstrip("#")
    if len(h) != 6:
        h = "e9e7e1"
    return [int(h[i:i + 2], 16) / 255 for i in (0, 2, 4)]


def shade(h, k):
    """Lighten (k > 0) or darken (k < 0) a hex colour."""
    r = hex_rgb(h)
    if k >= 0:
        r = [c + (1 - c) * k for c in r]
    else:
        r = [c * (1 + k) for c in r]
    return "#" + "".join(f"{max(0, min(255, round(c * 255))):02x}" for c in r)


def luminance(h):
    r, g, b = hex_rgb(h)
    return 0.2126 * r + 0.7152 * g + 0.0722 * b


MATS = {}


def mat(name, hexcol, rough=0.55, metal=0.0, sheen=0.0, emissive=False):
    key = (name, hexcol, rough, metal, sheen)
    if key in MATS:
        return MATS[key]
    m = bpy.data.materials.new(name)
    m.use_nodes = True
    p = m.node_tree.nodes.get("Principled BSDF")
    lin = [srgb_to_linear(c) for c in hex_rgb(hexcol)]
    p.inputs["Base Color"].default_value = (*lin, 1)
    p.inputs["Roughness"].default_value = rough
    p.inputs["Metallic"].default_value = metal
    if sheen > 0 and "Sheen Weight" in p.inputs:
        p.inputs["Sheen Weight"].default_value = sheen
        p.inputs["Sheen Roughness"].default_value = 0.6
    if emissive and "Emission Color" in p.inputs:
        # the scene turns the shade's glow on; it ships dark
        p.inputs["Emission Color"].default_value = (1.0, 0.86, 0.66, 1)
        p.inputs["Emission Strength"].default_value = 0.0
    MATS[key] = m
    return m


def body_mat(hexcol=None, kind=None):
    hexcol = hexcol or COLOR
    kind = kind or MATERIAL
    if kind == "fabric":
        return mat("fabric", hexcol, 0.92, 0, sheen=0.35)
    if kind == "wood":
        return mat("wood", hexcol, 0.6)
    if kind == "metal":
        return mat("metal", hexcol, 0.35, 0.8)
    if kind == "glass":
        return mat("glass", hexcol, 0.08)
    return mat("painted", hexcol, 0.5)


def leg_mat():
    legs = OPT.get("legs")
    if legs == "wood":
        return mat("legs_wood", "#c9a77c", 0.6)
    if legs == "metal" or MATERIAL in ("painted", "fabric"):
        return mat("legs_metal", "#3b3d42", 0.35, 0.8)
    return body_mat(shade(COLOR, -0.12))


def handle_mat():
    if luminance(COLOR) < 0.25:
        return mat("handle", "#c9c4ba", 0.3, 0.85)
    return mat("handle", "#4a4d53", 0.35, 0.8)


# ---------------------------------------------------------------- geometry

PARTS = []


def _finish(bm, name, material, smooth=True):
    me = bpy.data.meshes.new(name)
    bm.to_mesh(me)
    bm.free()
    for poly in me.polygons:
        poly.use_smooth = smooth
    me.materials.append(material)
    ob = bpy.data.objects.new(name, me)
    bpy.context.scene.collection.objects.link(ob)
    PARTS.append(ob)
    return ob


def box(x0, x1, y0, y1, z0, z1, material, r=0.004, seg=2, name="part"):
    """An axis-aligned box from its extents (metres), edges rounded by r."""
    w, d, h = abs(x1 - x0), abs(y1 - y0), abs(z1 - z0)
    if min(w, d, h) < 1e-5:
        return None
    bm = bmesh.new()
    bmesh.ops.create_cube(bm, size=1.0)
    bmesh.ops.scale(bm, vec=Vector((w, d, h)), verts=bm.verts)
    bmesh.ops.translate(bm, vec=Vector(((x0 + x1) / 2, (y0 + y1) / 2, (z0 + z1) / 2)), verts=bm.verts)
    r = min(r, w / 2 - 1e-4, d / 2 - 1e-4, h / 2 - 1e-4)
    if r > 0.0005:
        bmesh.ops.bevel(bm, geom=list(bm.edges), offset=r, segments=seg, profile=0.5, affect="EDGES", clamp_overlap=True)
    return _finish(bm, name, material)


def cushion(x0, x1, y0, y1, z0, z1, material, r=None, name="cushion"):
    """A soft box: big rounded edges and a slight crown on top."""
    w, d, h = abs(x1 - x0), abs(y1 - y0), abs(z1 - z0)
    r = r if r is not None else min(0.045, h * 0.42, w * 0.2, d * 0.2)
    ob = box(x0, x1, y0, y1, z0, z1, material, r=r, seg=3, name=name)
    if ob is None:
        return None
    # crown: lift the middle of the top face a little, keeping the outline
    cx, cy = (x0 + x1) / 2, (y0 + y1) / 2
    top = max(z0, z1)
    for v in ob.data.vertices:
        if v.co.z > top - r * 0.5:
            fx = 1 - min(1, abs(v.co.x - cx) / (w / 2)) ** 2
            fy = 1 - min(1, abs(v.co.y - cy) / (d / 2)) ** 2
            v.co.z -= (1 - fx * fy) * min(0.012, h * 0.08)
    return ob


def cyl(x, y, z0, z1, rad, material, seg=16, r2=None, name="cyl"):
    """A vertical cylinder (or cone when r2 is given) standing on z0."""
    bm = bmesh.new()
    bmesh.ops.create_cone(bm, cap_ends=True, cap_tris=False, segments=seg, radius1=rad, radius2=rad if r2 is None else r2, depth=z1 - z0)
    bmesh.ops.translate(bm, vec=Vector((x, y, (z0 + z1) / 2)), verts=bm.verts)
    return _finish(bm, name, material)


def tilted(x0, x1, ylen, zlen, pivot, angle, material, r=0.03, name="tilted", soft=True):
    """A box ylen deep, zlen high, turned about the x axis by angle (radians) around pivot (y, z),
    its local origin at the box's front-bottom edge."""
    ob = (cushion if soft else box)(x0, x1, 0, ylen, 0, zlen, material, r=r, name=name)
    if ob is None:
        return None
    m = Matrix.Translation((0, pivot[0], pivot[1])) @ Matrix.Rotation(angle, 4, "X")
    ob.data.transform(m)
    return ob


# ---------------------------------------------------------------- types

# depth kept in front of doors and drawer fronts for their handles
HG = 0.012

def build_bookcase():
    m = body_mat()
    t = 0.018
    plinth = 0.06
    box(-W / 2, -W / 2 + t, -D / 2, D / 2, 0, H, m, name="side")
    box(W / 2 - t, W / 2, -D / 2, D / 2, 0, H, m, name="side")
    box(-W / 2 + t, W / 2 - t, -D / 2, D / 2, H - t, H, m, name="top")
    box(-W / 2 + t, W / 2 - t, -D / 2 + 0.012, -D / 2 + 0.03, 0, plinth, m, name="plinth")
    box(-W / 2 + t, W / 2 - t, -D / 2 + 0.012, D / 2 - 0.005, plinth, plinth + t, m, name="bottom")
    box(-W / 2 + t, W / 2 - t, D / 2 - 0.006, D / 2 - 0.001, plinth, H - t, body_mat(shade(COLOR, -0.04)), r=0.001, name="back")
    inner0, inner1 = plinth + t, H - t
    n = OPT.get("shelves")
    if not isinstance(n, int) or n < 0:
        n = max(1, round((inner1 - inner0) / 0.37) - 1)
    step = (inner1 - inner0) / (n + 1)
    levels = [inner0 + step * i for i in range(n + 1)]
    for z in levels[1:]:
        box(-W / 2 + t + 0.001, W / 2 - t - 0.001, -D / 2 + 0.008, D / 2 - 0.007, z - t / 2, z + t / 2, m, name="shelf")
    if OPT.get("decor", True) and D > 0.2:
        palette = ["#c9b79c", "#8c9aa6", "#d8cfc0", "#a7836a", "#6f7f73", "#e6dfd2", "#b4a28a"]
        mats = [mat(f"book{i}", c, 0.7) for i, c in enumerate(palette)]
        width = W - 2 * t - 0.02
        for li, z0 in enumerate(levels):
            z0 = z0 + (t / 2 if li else 0)
            top = (levels[li + 1] - t / 2) if li + 1 < len(levels) else inner1
            room = top - z0
            if room < 0.16 or li % 2 == 1 and li != len(levels) - 1:
                continue
            x = -width / 2
            stop = x + width * (0.55 + 0.3 * random.random())
            while x < stop:
                bw = 0.018 + random.random() * 0.03
                bh = min(room - 0.02, 0.17 + random.random() * 0.12)
                bd = min(D - 0.05, 0.15 + random.random() * 0.07)
                box(x, x + bw, -D / 2 + 0.03 + (D - 0.05 - bd), D / 2 - 0.02, z0, z0 + bh, random.choice(mats), r=0.002, seg=1, name="book")
                x += bw + 0.002


def build_shelf():
    if H > 0.6:
        # open shelving unit: frame and boards, no back
        m = body_mat()
        t = 0.03 if W < 1.0 else 0.035
        box(-W / 2, -W / 2 + t, -D / 2, D / 2, 0, H, m, name="side")
        box(W / 2 - t, W / 2, -D / 2, D / 2, 0, H, m, name="side")
        n = OPT.get("shelves") if isinstance(OPT.get("shelves"), int) else max(2, round(H / 0.36))
        for i in range(n + 1):
            z = (H - t) * i / n
            box(-W / 2 + t, W / 2 - t, -D / 2, D / 2, z, z + t, m, name="board")
        return
    m = body_mat()
    n = OPT.get("shelves") if isinstance(OPT.get("shelves"), int) else max(1, round(H / 0.3))
    t = 0.022
    for i in range(n):
        z = (H - t) * (i / max(1, n - 1)) if n > 1 else H - t
        box(-W / 2, W / 2, -D / 2, D / 2, z, z + t, m, name="board")
        for sx in (-1, 1):
            x = sx * (W / 2 - 0.12)
            box(x - 0.01, x + 0.01, D / 2 - 0.012, D / 2, max(0, z - 0.12), z, leg_mat(), r=0.003, name="bracket")
            box(x - 0.01, x + 0.01, -D / 2 + 0.04, D / 2, z - 0.014, z, leg_mat(), r=0.003, name="bracket")


def build_sofa():
    fab = body_mat(kind="fabric")
    feet_h = 0.06 if H > 0.6 else 0.04
    sh = min(max(meas("seatHeight", 0.45), 0.32), H - 0.15)
    seat_w = meas("seatWidth", 0)
    arm_w = (W - seat_w) / 2 if 0.08 < (W - seat_w) / 2 < 0.35 else min(max(W * 0.09, 0.12), 0.25)
    arm_h = min(max(meas("armrestHeight", sh + 0.2), sh + 0.08), H - 0.04)
    back_t = 0.16
    seat_d = min(meas("seatDepth", D - back_t - 0.22), D - back_t - 0.05)
    cush_t = min(0.16, sh - feet_h - 0.08)
    base_top = sh - cush_t
    x0, x1 = -W / 2 + arm_w, W / 2 - arm_w
    # feet
    for sx in (-1, 1):
        for sy in (-1, 1):
            cyl(sx * (W / 2 - 0.07), sy * (D / 2 - 0.07), 0, feet_h + 0.01, 0.022, leg_mat(), seg=12, name="foot")
    # base under the cushions and the back frame
    box(x0 - 0.01, x1 + 0.01, -D / 2 + 0.01, D / 2 - 0.01, feet_h, base_top, fab, r=0.02, seg=2, name="base")
    box(x0 - 0.01, x1 + 0.01, D / 2 - back_t, D / 2, feet_h, H - 0.1, fab, r=0.04, seg=3, name="backframe")
    # arms
    for sx in (-1, 1):
        a0, a1 = (-W / 2, -W / 2 + arm_w) if sx < 0 else (W / 2 - arm_w, W / 2)
        cushion(a0, a1, -D / 2, D / 2, feet_h, arm_h, fab, r=min(0.06, arm_w * 0.4), name="arm")
    # seats
    n = OPT.get("seats") if isinstance(OPT.get("seats"), int) and OPT.get("seats") > 0 else (3 if W >= 2.0 else 2 if W >= 1.5 else 1)
    gap = 0.006
    sw = (x1 - x0 - gap * (n - 1)) / n
    front = -D / 2 + 0.0
    for i in range(n):
        a = x0 + i * (sw + gap)
        cushion(a, a + sw, front, front + seat_d + 0.02, base_top, sh, fab, name="seat")
        # back cushion leaning a little on the frame
        bt = min(0.2, D - seat_d - back_t + 0.08)
        cushion(a, a + sw, front + seat_d - 0.02, front + seat_d - 0.02 + bt, sh - 0.01, H, fab, r=0.06, name="back")


def build_armchair():
    fab = body_mat(kind="fabric")
    wood = mat("frame_wood", OPT.get("frameColor", "#d9bd8f"), 0.55)
    sh = min(max(meas("seatHeight", 0.42), 0.3), H * 0.6)
    t = 0.035  # frame strip width
    # side frames: floor runner, front post and arm rest
    arm_h = min(max(sh + 0.18, 0.55), H - 0.2)
    for sx in (-1, 1):
        xa, xb = (-W / 2, -W / 2 + t) if sx < 0 else (W / 2 - t, W / 2)
        box(xa, xb, -D / 2 + 0.02, D / 2 - 0.08, 0, 0.035, wood, r=0.012, name="runner")
        box(xa, xb, -D / 2 + 0.02, -D / 2 + 0.06, 0, arm_h, wood, r=0.012, name="post")
        box(xa, xb, -D / 2, D / 2 - 0.25, arm_h - 0.035, arm_h, wood, r=0.012, name="armrest")
        box(xa, xb, D / 2 - 0.15, D / 2 - 0.115, 0, 0.3, wood, r=0.012, name="rearpost")
    # seat and back: one cushion that bends, made as two tilted pads
    x0, x1 = -W / 2 + t + 0.01, W / 2 - t - 0.01
    seat_len = D * 0.62
    tilted(x0, x1, seat_len, 0.11, (-D / 2 + 0.03, sh - 0.12), math.radians(8), fab, r=0.04, name="seat")
    back_len = (H - sh) / math.cos(math.radians(22)) + 0.04
    py = -D / 2 + 0.03 + seat_len * math.cos(math.radians(8)) - 0.05
    pz = sh - 0.12 + seat_len * math.sin(math.radians(8))
    back = tilted(x0, x1, 0.11, back_len, (py, pz), math.radians(-22), fab, r=0.04, name="back")
    # frame under the back
    box(x0 - 0.01, x1 + 0.01, D / 2 - 0.15, D / 2 - 0.115, 0.265, 0.3, wood, r=0.01, name="crossbar")
    box(x0 - 0.01, x1 + 0.01, -D / 2 + 0.03, -D / 2 + 0.065, sh - 0.17, sh - 0.135, wood, r=0.01, name="frontbar")
    _ = back


def build_bed():
    soft = MATERIAL == "fabric"
    frame_m = body_mat()
    frame_h = min(0.32, H * 0.4)
    head_h = H
    head_t = 0.06
    side_t = 0.03
    mw = min(meas("mattressWidth", W - 2 * side_t - 0.02), W - 2 * side_t)
    ml = min(meas("mattressLength", D - head_t - 0.04), D - head_t - side_t)
    # frame: sides, foot board, low base, tiny feet
    for sx in (-1, 1):
        x = sx * (W / 2 - side_t / 2)
        box(x - side_t / 2, x + side_t / 2, -D / 2, D / 2 - head_t, 0.02, frame_h, frame_m, r=0.006 if not soft else 0.02, name="side")
    box(-W / 2, W / 2, -D / 2, -D / 2 + side_t, 0.02, frame_h, frame_m, r=0.006 if not soft else 0.02, name="foot")
    box(-W / 2 + side_t, W / 2 - side_t, -D / 2 + side_t, D / 2 - head_t, 0.1, 0.2, frame_m, r=0.004, name="slats")
    for sx in (-1, 1):
        for sy in (-1, 1):
            box(sx * (W / 2 - 0.06) - 0.02, sx * (W / 2 - 0.06) + 0.02, sy * (D / 2 - 0.08) - 0.02, sy * (D / 2 - 0.08) + 0.02, 0, 0.03, leg_mat(), r=0.004, name="foot")
    # headboard
    if soft:
        cushion(-W / 2, W / 2, D / 2 - head_t - 0.04, D / 2, 0.02, head_h, frame_m, r=0.05, name="headboard")
    else:
        box(-W / 2, W / 2, D / 2 - head_t, D / 2, 0.0, head_h, frame_m, r=0.008, name="headboard")
    # mattress, duvet, pillows
    linen = mat("linen", "#f4f1ea", 0.9, sheen=0.3)
    duvet = mat("duvet", OPT.get("duvet", "#ded6c6"), 0.9, sheen=0.3)
    mz0 = 0.2
    mz1 = mz0 + 0.22
    my0 = -D / 2 + side_t + (D - head_t - side_t - ml) / 2
    my1 = my0 + ml
    cushion(-mw / 2, mw / 2, my0, my1, mz0, mz1, linen, r=0.05, name="mattress")
    cushion(-mw / 2 - 0.015, mw / 2 + 0.015, my0 - 0.015, my0 + ml * 0.68, mz1 - 0.08, mz1 + 0.05, duvet, r=0.05, name="duvet")
    fold = mat("fold", shade(OPT.get("duvet", "#ded6c6"), -0.05), 0.9)
    cushion(-mw / 2 - 0.016, mw / 2 + 0.016, my0 + ml * 0.6, my0 + ml * 0.72, mz1 - 0.06, mz1 + 0.065, fold, r=0.04, name="fold")
    n = 1 if mw < 1.2 else 2
    pw = (mw - 0.12) / n - 0.04 * (n - 1)
    for i in range(n):
        a = -mw / 2 + 0.06 + i * (pw + 0.08)
        cushion(a, a + pw, my1 - 0.38, my1 - 0.04, mz1 - 0.01, mz1 + 0.13, linen, r=0.055, name="pillow")


def build_desk():
    m = body_mat()
    top_t = 0.025
    box(-W / 2, W / 2, -D / 2, D / 2, H - top_t, H, m, r=0.006, name="top")
    drawers = OPT.get("drawers", 2 if W >= 0.9 and MATERIAL == "painted" else 0)
    if drawers:
        uw = min(0.38, W * 0.36)
        ux0 = W / 2 - uw
        box(ux0, W / 2 - 0.004, -D / 2 + HG + 0.01, D / 2 - 0.01, 0, H - top_t, m, r=0.004, name="unit")
        fh = (H - top_t - 0.08) / drawers
        for i in range(drawers):
            z0 = 0.06 + i * fh
            box(ux0 + 0.006, W / 2 - 0.01, -D / 2 + HG, -D / 2 + HG + 0.012, z0 + 0.004, z0 + fh - 0.004, body_mat(shade(COLOR, -0.02)), r=0.003, name="front")
            box(ux0 + uw / 2 - 0.05, ux0 + uw / 2 + 0.05, -D / 2, -D / 2 + HG + 0.002, z0 + fh - 0.036, z0 + fh - 0.022, handle_mat(), r=0.002, name="handle")
        box(-W / 2 + 0.004, -W / 2 + 0.03, -D / 2 + 0.01, D / 2 - 0.01, 0, H - top_t, m, r=0.004, name="panel")
        box(-W / 2 + 0.03, ux0, D / 2 - 0.03, D / 2 - 0.012, H - top_t - 0.25, H - top_t, m, r=0.003, name="modesty")
        return
    legs(H - top_t, inset=0.04)


def legs(top, inset=0.04, rad=0.02, square=None):
    lm = leg_mat()
    square = square if square is not None else (MATERIAL == "wood")
    for sx in (-1, 1):
        for sy in (-1, 1):
            x, y = sx * (W / 2 - inset - rad), sy * (D / 2 - inset - rad)
            if square:
                box(x - rad, x + rad, y - rad, y + rad, 0, top, lm, r=0.004, name="leg")
            else:
                cyl(x, y, 0, top, rad, lm, seg=14, name="leg")
    if square:
        box(-W / 2 + inset, W / 2 - inset, -D / 2 + inset + rad, -D / 2 + inset + rad + 0.018, top - 0.08, top, lm, r=0.003, name="apron")
        box(-W / 2 + inset, W / 2 - inset, D / 2 - inset - rad - 0.018, D / 2 - inset - rad, top - 0.08, top, lm, r=0.003, name="apron")


def build_table():
    m = body_mat()
    top_t = 0.03 if MATERIAL == "wood" else 0.025
    box(-W / 2, W / 2, -D / 2, D / 2, H - top_t, H, m, r=0.008, name="top")
    legs(H - top_t, inset=0.05, rad=0.022)


def build_chair():
    m = body_mat()
    lm = leg_mat() if MATERIAL != "wood" else m
    sh = min(max(meas("seatHeight", 0.45), 0.35), H - 0.25)
    st = 0.04
    rad = 0.016
    for sx in (-1, 1):
        x = sx * (W / 2 - 0.03)
        cyl(x, -D / 2 + 0.04, 0, sh - st, rad, lm, seg=12, name="leg")
        cyl(x, D / 2 - 0.04, 0, H - 0.02, rad, lm, seg=12, name="post")
    seat_m = body_mat(kind="fabric") if MATERIAL == "fabric" else m
    cushion(-W / 2, W / 2, -D / 2, D / 2 - 0.02, sh - st, sh, seat_m, r=0.018, name="seat")
    box(-W / 2 + 0.01, W / 2 - 0.01, D / 2 - 0.05, D / 2, H - 0.22, H, m, r=0.02, name="backrest")


def build_wardrobe():
    m = body_mat()
    t = 0.018
    plinth = 0.05
    box(-W / 2, W / 2, -D / 2 + 0.02, D / 2, 0, H, m, r=0.004, name="carcass")
    box(-W / 2 + 0.01, W / 2 - 0.01, -D / 2 + 0.006, -D / 2 + 0.022, 0, plinth, body_mat(shade(COLOR, -0.08)), r=0.002, name="plinth")
    n = OPT.get("doors") if isinstance(OPT.get("doors"), int) and OPT.get("doors") > 0 else (1 if W <= 0.6 else 2 if W <= 1.2 else 3)
    gap = 0.003
    dw = (W - gap * (n - 1)) / n
    fm = body_mat(shade(COLOR, -0.015))
    hm = handle_mat()
    for i in range(n):
        a = -W / 2 + i * (dw + gap)
        box(a, a + dw, -D / 2 + HG, -D / 2 + HG + t, plinth + 0.004, H - 0.004, fm, r=0.003, name="door")
        # handle near the meeting edge (single door: right edge)
        right = (i % 2 == 0 and i + 1 < n) if n > 1 else True
        hx = a + dw - 0.04 if right else a + 0.04
        hz = min(1.1, H * 0.55)
        box(hx - 0.007, hx + 0.007, -D / 2, -D / 2 + HG + 0.002, hz - 0.12, hz + 0.12, hm, r=0.003, name="handle")


def drawer_grid(x0, x1, z0, z1, n, cols, y_front, fm, hm, handle=True):
    rows = math.ceil(n / cols)
    gap = 0.003
    cw = (x1 - x0 - gap * (cols - 1)) / cols
    rh = (z1 - z0 - gap * (rows - 1)) / rows
    for r in range(rows):
        for c in range(cols):
            a = x0 + c * (cw + gap)
            b = z0 + r * (rh + gap)
            box(a, a + cw, y_front + HG, y_front + HG + 0.02, b, b + rh, fm, r=0.003, name="front")
            if handle:
                hw = min(0.16, cw * 0.4)
                mid = a + cw / 2
                hz = b + rh - min(0.05, rh * 0.3)
                box(mid - hw / 2, mid + hw / 2, y_front, y_front + HG + 0.002, hz - 0.007, hz + 0.007, hm, r=0.002, name="handle")


def build_drawers():
    m = body_mat()
    plinth = 0.04 if H > 0.5 else 0.02
    box(-W / 2, W / 2, -D / 2 + 0.02, D / 2, 0, H, m, r=0.004, name="carcass")
    n = OPT.get("drawers") if isinstance(OPT.get("drawers"), int) and OPT.get("drawers") > 0 else max(2, round((H - plinth) / 0.18))
    cols = 2 if W > 1.2 and n % 2 == 0 else 1
    drawer_grid(-W / 2 + 0.004, W / 2 - 0.004, plinth + 0.004, H - 0.004, n, cols, -D / 2, body_mat(shade(COLOR, -0.015)), handle_mat())


def build_tvbench():
    m = body_mat()
    lh = 0.08 if H > 0.4 else 0.05
    y0 = -D / 2 + HG + 0.02  # carcass front, behind the doors
    door_w = (W - 0.02) * 0.3
    inner = W - 0.02 - 2 * door_w
    z0, z1 = lh + 0.004, H - 0.03
    # two closed cabinets either side of an open compartment
    for sx in (-1, 1):
        a, b = sorted((sx * (W / 2 - 0.01), sx * (W / 2 - 0.01 - door_w)))
        box(a, b, y0, D / 2, lh, H - 0.025, m, r=0.004, name="cabinet")
    box(-inner / 2, inner / 2, y0, D / 2, lh, lh + 0.018, m, r=0.002, name="bottom")
    box(-inner / 2, inner / 2, D / 2 - 0.012, D / 2, lh, H - 0.025, body_mat(shade(COLOR, -0.35)), r=0.001, name="back")
    box(-inner / 2, inner / 2, -D / 2 + HG, D / 2 - 0.012, (z0 + z1) / 2 - 0.009, (z0 + z1) / 2 + 0.009, m, r=0.003, name="midshelf")
    box(-W / 2, W / 2, -D / 2, D / 2, H - 0.025, H, m, r=0.005, name="top")
    for sx in (-1, 1):
        for sy in (-1, 1):
            x, y = sx * (W / 2 - 0.06), sy * (D / 2 - 0.06)
            box(x - 0.02, x + 0.02, y - 0.02, y + 0.02, 0, lh + 0.002, leg_mat() if MATERIAL != "wood" else m, r=0.004, name="leg")
    fm = body_mat(shade(COLOR, -0.015))
    hm = handle_mat()
    for sx in (-1, 1):
        a = sx * (W / 2 - 0.01 - door_w / 2)
        box(a - door_w / 2 + 0.002, a + door_w / 2 - 0.002, -D / 2 + HG, -D / 2 + HG + 0.02, z0, z1, fm, r=0.003, name="door")
        hx = a - sx * (door_w / 2 - 0.035)
        box(hx - 0.006, hx + 0.006, -D / 2, -D / 2 + HG + 0.002, (z0 + z1) / 2 - 0.05, (z0 + z1) / 2 + 0.05, hm, r=0.002, name="handle")


def build_lamp():
    lm = leg_mat()
    shade_m = mat("lamp_shade", OPT.get("shadeColor", "#f3ece0"), 0.85, emissive=True)
    rad = min(W, D) / 2
    shade_h = min(0.32, H * 0.28) if H > 0.7 else H * 0.45
    base_r = min(rad * 0.65, 0.16)
    cyl(0, 0, 0, 0.025, base_r, lm, seg=28, name="base")
    cyl(0, 0, 0.025, H - shade_h * 0.5, 0.011, lm, seg=10, name="stem")
    cyl(0, 0, H - shade_h, H, rad, shade_m, seg=36, r2=rad * 0.78, name="shade")


def build_rug():
    h = max(H, 0.012)
    border = mat("rug_border", shade(COLOR, -0.18), 0.95, sheen=0.4)
    field = mat("rug_field", COLOR, 0.95, sheen=0.4)
    b = min(0.07, min(W, D) * 0.06)
    box(-W / 2, W / 2, -D / 2, D / 2, 0, h * 0.85, border, r=min(0.006, h * 0.4), seg=1, name="border")
    box(-W / 2 + b, W / 2 - b, -D / 2 + b, D / 2 - b, h * 0.5, h, field, r=min(0.004, h * 0.3), seg=1, name="field")


def build_box():
    box(-W / 2, W / 2, -D / 2, D / 2, 0, H, body_mat(), r=min(0.02, min(W, D, H) * 0.1), seg=2, name="box")


BUILDERS = {
    "bookcase": build_bookcase, "shelf": build_shelf, "sofa": build_sofa, "armchair": build_armchair,
    "bed": build_bed, "desk": build_desk, "table": build_table, "chair": build_chair,
    "wardrobe": build_wardrobe, "drawers": build_drawers, "tvbench": build_tvbench,
    "lamp": build_lamp, "rug": build_rug, "box": build_box,
}

# ---------------------------------------------------------------- assemble

for ob in list(bpy.data.objects):
    bpy.data.objects.remove(ob)

BUILDERS.get(TYPE, build_box)()
if TYPE == "rug":
    H = max(H, 0.012)

# one object, one primitive per material
bpy.ops.object.select_all(action="DESELECT")
for ob in PARTS:
    ob.select_set(True)
bpy.context.view_layer.objects.active = PARTS[0]
if len(PARTS) > 1:
    bpy.ops.object.join()
obj = bpy.context.view_layer.objects.active
obj.name = TYPE
me = obj.data
me.name = TYPE

# fit exactly into W x D x H (parts are built close to it; this removes the last millimetres)
xs = [v.co.x for v in me.vertices]
ys = [v.co.y for v in me.vertices]
zs = [v.co.z for v in me.vertices]
sx = W / max(1e-6, max(xs) - min(xs))
sy = D / max(1e-6, max(ys) - min(ys))
sz = H / max(1e-6, max(zs) - min(zs))
cx, cy = (max(xs) + min(xs)) / 2, (max(ys) + min(ys)) / 2
me.transform(Matrix.Diagonal((sx, sy, sz, 1)) @ Matrix.Translation((-cx, -cy, -min(zs))))

# merge doubles, weighted normals so flat faces stay flat and bevels read soft
bm = bmesh.new()
bm.from_mesh(me)
bmesh.ops.remove_doubles(bm, verts=bm.verts, dist=1e-5)
bm.to_mesh(me)
bm.free()
wn = obj.modifiers.new("wn", "WEIGHTED_NORMAL")
wn.keep_sharp = True
wn.weight = 50
bpy.ops.object.modifier_apply(modifier="wn")

tris = sum(len(p.vertices) - 2 for p in me.polygons)
print(f"HT-STATS type={TYPE} tris={tris} parts={len(PARTS)} size={W:.3f}x{D:.3f}x{H:.3f} scale={sx:.4f},{sy:.4f},{sz:.4f}")

bpy.ops.export_scene.gltf(
    filepath=OUT,
    export_format="GLB",
    use_selection=True,
    export_apply=True,
    export_yup=True,
    export_materials="EXPORT",
    export_cameras=False,
    export_lights=False,
    export_normals=True,
    export_texcoords=False,
    export_draco_mesh_compression_enable=DRACO,
    export_draco_mesh_compression_level=6,
)
print(f"HT-OK {OUT}")

# ---------------------------------------------------------------- preview

if PREVIEW:
    scn = bpy.context.scene
    scn.render.engine = "BLENDER_EEVEE"
    scn.render.resolution_x, scn.render.resolution_y = 800, 600
    scn.render.film_transparent = False
    try:
        scn.eevee.taa_render_samples = 48
        scn.eevee.use_shadows = True
    except AttributeError:
        pass
    scn.view_settings.view_transform = "AgX"
    world = bpy.data.worlds.new("w")
    world.use_nodes = True
    world.node_tree.nodes["Background"].inputs[0].default_value = (0.82, 0.83, 0.85, 1)
    world.node_tree.nodes["Background"].inputs[1].default_value = 0.55
    scn.world = world
    # floor
    bpy.ops.mesh.primitive_plane_add(size=40)
    floor = bpy.context.active_object
    fm = bpy.data.materials.new("floor")
    fm.use_nodes = True
    fm.node_tree.nodes["Principled BSDF"].inputs["Base Color"].default_value = (0.8, 0.8, 0.82, 1)
    fm.node_tree.nodes["Principled BSDF"].inputs["Roughness"].default_value = 0.9
    floor.data.materials.append(fm)
    # soft key and fill
    def area(loc, energy, size):
        ld = bpy.data.lights.new("l", "AREA")
        ld.energy = energy
        ld.size = size
        lo = bpy.data.objects.new("l", ld)
        scn.collection.objects.link(lo)
        lo.location = loc
        d = Vector((0, 0, H / 2)) - Vector(loc)
        lo.rotation_euler = d.to_track_quat("-Z", "Y").to_euler()
    span = max(W, D, H)
    area((span * 1.5, -span * 2.0, span * 2.5), 260 * span * span, span * 2.5)
    area((-span * 2.2, -span * 0.8, span * 1.2), 70 * span * span, span * 3)
    area((0, span * 2.5, span * 2), 60 * span * span, span * 3)
    cam_d = bpy.data.cameras.new("c")
    cam_d.lens = 50
    cam = bpy.data.objects.new("c", cam_d)
    scn.collection.objects.link(cam)
    scn.camera = cam
    center = Vector((0, 0, H * 0.45))
    radius = 0.5 * math.sqrt(W * W + D * D + H * H)
    dirv = Vector((0.75, -1.25, 0.7)).normalized()
    dist = radius / math.sin(math.radians(14)) * 1.04
    cam.location = center + dirv * dist
    cam.rotation_euler = (center - cam.location).to_track_quat("-Z", "Y").to_euler()
    cam_d.clip_end = dist * 4
    scn.render.filepath = PREVIEW
    bpy.ops.render.render(write_still=True)
    print(f"HT-PREVIEW {PREVIEW}")
