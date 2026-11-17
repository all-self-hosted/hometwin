// The home's fixed parts as one scene graph: floors, cutaway walls with their openings, doors,
// windows, the fixtures that come with the home, and the plot it sits on.
import * as THREE from 'three';
import { mergeGeometries } from 'three/addons/utils/BufferGeometryUtils.js';
import type { Door, Fixture, Home, Wall } from '@/api';
import { along, dir, floorAt, inside, len, normal, wallAngle } from '@/geom';
import { M, TEX, TEX_SIZE, blk, cyl, cylGeo, edgeAOTex, mesh } from './kit';

const FLOOR: Record<string, keyof typeof TEX> = { living: 'oak', bedroom: 'oakLight', entry: 'tileWarm', kitchen: 'tile', bath: 'tileSmall', balcony: 'tileWarm' };

// the edge of a platform or a step: dark timber nosing
const RISER = new THREE.MeshStandardMaterial({ color: '#4a3326', roughness: 0.55, metalness: 0 });

// a finish named outright wins over the one the room's kind picks
const FINISH: Record<string, keyof typeof TEX> = { wood: 'oak', tile: 'tile' };

const floorMats = new Map<string, THREE.MeshStandardMaterial>();
function floorMat(kind: string, finish?: string) {
  const key = (finish && FINISH[finish]) || FLOOR[kind] || 'oak';
  let m = floorMats.get(key);
  if (!m) {
    const t = TEX[key].clone();
    t.needsUpdate = true;
    const [sx, sz] = TEX_SIZE[key];
    t.repeat.set(1 / sx, 1 / sz);
    m = new THREE.MeshStandardMaterial({ map: t, roughness: key.startsWith('oak') ? 0.62 : 0.4, metalness: 0 });
    floorMats.set(key, m);
  }
  return m;
}

/** Shape from a floor polygon; ShapeGeometry is in xy, turned flat so y becomes -z → +z. */
function shapeOf(poly: [number, number][]) {
  const s = new THREE.Shape();
  poly.forEach(([x, z], i) => (i ? s.lineTo(x, -z) : s.moveTo(x, -z)));
  s.closePath();
  return s;
}

/** Collects boxes per material and merges them into one mesh each. */
class Batch {
  private parts = new Map<THREE.Material, THREE.BufferGeometry[]>();
  box(mat: THREE.Material, w: number, h: number, d: number, m4: THREE.Matrix4) {
    const g = new THREE.BoxGeometry(w, h, d).applyMatrix4(m4);
    if (!this.parts.has(mat)) this.parts.set(mat, []);
    this.parts.get(mat)!.push(g);
  }
  geo(mat: THREE.Material, g: THREE.BufferGeometry) {
    if (!this.parts.has(mat)) this.parts.set(mat, []);
    this.parts.get(mat)!.push(g.index ? g.toNonIndexed() : g);
  }
  build(parent: THREE.Object3D, shadow = true) {
    for (const [mat, gs] of this.parts) {
      const norm = gs.map((g) => (g.index ? g.toNonIndexed() : g));
      for (const g of norm) for (const k of Object.keys(g.attributes)) if (!['position', 'normal', 'uv'].includes(k)) g.deleteAttribute(k);
      const merged = mergeGeometries(norm, false);
      if (!merged) continue;
      mesh(parent, merged, mat, 0, 0, 0, shadow);
    }
  }
}

function wallFrame(w: Wall, t: number, y: number, off = 0): THREE.Matrix4 {
  const p = along(w, t), n = normal(w);
  return new THREE.Matrix4().makeRotationY(wallAngle(w)).setPosition(p[0] + n[0] * off, y, p[1] + n[1] * off);
}

type Batches = { solid: Batch; caps: Batch; ghosts: Batch; frames: Batch; glass: Batch };

/** A bay window standing out of the wall between t0 and t1: a slab under the sill with a board
 *  on top, glass on three sides up to the head and a roof, all cut at the cut height. */
