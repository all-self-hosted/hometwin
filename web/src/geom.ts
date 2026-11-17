// Floor-plane geometry: x east, z south, metres. Shared by the shell, placement and labels.
import type { Door, Fixture, Home, Item, P, Wall } from './api';

export const len = (w: Wall) => Math.hypot(w.b[0] - w.a[0], w.b[1] - w.a[1]);
export const dir = (w: Wall): P => { const l = len(w); return [(w.b[0] - w.a[0]) / l, (w.b[1] - w.a[1]) / l]; };
export const normal = (w: Wall): P => { const d = dir(w); return [-d[1], d[0]]; };
export const along = (w: Wall, t: number, o = 0): P => {
  const d = dir(w), n = normal(w);
  return [w.a[0] + d[0] * t + n[0] * o, w.a[1] + d[1] * t + n[1] * o];
};
/** three.js rotation.y of an object whose local +x runs along the wall */
export const wallAngle = (w: Wall) => -Math.atan2(w.b[1] - w.a[1], w.b[0] - w.a[0]);

/** A local offset turned the way three.js turns an object about +y by rot degrees. */
export function rotate(lx: number, lz: number, rotDeg: number): P {
  const r = (rotDeg * Math.PI) / 180, c = Math.cos(r), s = Math.sin(r);
  return [lx * c + lz * s, -lx * s + lz * c];
}

export function rect(x: number, z: number, w: number, d: number, rot: number): P[] {
  return [[-1, -1], [1, -1], [1, 1], [-1, 1]].map(([a, b]) => {
    const o = rotate((a * w) / 2, (b * d) / 2, rot);
    return [x + o[0], z + o[1]] as P;
  });
}

export const itemRect = (it: Item, x = it.x, z = it.z, rot = it.rot) => rect(x, z, it.product.w / 100, it.product.d / 100, rot);
export const fixtureRect = (f: Fixture) => rect(f.x, f.z, f.w, f.d, f.rot);

export function inside(poly: P[], p: P) {
  let ins = false;
  for (let i = 0, j = poly.length - 1; i < poly.length; j = i++) {
    const a = poly[i], b = poly[j];
    if ((a[1] > p[1]) !== (b[1] > p[1]) && p[0] < ((b[0] - a[0]) * (p[1] - a[1])) / (b[1] - a[1]) + a[0]) ins = !ins;
  }
  return ins;
}

export const roomAt = (h: Home, p: P) => h.rooms.find((r) => inside(r.poly, p));

/** How high the floor is at p above the main floor: the highest platform or step there,
 * else the room's own floor. */
export function floorAt(h: Home, p: P): number {
  let y: number | null = null;
  for (const l of h.levels ?? []) if (inside(l.poly, p) && (y === null || l.y > y)) y = l.y;
  if (y !== null) return y;
  for (const r of h.rooms) if (inside(r.poly, p)) return r.y ?? 0;
  return 0;
}

export function area(poly: P[]) {
  let s = 0;
  for (let i = 0; i < poly.length; i++) {
    const a = poly[i], b = poly[(i + 1) % poly.length];
    s += a[0] * b[1] - b[0] * a[1];
  }
  return Math.abs(s) / 2;
}

export function centroid(poly: P[]): P {
  // area-weighted, so L-shaped rooms get a centre inside them
  let cx = 0, cz = 0, a = 0;
  for (let i = 0; i < poly.length; i++) {
    const p = poly[i], q = poly[(i + 1) % poly.length];
    const f = p[0] * q[1] - q[0] * p[1];
    a += f;
    cx += (p[0] + q[0]) * f;
    cz += (p[1] + q[1]) * f;
  }
  return [cx / (3 * a), cz / (3 * a)];
}

export interface Piece { wall: Wall; t0: number; t1: number }

/** Walls split around their door openings: what stands. */
export function pieces(h: Home): Piece[] {
  const out: Piece[] = [];
  for (const w of h.walls) {
    const cuts = h.doors.filter((d) => d.wall === w.id).map((d) => [d.at - d.width / 2, d.at + d.width / 2]).sort((a, b) => a[0] - b[0]);
    let t = 0;
    for (const c of [...cuts, [len(w), len(w)]]) {
      if (c[0] - t > 0.005) out.push({ wall: w, t0: t, t1: c[0] });
      t = c[1];
    }
  }
  return out;
}

export const piecePoly = (p: Piece): P[] => [
  along(p.wall, p.t0, -p.wall.thick / 2), along(p.wall, p.t1, -p.wall.thick / 2),
  along(p.wall, p.t1, p.wall.thick / 2), along(p.wall, p.t0, p.wall.thick / 2),
];

function raySeg(o: P, d: P, a: P, b: P): number {
  const e: P = [b[0] - a[0], b[1] - a[1]];
  const den = d[0] * e[1] - d[1] * e[0];
  if (Math.abs(den) < 1e-9) return Infinity;
  const f: P = [a[0] - o[0], a[1] - o[1]];
  const t = (f[0] * e[1] - f[1] * e[0]) / den;
  const u = (f[0] * d[1] - f[1] * d[0]) / den;
  return t > 1e-6 && u >= 0 && u <= 1 ? t : Infinity;
}

