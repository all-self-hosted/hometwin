# Static props for the scene, exported as GLB.
#
#   blender -b --factory-startup -P props.py -- <out dir> [--preview]
#
# plant-tall.glb: a potted floor plant about 1.2 m high; plant-small.glb: about 0.5 m.
# Front faces +Z, +Y up, origin at the floor centre (same as the furniture).
import math
import os
import random
import sys

import bmesh
import bpy
from mathutils import Euler, Matrix, Vector

argv = sys.argv[sys.argv.index("--") + 1:] if "--" in sys.argv else []
OUT = argv[0] if argv else "."
PREVIEW = "--preview" in argv
DRACO = "--no-draco" not in argv


def srgb_to_linear(c):
    return c / 12.92 if c <= 0.04045 else ((c + 0.055) / 1.055) ** 2.4


def mat(name, hexcol, rough=0.6, sheen=0.0):
    m = bpy.data.materials.get(name) or bpy.data.materials.new(name)
    m.use_nodes = True
    p = m.node_tree.nodes["Principled BSDF"]
    h = hexcol.lstrip("#")
    p.inputs["Base Color"].default_value = (*[srgb_to_linear(int(h[i:i + 2], 16) / 255) for i in (0, 2, 4)], 1)
    p.inputs["Roughness"].default_value = rough
    if sheen and "Sheen Weight" in p.inputs:
        p.inputs["Sheen Weight"].default_value = sheen
    return m


PARTS = []


def finish(bm, material, name, smooth=True):
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


def pot(r_top, r_bot, h, material, soil):
    bm = bmesh.new()
    bmesh.ops.create_cone(bm, cap_ends=True, segments=28, radius1=r_bot, radius2=r_top, depth=h)
    bmesh.ops.translate(bm, vec=Vector((0, 0, h / 2)), verts=bm.verts)
    bmesh.ops.bevel(bm, geom=[e for e in bm.edges if any(len(f.verts) > 4 for f in e.link_faces)], offset=0.008, segments=2, affect="EDGES", clamp_overlap=True)
    finish(bm, material, "pot")
    bm = bmesh.new()
    bmesh.ops.create_cone(bm, cap_ends=True, segments=24, radius1=r_top * 0.9, radius2=r_top * 0.9, depth=0.01)
    bmesh.ops.translate(bm, vec=Vector((0, 0, h - 0.025)), verts=bm.verts)
    finish(bm, soil, "soil")


def leaf(base, direction, length, width, material, curl=0.25):
    """A curved leaf: a flattened, bent ellipsoid from base along direction."""
    bm = bmesh.new()
    bmesh.ops.create_uvsphere(bm, u_segments=10, v_segments=7, radius=0.5)
    for v in bm.verts:
        x, y, z = v.co
        y = y + 0.5  # 0..1 along the leaf
        v.co = Vector((x * width, y * length, z * width * 0.12 + curl * length * (y ** 2)))
    d = Vector(direction).normalized()
    rot = Vector((0, 1, 0)).rotation_difference(d).to_matrix().to_4x4()
    bmesh.ops.transform(bm, matrix=Matrix.Translation(base) @ rot, verts=bm.verts)
    finish(bm, material, "leaf")


def stem(a, b, r, material):
    a, b = Vector(a), Vector(b)
    bm = bmesh.new()
    bmesh.ops.create_cone(bm, cap_ends=False, segments=6, radius1=r, radius2=r * 0.7, depth=(b - a).length)
    rot = Vector((0, 0, 1)).rotation_difference((b - a).normalized()).to_matrix().to_4x4()
    bmesh.ops.transform(bm, matrix=Matrix.Translation((a + b) / 2) @ rot, verts=bm.verts)
    finish(bm, material, "stem")