function bayWindow(h: Home, w: Wall, t0: number, t1: number, sill: number, head: number, depth: number, top: number, b: Batches) {
  // it stands out on the side away from the rooms
  const p = along(w, (t0 + t1) / 2), n = normal(w), probe = w.thick / 2 + 0.3;
  const s = h.rooms.some((r) => inside(r.poly, [p[0] + n[0] * probe, p[1] + n[1] * probe])) ? -1 : 1;
  const d0 = w.thick / 2, mid = (t0 + t1) / 2, W = t1 - t0 + 0.12;
  const at = (d: number) => s * (d0 + d);
  // a piece from y0 to y1: solid below the cut, see-through above it
  const piece = (below: [Batch, THREE.Material], above: [Batch, THREE.Material], t: number, x: number, z: number, d: number, y0: number, y1: number) => {
    const c = Math.min(y1, top);
    if (c - y0 > 0.002) below[0].box(below[1], x, c - y0, z, wallFrame(w, t, (y0 + c) / 2, at(d)));
    const a = Math.max(y0, top);
    if (y1 - a > 0.002) above[0].box(above[1], x, y1 - a, z, wallFrame(w, t, (a + y1) / 2, at(d)));
  };
  // it hangs off the wall: a slab under the sill, the wall below stays flush
  piece([b.solid, M.wall], [b.ghosts, M.ghost], mid, W, depth, depth / 2, sill - 0.15, sill);
  if (sill < top) {
    b.frames.box(M.frame, W + 0.02, 0.04, depth + w.thick + 0.04, wallFrame(w, mid, sill + 0.02, at(depth / 2 - d0)));
  }
  const g0 = sill + 0.04;
  piece([b.glass, M.glass], [b.ghosts, M.glass], mid, W - 0.08, 0.012, depth - 0.03, g0, head);
  for (const t of [t0 - 0.03, t1 + 0.03]) {
    piece([b.glass, M.glass], [b.ghosts, M.glass], t, 0.012, depth - 0.06, depth / 2, g0, head);
    piece([b.frames, M.frame], [b.ghosts, M.ghost], t, 0.05, 0.05, depth - 0.03, g0, head);
  }
  piece([b.frames, M.frame], [b.ghosts, M.ghost], mid, 0.04, 0.05, depth - 0.03, g0, head);
  piece([b.solid, M.wall], [b.ghosts, M.ghost], mid, W, depth, depth / 2, head, head + 0.1);
}

export interface Shell {
  root: THREE.Group;
  /** floors, walls, doors and fixtures: what the scanned model stands in for when it is shown */
  structure: THREE.Group;
  floors: THREE.Mesh[];
  ghost: THREE.Object3D;
  /** door id → the leaf's pivot, to swing it */
  leaves: Map<string, THREE.Object3D>;
  box: { x0: number; x1: number; z0: number; z1: number };
}

