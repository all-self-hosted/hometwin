// The robot vacuum's route: lanes across each room it cleans, through the doors between them,
// and the way back to its dock.
import type { Device, Home, Item, P } from '@/api';
import { along, fixtureRect, inside, itemRect, roomAt } from '@/geom';

const LANE = 0.42;
const BODY = 0.2; // half the robot plus a little

function free(h: Home, room: string, blocks: P[][], p: P) {
  const r = h.rooms.find((x) => x.id === room)!;
  for (const [dx, dz] of [[0, 0], [BODY, 0], [-BODY, 0], [0, BODY], [0, -BODY]]) {
    const q: P = [p[0] + dx, p[1] + dz];
    if (!inside(r.poly, q)) return false;
    if (blocks.some((b) => inside(b, q))) return false;
  }
  return true;
}

function lanes(h: Home, room: string, blocks: P[][]): P[] {
  const r = h.rooms.find((x) => x.id === room);
  if (!r) return [];
  const xs = r.poly.map((p) => p[0]), zs = r.poly.map((p) => p[1]);
  const x0 = Math.min(...xs) + 0.2, x1 = Math.max(...xs) - 0.2;
  const z0 = Math.min(...zs) + 0.25, z1 = Math.max(...zs) - 0.25;
  const out: P[] = [];
  let flip = false;
  for (let z = z0; z <= z1 + 1e-6; z += LANE) {
    // the longest free run across this row
    let best: [number, number] | null = null, start: number | null = null;
    for (let x = x0; x <= x1 + 0.05; x += 0.05) {
      const ok = x <= x1 && free(h, room, blocks, [x, z]);
      if (ok && start === null) start = x;
      if ((!ok || x > x1) && start !== null) {
        if (!best || x - 0.05 - start > best[1] - best[0]) best = [start, x - 0.05];
        start = null;
      }
    }
    if (!best || best[1] - best[0] < 0.3) continue;
    const row: P[] = [[best[0], z], [best[1], z]];
    out.push(...(flip ? row.reverse() : row));
    flip = !flip;
  }
  return out;
}

/** The two points either side of a door, the one in `room` first. */
function doorway(h: Home, doorId: string, room: string): [P, P] | null {
  const d = h.doors.find((x) => x.id === doorId);
  const w = d && h.walls.find((x) => x.id === d.wall);
  if (!d || !w) return null;
  const a = along(w, d.at, 0.45), b = along(w, d.at, -0.45);
  return roomAt(h, a)?.id === room ? [a, b] : [b, a];
}

export interface Route { clean: P[]; back: P[] }

export function vacuumRoute(h: Home, items: Item[], dock: Device): Route {
  const blocks = [
    ...items.filter((it) => it.product.type !== 'rug' && it.product.type !== 'bed' && it.product.h > 12).map((it) => itemRect(it)),
    ...(h.fixtures ?? []).map(fixtureRect),
  ];
  const r = ((dock.rot ?? 0) * Math.PI) / 180;
  const start: P = [dock.x + Math.sin(r) * 0.35, dock.z + Math.cos(r) * 0.35];
  const clean: P[] = [[dock.x, dock.z], start];
  clean.push(...lanes(h, 'living', blocks));
  for (const room of ['master', 'second']) {
    const dw = doorway(h, room, room);
    if (!dw) continue;
    clean.push(dw[1], dw[0]);
    clean.push(...lanes(h, room, blocks));
    clean.push(dw[0], dw[1]);
  }
  const back: P[] = [clean[clean.length - 1], [6.0, 4.0], start, [dock.x, dock.z]];
  return { clean, back };
}

export function pathLength(path: P[]) {
  let l = 0;
  for (let i = 1; i < path.length; i++) l += Math.hypot(path[i][0] - path[i - 1][0], path[i][1] - path[i - 1][1]);
  return l;
}

/** The point a fraction f along a path, and the heading there (radians, three.js y). */
export function pointAt(path: P[], f: number): { p: P; yaw: number } {
  const total = pathLength(path);
  let want = Math.max(0, Math.min(1, f)) * total;
  for (let i = 1; i < path.length; i++) {
    const a = path[i - 1], b = path[i];
    const l = Math.hypot(b[0] - a[0], b[1] - a[1]);
    if (want <= l || i === path.length - 1) {
      const k = l > 0 ? Math.min(1, want / l) : 0;
      return { p: [a[0] + (b[0] - a[0]) * k, a[1] + (b[1] - a[1]) * k], yaw: Math.atan2(b[0] - a[0], b[1] - a[1]) };
    }
    want -= l;
  }
  return { p: path[0] ?? [0, 0], yaw: 0 };
}