def export(name):
    bpy.ops.object.select_all(action="DESELECT")
    for ob in PARTS:
        ob.select_set(True)
    bpy.context.view_layer.objects.active = PARTS[0]
    bpy.ops.object.join()
    ob = bpy.context.view_layer.objects.active
    ob.name = name
    tris = sum(len(p.vertices) - 2 for p in ob.data.polygons)
    zs = [v.co.z for v in ob.data.vertices]
    print(f"HT-STATS {name} tris={tris} h={max(zs) - min(zs):.3f}")
    path = os.path.join(OUT, name + ".glb")
    bpy.ops.export_scene.gltf(filepath=path, export_format="GLB", use_selection=True, export_apply=True,
                              export_yup=True, export_cameras=False, export_lights=False, export_texcoords=False,
                              export_draco_mesh_compression_enable=DRACO)
    if PREVIEW:
        render(ob, os.path.join(OUT, name + ".png"))
    bpy.data.objects.remove(ob)
    PARTS.clear()


def render(ob, path):
    scn = bpy.context.scene
    scn.render.engine = "BLENDER_EEVEE"
    scn.render.resolution_x, scn.render.resolution_y = 600, 600
    world = bpy.data.worlds.new("w")
    world.use_nodes = True
    world.node_tree.nodes["Background"].inputs[0].default_value = (0.82, 0.83, 0.85, 1)
    world.node_tree.nodes["Background"].inputs[1].default_value = 0.6
    scn.world = world
    ld = bpy.data.lights.new("k", "AREA")
    ld.energy, ld.size = 300, 3
    lo = bpy.data.objects.new("k", ld)
    scn.collection.objects.link(lo)
    lo.location = (2, -2.5, 3)
    lo.rotation_euler = (Vector((0, 0, 0.5)) - lo.location).to_track_quat("-Z", "Y").to_euler()
    zs = [v.co.z for v in ob.data.vertices]
    h = max(zs)
    cam_d = bpy.data.cameras.new("c")
    cam = bpy.data.objects.new("c", cam_d)
    scn.collection.objects.link(cam)
    scn.camera = cam
    c = Vector((0, 0, h * 0.5))
    cam.location = c + Vector((0.8, -1.2, 0.6)).normalized() * h * 2.6
    cam.rotation_euler = (c - cam.location).to_track_quat("-Z", "Y").to_euler()
    scn.render.filepath = path
    bpy.ops.render.render(write_still=True)
    for o in (lo, cam):
        bpy.data.objects.remove(o)


for o in list(bpy.data.objects):
    bpy.data.objects.remove(o)

greens = [mat("leaf_a", "#5f9b62", 0.55, 0.2), mat("leaf_b", "#4f8a57", 0.55, 0.2), mat("leaf_c", "#74ad6c", 0.55, 0.2)]
soil = mat("soil", "#5a4636", 0.95)
trunk = mat("trunk", "#8a6a4b", 0.8)

# tall: a fiddle-leaf-fig-like plant in a white pot
random.seed(7)
pot(0.16, 0.13, 0.32, mat("pot_white", "#f1efea", 0.45), soil)
stem((0, 0, 0.29), (0.02, 0.01, 0.95), 0.016, trunk)
for i in range(30):
    t = i / 25
    z = 0.45 + t * 0.5
    ang = i * 2.4
    r = 0.05 + 0.14 * (1 - abs(t - 0.4) * 1.6)
    base = Vector((math.cos(ang) * r + 0.01, math.sin(ang) * r, z))
    stem((0.01 + 0.02 * t, 0.01 * t, z - 0.06), base, 0.005, trunk)
    dirv = Vector((math.cos(ang), math.sin(ang), 0.45 + 0.7 * t))
    leaf(base, dirv, 0.2 + 0.08 * random.random(), 0.13 + 0.04 * random.random(), random.choice(greens), curl=-0.18)
export("plant-tall")

# small: a snake plant in a terracotta pot
random.seed(3)
pot(0.1, 0.08, 0.16, mat("pot_clay", "#c98b63", 0.7), soil)
for i in range(9):
    ang = i * 2.2 + random.random() * 0.3
    lean = 0.12 + random.random() * 0.15
    base = Vector((math.cos(ang) * 0.03, math.sin(ang) * 0.03, 0.14))
    leaf(base, (math.cos(ang) * lean, math.sin(ang) * lean, 1), 0.28 + random.random() * 0.08, 0.05, random.choice(greens), curl=0.05)
export("plant-small")