export function buildShell(h: Home): Shell {
  const root = new THREE.Group();
  root.name = 'shell';
  const cut = h.cutHeight;
  const full = h.wallHeight;
  const xs = h.walls.flatMap((w) => [w.a[0], w.b[0]]), zs = h.walls.flatMap((w) => [w.a[1], w.b[1]]);
  const box = { x0: Math.min(...xs), x1: Math.max(...xs), z0: Math.min(...zs), z1: Math.max(...zs) };
  const cx = (box.x0 + box.x1) / 2, cz = (box.z0 + box.z1) / 2;

  // the plot: lawn, the building slab the flat sits on, the landing in front of the door
  const lawn = new THREE.Mesh(new THREE.CircleGeometry(70, 64), new THREE.MeshStandardMaterial({ map: (() => { const t = TEX.grass.clone(); t.needsUpdate = true; t.repeat.set(22, 22); return t; })(), roughness: 1 }));
  lawn.rotation.x = -Math.PI / 2;
  lawn.position.set(cx, -0.16, cz);
  lawn.receiveShadow = true;
  root.add(lawn);
  blk(root, M.plinth, box.x1 - box.x0 + 1.4, 0.16, box.z1 - box.z0 + 1.4, cx, -0.16, cz, 0.05);
  const entry = h.doors.find((d) => d.entry);
  if (entry) {
    const w = h.walls.find((x) => x.id === entry.wall)!;
    const p = along(w, entry.at, -(w.thick / 2 + 1.1) * (entry.side || 1));
    blk(root, M.path, 2.2, 0.02, 2.0, p[0], 0, p[1], 0.01);
    // the corridor's path off to the lawn
    blk(root, M.path, 1.4, 0.02, 3.0, p[0], -0.14, p[1] - 2.4, 0.01);
  }

  const ground = new Set(root.children);

  // floors
  const floors: THREE.Mesh[] = [];
  for (const r of h.rooms) {
    const y = r.y ?? 0;
    let m: THREE.Mesh;
    if (y > 0) {
      // a raised room: a slab up to its floor, edged like the platforms
      const g = new THREE.ExtrudeGeometry(shapeOf(r.poly), { depth: y, bevelEnabled: false });
      g.rotateX(-Math.PI / 2);
      m = mesh(root, g, [floorMat(r.kind, r.floor), M.wall], 0, 0.002, 0, false);
    } else {
      const g = new THREE.ShapeGeometry(shapeOf(r.poly));
      g.rotateX(-Math.PI / 2);
      m = mesh(root, g, floorMat(r.kind, r.floor), 0, y + 0.002, 0, false);
    }
    m.userData.room = r.id;
    m.name = `floor:${r.id}`;
    floors.push(m);
  }

  // platforms and their steps: slabs up to their height, lowest first
  for (const l of [...(h.levels ?? [])].sort((a, b) => a.y - b.y)) {
    if (l.y <= 0) continue;
    const room = h.rooms.find((r) => r.id === l.room);
    const g = new THREE.ExtrudeGeometry(shapeOf(l.poly), { depth: l.y, bevelEnabled: false });
    g.rotateX(-Math.PI / 2);
    // ExtrudeGeometry's groups: 0 the caps, 1 the sides
    // the risers are edged dark, like the nosing on a real step
    const m = mesh(root, g, [floorMat(room?.kind ?? 'living', l.floor ?? room?.floor), RISER], 0, 0.002, 0, false);
    m.receiveShadow = true;
    m.userData.room = l.room;
    m.name = `level:${l.id}`;
    floors.push(m);
  }

  // walls, cut at the cut height, with their openings
  const solid = new Batch(), caps = new Batch(), ghosts = new Batch(), frames = new Batch();
  const glass = new Batch();
  const b2 = (b: Batch, mat: THREE.Material, t0: number, t1: number, y0: number, y1: number, thick: number) => {
    if (t1 - t0 < 0.002 || y1 - y0 < 0.002) return;
    b.box(mat, t1 - t0, y1 - y0, thick, wallFrame(curWall!, (t0 + t1) / 2, (y0 + y1) / 2));
  };
  let curWall: Wall | null = null;
  for (const w of h.walls) {
    curWall = w;
    const L = len(w);
    const wh = Math.min(w.h ?? full, full);
    const top = Math.min(wh, cut);
    const ext = w.thick / 2;
    if (w.railing || w.glazed) {
      // an open balustrade on a curb, and glass above it when glazed; glass alone on the curb
      // when there is no railing
      const b = baseAt(h, w, L / 2), r = w.railing ? b + Math.min(w.railing, wh) : b + 0.08;
      b2(solid, M.wall, -ext, L + ext, b, b + 0.08, w.thick);
      if (w.railing) {
        for (let t = 0.07; t < L - 0.03; t += 0.12) frames.box(M.frame, 0.022, r - b - 0.08, 0.022, wallFrame(w, t, b + 0.08 + (r - b - 0.08) / 2));
        frames.box(M.frame, L, 0.05, w.thick * 0.5, wallFrame(w, L / 2, r - 0.025));
      } else {
        frames.box(M.frame, L, 0.04, 0.06, wallFrame(w, L / 2, r + 0.02));
      }
      if (w.glazed && r < wh) {
        const below = Math.min(cut, wh);
        if (r < below) glass.box(M.glass, L, below - r, 0.012, wallFrame(w, L / 2, (r + below) / 2));
        if (wh > Math.max(r, cut)) ghosts.box(M.glass, L, wh - Math.max(r, cut), 0.012, wallFrame(w, L / 2, (Math.max(r, cut) + wh) / 2));
        const n = Math.max(1, Math.round(L / 1.1));
        for (let i = 1; i < n; i++) {
          const t = (L * i) / n;
          if (r < below) frames.box(M.frame, 0.04, below - r, 0.05, wallFrame(w, t, (r + below) / 2));
          if (wh > Math.max(r, cut)) ghosts.box(M.ghost, 0.04, wh - Math.max(r, cut), 0.05, wallFrame(w, t, (Math.max(r, cut) + wh) / 2));
        }
      }
      continue;
    }
    type Op = { t0: number; t1: number; kind: 'door' | 'window'; sill: number; head: number; bay?: number };
    // openings stand on the higher of the floors either side (a raised room, a platform)
    const ops: Op[] = [
      ...h.doors.filter((d) => d.wall === w.id).map((d) => { const b = baseAt(h, w, d.at); return { t0: d.at - d.width / 2, t1: d.at + d.width / 2, kind: 'door' as const, sill: b, head: b + (d.height ?? 2.1) }; }),
      ...h.windows.filter((x) => x.wall === w.id).map((x) => { const b = baseAt(h, w, x.at); return { t0: x.at - x.width / 2, t1: x.at + x.width / 2, kind: 'window' as const, sill: b + x.sill, head: b + x.sill + x.height, bay: x.bay }; }),
    ].sort((a, b) => a.t0 - b.t0);
    const seg = (t0: number, t1: number, y0: number, y1: number, b: Batch, mat: THREE.Material) => {
      if (t1 - t0 < 0.002 || y1 - y0 < 0.002) return;
      b.box(mat, t1 - t0, y1 - y0, w.thick, wallFrame(w, (t0 + t1) / 2, (y0 + y1) / 2));
    };
    let t = -ext;
    for (const o of [...ops, { t0: L + ext, t1: L + ext, kind: 'door' as const, sill: 0, head: 0 }]) {
      seg(t, o.t0, 0, top, solid, M.wall);
      seg(t, o.t0, top, top + 0.012, caps, M.wallCap);
      seg(t, o.t0, cut, wh, ghosts, M.ghost);
      if (o.t1 > o.t0) {
        const mid = (o.t0 + o.t1) / 2, width = o.t1 - o.t0;
        if (o.kind === 'window') {
          // the wall under the sill, and the glass up to the cut
          const s = Math.min(o.sill, top);
          seg(o.t0, o.t1, 0, s, solid, M.wall);
          seg(o.t0, o.t1, s, s + 0.012, caps, M.wallCap);
          if (o.bay) {
            bayWindow(h, w, o.t0, o.t1, o.sill, o.head, o.bay, top, { solid, caps, ghosts, frames, glass });
          } else if (o.sill < top) {
            const gTop = Math.min(o.head, top);
            glass.box(M.glass, width - 0.06, gTop - o.sill, 0.012, wallFrame(w, mid, (o.sill + gTop) / 2));
            // frame: sill board, jambs and the mullion
            frames.box(M.frame, width + 0.04, 0.035, w.thick + 0.05, wallFrame(w, mid, o.sill + 0.017));
            for (const j of [o.t0 + 0.025, o.t1 - 0.025]) frames.box(M.frame, 0.05, gTop - o.sill, w.thick * 0.55, wallFrame(w, j, (o.sill + gTop) / 2));
            if (width > 1.3) frames.box(M.frame, 0.04, gTop - o.sill, 0.06, wallFrame(w, mid, (o.sill + gTop) / 2));
            frames.box(M.frame, width, 0.03, w.thick * 0.55, wallFrame(w, mid, gTop - 0.015));
          }
          if (o.head < wh) seg(o.t0, o.t1, Math.max(o.head, cut), wh, ghosts, M.ghost);
        } else {
          // door: the step up to a raised floor, frame jambs to the cut, a threshold strip
          seg(o.t0, o.t1, 0, o.sill, solid, M.wall);
          for (const j of [o.t0 + 0.02, o.t1 - 0.02]) frames.box(M.frame, 0.04, top - o.sill, w.thick + 0.03, wallFrame(w, j, (top + o.sill) / 2));
          frames.box(M.frame, width, 0.008, w.thick, wallFrame(w, mid, o.sill + 0.004));
          if (o.head < wh) seg(o.t0, o.t1, Math.max(o.head, cut), wh, ghosts, M.ghost);
        }
      }
      t = o.t1;
    }
  }
  solid.build(root);
  // soft shading where walls meet the floor, on both faces of every standing piece
  const aoGeos: THREE.BufferGeometry[] = [];
  const AO_W = 0.38;
  for (const w of h.walls) {
    if (w.railing || w.glazed) continue;
    const L = len(w);
    const cuts = h.doors.filter((d) => d.wall === w.id).map((d) => [d.at - d.width / 2, d.at + d.width / 2]).sort((a, b) => a[0] - b[0]);
    let t = 0;
    for (const c of [...cuts, [L, L]]) {
      if (c[0] - t > 0.05) {
        for (const side of [-1, 1]) {
          const g = new THREE.PlaneGeometry(c[0] - t, AO_W);
          // v=0 (dark) at the wall face, fading away from it
          g.rotateX(-Math.PI / 2);
          if (side < 0) g.rotateY(Math.PI);
          g.translate(0, 0, 0);
          const mid = (t + c[0]) / 2;
          const p0 = along(w, mid, side * (w.thick / 2 + AO_W / 2));
          g.applyMatrix4(new THREE.Matrix4().makeRotationY(wallAngle(w)).setPosition(p0[0], 0.004, p0[1]));
          aoGeos.push(g);
        }
      }
      t = c[1];
    }
  }
  if (aoGeos.length) {
    const ao = new THREE.Mesh(mergeGeometries(aoGeos, false)!, new THREE.MeshBasicMaterial({ map: edgeAOTex, transparent: true, depthWrite: false }));
    ao.renderOrder = 1;
    root.add(ao);
  }
  caps.build(root, false);
  frames.build(root);
  glass.build(root, false);
  const ghost = new THREE.Group();
  ghost.name = 'ghost';
  ghosts.build(ghost, false);
  ghost.traverse((o) => { o.renderOrder = 5; (o as THREE.Mesh).castShadow = false; (o as THREE.Mesh).receiveShadow = false; });
  // the walls' outline at ceiling height, so what hangs up there has something to hang from
  const pts: THREE.Vector3[] = [];
  for (const w of h.walls) {
    const wh = Math.min(w.h ?? full, full);
    if (wh <= cut + 0.01) continue;
    for (const o of [-w.thick / 2, w.thick / 2]) {
      const a = along(w, -w.thick / 2, o), b = along(w, len(w) + w.thick / 2, o);
      pts.push(new THREE.Vector3(a[0], wh, a[1]), new THREE.Vector3(b[0], wh, b[1]));
    }
  }
  const outline = new THREE.LineSegments(new THREE.BufferGeometry().setFromPoints(pts), M.ghostLine);
  ghost.add(outline);
  root.add(ghost);

  // doors
  const leaves = new Map<string, THREE.Object3D>();
  for (const d of h.doors) {
    const w = h.walls.find((x) => x.id === d.wall);
    if (!w) continue;
    const base = baseAt(h, w, d.at);
    const leaf = doorLeaf(w, d, cut - base);
    if (leaf) {
      leaf.position.y = base;
      root.add(leaf);
      leaves.set(d.id, leaf);
    }
  }

  // fixtures
  for (const f of h.fixtures ?? []) root.add(fixture(f));

  // all but the plot is the home's structure
  const structure = new THREE.Group();
  structure.name = 'structure';
  for (const o of [...root.children]) if (!ground.has(o)) structure.add(o);
  root.add(structure);
  trees(root, box);
  return { root, structure, floors, ghost, leaves, box };
}