/** How far a ray goes before it meets a polygon among polys. */
export function cast(o: P, d: P, polys: P[][]): number {
  let best = Infinity;
  for (const poly of polys) for (let i = 0; i < poly.length; i++) best = Math.min(best, raySeg(o, d, poly[i], poly[(i + 1) % poly.length]));
  return best;
}

export interface Gap { from: P; to: P; m: number }

/** The clear distance from each side of an item to the nearest wall or thing in front of it. */
export function gaps(it: Item, obstacles: P[][], x = it.x, z = it.z, rot = it.rot): Gap[] {
  const w = it.product.w / 100, d = it.product.d / 100;
  const sides: [number, number, number, number][] = [[0, -d / 2, 0, -1], [0, d / 2, 0, 1], [-w / 2, 0, -1, 0], [w / 2, 0, 1, 0]];
  const out: Gap[] = [];
  for (const [lx, lz, nx, nz] of sides) {
    const o = rotate(lx, lz, rot), n = rotate(nx, nz, rot);
    const from: P = [x + o[0], z + o[1]];
    const m = cast(from, n, obstacles);
    if (m < 3.5) out.push({ from, to: [from[0] + n[0] * m, from[1] + n[1] * m], m });
  }
  return out;
}

/** The fan a swing door's leaf sweeps, from its hinge. */
export function swingArc(h: Home, d: Door): P[] | null {
  const w = h.walls.find((x) => x.id === d.wall);
  if (!w || d.kind !== 'swing') return null;
  const t0 = d.at - d.width / 2, t1 = d.at + d.width / 2;
  const [hinge, free] = d.hinge === 'b' ? [t1, t0] : [t0, t1];
  const s = d.side || 1;
  const n = normal(w).map((v) => v * s) as P;
  const hp = along(w, hinge, (w.thick / 2) * s);
  const fp = along(w, free, (w.thick / 2) * s);
  const u: P = [(fp[0] - hp[0]) / d.width, (fp[1] - hp[1]) / d.width];
  const out: P[] = [hp];
  for (let k = 0; k <= 12; k++) {
    const a = (k / 12) * (Math.PI / 2);
    out.push([hp[0] + d.width * (u[0] * Math.cos(a) + n[0] * Math.sin(a)), hp[1] + d.width * (u[1] * Math.cos(a) + n[1] * Math.sin(a))]);
  }
  return out;
}

const SNAP = 0.1;

/**
 * Pulls an item being dragged flush against nearby wall faces and the edges of other things,
 * when it stands square (a multiple of 90°). Returns the snapped centre and the lines it
 * snapped to, for drawing.
 */
export function snap(it: Item, x: number, z: number, rot: number, walls: Piece[], others: P[][]): { x: number; z: number; hit: boolean } {
  const q = ((Math.round(rot) % 360) + 360) % 360;
  if (q % 90 !== 0) return { x, z, hit: false };
  const turned = q === 90 || q === 270;
  const hx = (turned ? it.product.d : it.product.w) / 200;
  const hz = (turned ? it.product.w : it.product.d) / 200;
  // candidate lines: x = const (with z span) and z = const (with x span)
  const xs: [number, number, number][] = [], zs: [number, number, number][] = [];
  for (const p of walls) {
    const w = p.wall;
    const dx = w.b[0] - w.a[0], dz = w.b[1] - w.a[1];
    const a = along(w, p.t0), b = along(w, p.t1);
    if (Math.abs(dx) < 1e-6) {
      const lo = Math.min(a[1], b[1]), hi = Math.max(a[1], b[1]);
      xs.push([w.a[0] - w.thick / 2, lo, hi], [w.a[0] + w.thick / 2, lo, hi]);
    } else if (Math.abs(dz) < 1e-6) {
      const lo = Math.min(a[0], b[0]), hi = Math.max(a[0], b[0]);
      zs.push([w.a[1] - w.thick / 2, lo, hi], [w.a[1] + w.thick / 2, lo, hi]);
    }
  }
  for (const poly of others) {
    const minx = Math.min(...poly.map((p) => p[0])), maxx = Math.max(...poly.map((p) => p[0]));
    const minz = Math.min(...poly.map((p) => p[1])), maxz = Math.max(...poly.map((p) => p[1]));
    xs.push([minx, minz - 0.6, maxz + 0.6], [maxx, minz - 0.6, maxz + 0.6]);
    zs.push([minz, minx - 0.6, maxx + 0.6], [maxz, minx - 0.6, maxx + 0.6]);
  }
  let bx = x, bz = z, dxBest = SNAP, dzBest = SNAP, hit = false;
  for (const [lx, lo, hi] of xs) {
    if (z + hz < lo || z - hz > hi) continue;
    for (const edge of [-hx, hx]) {
      const d = Math.abs(x + edge - lx);
      if (d < dxBest) { dxBest = d; bx = lx - edge; hit = true; }
    }
  }
  for (const [lz, lo, hi] of zs) {
    if (x + hx < lo || x - hx > hi) continue;
    for (const edge of [-hz, hz]) {
      const d = Math.abs(z + edge - lz);
      if (d < dzBest) { dzBest = d; bz = lz - edge; hit = true; }
    }
  }
  return { x: bx, z: bz, hit };
}