/** The floor an opening stands on: the higher of the floors just either side of the wall. */
function baseAt(h: Home, w: Wall, t: number) {
  const o = w.thick / 2 + 0.2;
  return Math.max(0, floorAt(h, along(w, t, o)), floorAt(h, along(w, t, -o)));
}

const doorMats = new Map<string, THREE.MeshStandardMaterial>();
function doorMat(color: string) {
  let m = doorMats.get(color);
  if (!m) doorMats.set(color, (m = new THREE.MeshStandardMaterial({ color, roughness: 0.5 })));
  return m;
}

function doorLeaf(w: Wall, d: Door, cut: number): THREE.Object3D | null {
  // leaves are cut with the walls, so the rooms stay open to view
  const height = Math.min(d.height ?? 2.1, 2.05, cut);
  const n = normal(w), u = dir(w);
  if (d.kind === 'sliding') {
    // two glass panels in a light frame, one slid behind the other
    const g = new THREE.Group();
    const p = along(w, d.at);
    g.position.set(p[0], 0, p[1]);
    g.rotation.y = wallAngle(w);
    const pw = d.width / 2 + 0.03;
    for (const [i, off] of [[0, -d.width / 4], [1, -d.width / 4 + pw * 0.55]] as const) {
      const panel = new THREE.Group();
      panel.position.set(off, 0, (i ? 1 : -1) * 0.025);
      blk(panel, M.frameDark, pw, 0.04, 0.035, 0, 0, 0, 0.005);
      blk(panel, M.frameDark, pw, 0.04, 0.035, 0, height - 0.04, 0, 0.005);
      blk(panel, M.frameDark, 0.04, height, 0.035, -pw / 2 + 0.02, 0, 0, 0.005);
      blk(panel, M.frameDark, 0.04, height, 0.035, pw / 2 - 0.02, 0, 0, 0.005);
      mesh(panel, new THREE.BoxGeometry(pw - 0.08, height - 0.08, 0.01), M.glass, 0, height / 2, 0, false);
      g.add(panel);
    }
    g.userData.kind = 'sliding';
    return g;
  }
  if (d.kind !== 'swing') return null;
  const t0 = d.at - d.width / 2, t1 = d.at + d.width / 2;
  const hingeT = d.hinge === 'b' ? t1 : t0;
  const s = d.side || 1;
  const hp = along(w, hingeT, (w.thick / 2) * s);
  const pivot = new THREE.Group();
  pivot.position.set(hp[0], 0, hp[1]);
  // closed: the leaf runs from the hinge toward the other jamb, along the wall
  const toFree = d.hinge === 'b' ? -1 : 1;
  pivot.rotation.y = Math.atan2(-(u[1] * toFree), u[0] * toFree);
  const leafW = d.width - 0.04;
  const mat = d.color ? doorMat(d.color) : d.entry ? M.doorEntry : M.door;
  const panel = blk(pivot, mat, leafW, height, 0.04, leafW / 2 + 0.02, 0, 0, 0.008);
  panel.name = 'leaf';
  // handle on the far edge, both faces
  for (const f of [-1, 1]) blk(pivot, M.steel, 0.12, 0.022, 0.022, leafW - 0.08, Math.min(1.0, height - 0.12), f * 0.035, 0.008);
  blk(pivot, M.wallCap, leafW, 0.012, 0.042, leafW / 2 + 0.02, height, 0, 0.004);
  // open angle: into the room on side s; the sign depends on which way "free" runs
  const n3 = new THREE.Vector3(n[0] * s, 0, n[1] * s);
  const along3 = new THREE.Vector3(u[0] * toFree, 0, u[1] * toFree);
  const cross = along3.clone().cross(n3).y;
  pivot.userData.openSign = cross > 0 ? 1 : -1;
  pivot.userData.kind = 'swing';
  pivot.userData.entry = !!d.entry;
  pivot.userData.base = pivot.rotation.y;
  return pivot;
}

/** Sets how far a swing door stands open, 0 (shut) to 1 (90°). */
export function setDoorOpen(pivot: THREE.Object3D, k: number) {
  if (pivot.userData.kind !== 'swing') return;
  pivot.rotation.y = pivot.userData.base + pivot.userData.openSign * k * (Math.PI / 2);
}

function fixture(f: Fixture): THREE.Object3D {
  const g = new THREE.Group();
  g.position.set(f.x, 0, f.z);
  g.rotation.y = (f.rot * Math.PI) / 180;
  g.name = `fixture:${f.id}`;
  const { w, d, h } = f;
  switch (f.kind) {
    case 'counter': {
      blk(g, M.dark, w - 0.04, 0.1, d - 0.08, 0, 0, -0.03, 0.01);
      blk(g, M.satin, w, h - 0.14, d - 0.03, 0, 0.1, -0.015, 0.012);
      blk(g, M.worktop, w + 0.01, 0.04, d, 0, h - 0.04, 0, 0.008);
      const n = Math.max(1, Math.round(w / 0.6));
      for (let i = 0; i < n; i++) {
        const x = -w / 2 + (w / n) * (i + 0.5);
        blk(g, M.white, w / n - 0.012, h - 0.17, 0.02, x, 0.11, d / 2 - 0.02, 0.006);
        blk(g, M.steel, 0.14, 0.012, 0.015, x, h - 0.12, d / 2, 0.004);
      }
      // a hob or a sink on the worktop
      if (w > 1.5) {
        blk(g, M.ink, 0.58, 0.008, 0.5, w / 2 - 0.45, h, 0, 0.004);
        blk(g, M.steel, 0.5, 0.012, 0.4, -w / 2 + 0.45, h - 0.004, 0, 0.004);
      }
      break;
    }
    case 'fridge':
      blk(g, M.satin, w, h, d, 0, 0, 0, 0.03);
      blk(g, M.steel, 0.02, 0.5, 0.025, w / 2 - 0.06, h - 0.8, d / 2, 0.008);
      blk(g, M.steel, 0.02, 0.4, 0.025, w / 2 - 0.06, 0.6, d / 2, 0.008);
      blk(g, M.frame, w - 0.01, 0.006, 0.01, 0, h * 0.62, d / 2, 0.002);
      break;
    case 'shoe':
      blk(g, M.white, w, h - 0.12, d, 0, 0.12, 0, 0.012);
      blk(g, M.door, w - 0.02, 0.02, d, 0, h - 0.02, 0, 0.006);
      for (const x of [-w / 4, w / 4]) blk(g, M.steel, 0.012, 0.12, 0.015, x + 0.05, h * 0.55, d / 2, 0.004);
      break;
    case 'toilet': {
      blk(g, M.ceramic, 0.36, 0.38, 0.16, 0, 0.42, -d / 2 + 0.08, 0.04);
      const bowl = mesh(g, cylGeo(0.2, 0.15, 0.4, 28), M.ceramic, 0, 0.2, 0.08);
      bowl.scale.z = 1.3;
      const lid = mesh(g, cylGeo(0.2, 0.2, 0.03, 28), M.ceramic, 0, 0.415, 0.08);
      lid.scale.z = 1.3;
      break;
    }
    case 'basin':
      blk(g, M.door, w, 0.5, d, 0, 0.3, 0, 0.015);
      blk(g, M.ceramic, w + 0.02, 0.06, d + 0.01, 0, 0.79, 0, 0.02);
      blk(g, M.steel, 0.03, 0.14, 0.03, 0, 0.85, -d / 2 + 0.07, 0.008);
      break;
    case 'shower':
      blk(g, M.ceramic, w, 0.05, d, 0, 0, 0, 0.015);
      for (const [x, z, ww, dd] of [[w / 2 - 0.005, 0, 0.01, d], [0, -d / 2 + 0.005, w, 0.01]] as const) mesh(g, new THREE.BoxGeometry(ww, h - 0.05, dd), M.glass, x, 0.05 + (h - 0.05) / 2, z, false);
      cyl(g, M.steel, 0.012, 1.4, -w / 2 + 0.06, 0.5, -d / 2 + 0.06, 8);
      break;
    case 'washer':
      blk(g, M.white, w, h, d, 0, 0, 0, 0.03);
      mesh(g, cylGeo(0.17, 0.17, 0.03, 32), M.glass, 0, 0.42, d / 2 + 0.01, false).rotation.x = Math.PI / 2;
      mesh(g, new THREE.TorusGeometry(0.18, 0.02, 10, 32), M.steel, 0, 0.42, d / 2 + 0.01);
      break;
    default:
      blk(g, M.white, w, h, d, 0, 0, 0, 0.02);
  }
  g.traverse((o) => { if ((o as THREE.Mesh).isMesh) (o as THREE.Mesh).castShadow = true; });
  return g;
}

function trees(root: THREE.Object3D, box: { x0: number; x1: number; z0: number; z1: number }) {
  // a few round trees and bushes on the lawn, instanced
  const spots: [number, number, number][] = [
    [box.x0 - 3.6, box.z0 + 1.2, 1.15], [box.x0 - 3.0, box.z1 - 0.6, 0.95], [box.x1 + 3.4, box.z0 + 1.8, 1.05],
    [box.x1 + 3.2, box.z1 + 0.4, 0.9], [box.x0 + 0.6, box.z1 + 3.4, 0.85], [box.x1 - 0.6, box.z1 + 3.6, 1.0],
    [box.x0 - 5.0, box.z0 + 5.0, 0.8], [box.x1 + 5.0, box.z0 + 6.2, 0.85],
  ];
  const crown = new THREE.InstancedMesh(new THREE.IcosahedronGeometry(1, 2), M.leaf, spots.length * 2);
  const trunk = new THREE.InstancedMesh(cylGeo(0.07, 0.1, 1, 8), M.trunk, spots.length);
  const m = new THREE.Matrix4(), q = new THREE.Quaternion(), s = new THREE.Vector3(), p = new THREE.Vector3();
  spots.forEach(([x, z, k], i) => {
    trunk.setMatrixAt(i, m.compose(p.set(x, -0.15 + 0.6 * k, z), q.identity(), s.set(k, 1.2 * k, k)));
    crown.setMatrixAt(i * 2, m.compose(p.set(x, -0.15 + 1.75 * k, z), q.identity(), s.set(0.85 * k, 0.8 * k, 0.85 * k)));
    crown.setMatrixAt(i * 2 + 1, m.compose(p.set(x + 0.35 * k, -0.15 + 1.35 * k, z + 0.2 * k), q.identity(), s.set(0.55 * k, 0.5 * k, 0.55 * k)));
  });
  for (const im of [crown, trunk]) {
    im.castShadow = true;
    im.receiveShadow = true;
    root.add(im);
  }
  // bushes along the south of the plot
  const n = Math.round((box.x1 - box.x0 + 2) / 0.5);
  const bush = new THREE.InstancedMesh(new THREE.IcosahedronGeometry(0.27, 1), M.leafDark, n);
  for (let i = 0; i < n; i++) {
    const k = 0.75 + ((i * 37) % 10) / 25;
    bush.setMatrixAt(i, m.compose(p.set(box.x0 - 1 + i * 0.5, -0.16 + 0.16 * k, box.z1 + 1.25 + ((i * 13) % 5) * 0.04), q.identity(), s.set(k, 0.8 * k, k)));
  }
  bush.castShadow = true;
  bush.receiveShadow = true;
  root.add(bush);
}
