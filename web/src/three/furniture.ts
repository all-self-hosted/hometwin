// Furniture models built in the browser from a product's real size and colour: the fallback
// when the server has no Blender to generate a GLB. Origin at the floor centre, front facing +z.
import * as THREE from 'three';
import type { Product } from '@/api';
import { M, blk, cyl, mesh } from './kit';

const mats = new Map<string, THREE.MeshStandardMaterial>();
function mat(hex: string, rough: number, metal = 0) {
  const key = `${hex}:${rough}:${metal}`;
  let m = mats.get(key);
  if (!m) mats.set(key, (m = new THREE.MeshStandardMaterial({ color: hex, roughness: rough, metalness: metal })));
  return m;
}

const darker = (hex: string, k: number) => `#${new THREE.Color(hex).offsetHSL(0, -0.02, -k).getHexString()}`;
const lighter = (hex: string, k: number) => `#${new THREE.Color(hex).offsetHSL(0, 0, k).getHexString()}`;

function measure(p: Product, key: string): number | undefined {
  const m = p.measures?.find((x) => x.key === key);
  return m && m.cm > 0 ? m.cm / 100 : undefined;
}

function count(name: string, words: RegExp, fallback: number) {
  const m = name.match(words);
  return m ? Number(m[1]) : fallback;
}

/** A label sprite, for boxes of unknown kind. */
function labelSprite(text: string) {
  const c = document.createElement('canvas');
  c.width = 512;
  c.height = 128;
  const g = c.getContext('2d')!;
  g.fillStyle = 'rgba(255,255,255,0.9)';
  g.roundRect(4, 4, 504, 120, 28);
  g.fill();
  g.fillStyle = '#22252b';
  g.font = '600 52px "PingFang SC", sans-serif';
  g.textAlign = 'center';
  g.textBaseline = 'middle';
  g.fillText(text.slice(0, 14), 256, 66);
  const t = new THREE.CanvasTexture(c);
  t.colorSpace = THREE.SRGBColorSpace;
  const s = new THREE.Sprite(new THREE.SpriteMaterial({ map: t, depthTest: false }));
  s.scale.set(0.8, 0.2, 1);
  return s;
}

export function buildFurniture(p: Product): THREE.Group {
  const g = new THREE.Group();
  const W = p.w / 100, D = p.d / 100, H = p.h / 100;
  const col = p.colorHex || '#e9e7e1';
  const name = `${p.name} ${p.family ?? ''}`;
  const fabric = mat(col, 0.92);
  const body = mat(col, 0.5);
  const wood = mat(col, 0.62);
  const leg = mat(darker(col, 0.25), 0.5);
  const metal = M.steel;
  const r = Math.min(0.02, W / 10, D / 10, H / 10);

  switch (p.type) {
    case 'bookcase': {
      const t = 0.018;
      const plinth = 0.06;
      blk(g, body, W, H, t, 0, 0, -D / 2 + t / 2, 0.004); // back
      blk(g, body, t, H, D, -W / 2 + t / 2, 0, 0, 0.004);
      blk(g, body, t, H, D, W / 2 - t / 2, 0, 0, 0.004);
      blk(g, body, W, t, D, 0, H - t, 0, 0.004);
      blk(g, body, W - 2 * t, plinth, t, 0, 0, D / 2 - t, 0.003);
      blk(g, body, W - 2 * t, t, D - t, 0, plinth, t / 2, 0.003);
      const n = Math.max(2, Math.round((H - plinth) / 0.37));
      const gap = (H - plinth - t) / n;
      const books = [mat('#c9b79c', 0.8), mat('#7c8f86', 0.8), mat('#d8cfc0', 0.8), mat('#b9876a', 0.8), mat('#e8e2d8', 0.8)];
      for (let i = 1; i < n; i++) blk(g, body, W - 2 * t, t, D - t - 0.01, 0, plinth + i * gap, t / 2, 0.003);
      for (let i = 0; i < n; i += 2) {
        let x = -W / 2 + t + 0.02;
        let k = i;
        while (x < W * 0.15) {
          const bw = 0.025 + ((k * 7) % 5) * 0.006, bh = Math.min(gap - 0.04, 0.2 + ((k * 3) % 4) * 0.025);
          blk(g, books[k % books.length], bw, bh, Math.min(D - 0.06, 0.2), x + bw / 2, plinth + i * gap + t, 0, 0.003);
          x += bw + 0.002;
          k++;
        }
      }
      break;
    }
    case 'shelf': {
      const n = Math.max(1, Math.round(H / 0.35));
      for (let i = 0; i <= n; i++) blk(g, wood, W, 0.025, D, 0, Math.min(H - 0.025, i * (H / n)), 0, 0.006);
      for (const x of [-W / 2 + 0.02, W / 2 - 0.02]) for (const z of [-D / 2 + 0.02, D / 2 - 0.02]) blk(g, metal, 0.02, H, 0.02, x, 0, z, 0.004);
      break;
    }
    case 'sofa':
    case 'armchair': {
      const seatH = Math.min(H * 0.62, measure(p, 'seatHeight') ?? H * 0.52);
      const arm = Math.min(0.22, W * 0.12);
      const armH = Math.min(H * 0.85, measure(p, 'armrestHeight') ?? seatH + 0.17);
      const backD = Math.min(0.24, D * 0.26);
      const legH = 0.08;
      for (const x of [-W / 2 + 0.06, W / 2 - 0.06]) for (const z of [-D / 2 + 0.06, D / 2 - 0.06]) cyl(g, leg, 0.022, legH, x, 0, z, 12);
      blk(g, fabric, W, seatH - 0.14 - legH, D, 0, legH, 0, 0.04); // base
      for (const sx of [-1, 1]) blk(g, fabric, arm, armH - legH, D, sx * (W / 2 - arm / 2), legH, 0, 0.06);
      blk(g, fabric, W - 2 * arm + 0.02, H - legH, backD, 0, legH, -D / 2 + backD / 2, 0.06);
      const seats = p.type === 'armchair' ? 1 : count(name, /(\d)\s*-?\s*seat/i, W > 2 ? 3 : W > 1.5 ? 2 : 1);
      const cw = (W - 2 * arm) / seats;
      const cushion = mat(lighter(col, 0.02), 0.95);
      for (let i = 0; i < seats; i++) {
        const x = -W / 2 + arm + cw * (i + 0.5);
        blk(g, cushion, cw - 0.012, 0.15, D - backD - 0.02, x, seatH - 0.15, backD / 2 + 0.005, 0.06);
        const bc = blk(g, cushion, cw - 0.02, Math.min(0.5, H - seatH - 0.02), 0.18, x, seatH - 0.01, -D / 2 + backD + 0.06, 0.07);
        bc.rotation.x = -0.12;
      }
      break;
    }
    case 'bed': {
      const frameH = Math.min(0.32, H * 0.4);
      const head = Math.min(H, measure(p, 'headboardHeight') ?? H);
      const mw = Math.min(W - 0.08, measure(p, 'mattressWidth') ?? W - 0.1);
      const ml = Math.min(D - 0.12, measure(p, 'mattressLength') ?? D - 0.12);
      blk(g, wood, W, frameH, D - 0.06, 0, 0, 0.03, 0.015);
      blk(g, wood, W, head, 0.06, 0, 0, -D / 2 + 0.03, 0.015);
      const sheet = mat('#f7f5f0', 0.95);
      blk(g, mat('#fbfaf7', 0.9), mw, 0.2, ml, 0, frameH - 0.06, -D / 2 + 0.07 + ml / 2, 0.05);
      blk(g, mat('#e7e1d6', 0.95), mw + 0.04, 0.06, ml * 0.62, 0, frameH + 0.12, -D / 2 + 0.07 + ml * 0.69, 0.04); // duvet
      const pillows = mw > 1.3 ? 2 : 1;
      for (let i = 0; i < pillows; i++) blk(g, sheet, mw / pillows - 0.1, 0.12, 0.38, -mw / 2 + (mw / pillows) * (i + 0.5), frameH + 0.14, -D / 2 + 0.32, 0.05);
      break;
    }
    case 'desk':
    case 'table': {
      const top = 0.03;
      blk(g, body, W, top, D, 0, H - top, 0, 0.008);
      const inset = 0.05;
      const isDesk = p.type === 'desk';
      const legs: [number, number][] = isDesk && W > 0.9
        ? [[-W / 2 + inset, -D / 2 + inset], [-W / 2 + inset, D / 2 - inset]]
        : [[-W / 2 + inset, -D / 2 + inset], [-W / 2 + inset, D / 2 - inset], [W / 2 - inset, -D / 2 + inset], [W / 2 - inset, D / 2 - inset]];
      for (const [x, z] of legs) blk(g, isDesk ? body : leg, 0.045, H - top, 0.045, x, 0, z, 0.008);
      if (isDesk && W > 0.9) {
        // drawer unit on the right
        const uw = Math.min(0.4, W * 0.35);
        blk(g, body, uw, H - top, D - 0.02, W / 2 - uw / 2, 0, 0, 0.006);
        for (let i = 0; i < 2; i++) {
          const y = (H - top) * (i ? 0.5 : 0.02) + 0.02;
          blk(g, mat(lighter(col, -0.03), 0.5), uw - 0.02, (H - top) / 2 - 0.04, 0.012, W / 2 - uw / 2, y, D / 2 - 0.004, 0.004);
          blk(g, metal, 0.12, 0.012, 0.015, W / 2 - uw / 2, y + (H - top) / 2 - 0.1, D / 2 + 0.006, 0.004);
        }
      }
      break;
    }
    case 'chair': {
      const seat = Math.min(0.46, H * 0.52);
      for (const x of [-W / 2 + 0.04, W / 2 - 0.04]) for (const z of [-D / 2 + 0.04, D / 2 - 0.04]) blk(g, leg, 0.03, seat, 0.03, x, 0, z, 0.006);
      blk(g, wood, W, 0.04, D, 0, seat, 0, 0.012);
      blk(g, wood, W, H - seat - 0.04, 0.03, 0, seat + 0.04, -D / 2 + 0.02, 0.012).rotation.x = -0.08;
      break;
    }
    case 'wardrobe':
    case 'drawers':
    case 'tvbench': {
      const plinth = Math.min(0.08, H * 0.1);
      blk(g, body, W, H - plinth, D, 0, plinth, 0, r);
      blk(g, leg, W - 0.04, plinth, D - 0.06, 0, 0, -0.02, 0.005);
      const front = mat(lighter(col, -0.015), 0.48);
      if (p.type === 'wardrobe') {
        const doors = count(name, /(\d)\s*doors?/i, W <= 0.6 ? 1 : W <= 1.2 ? 2 : 3);
        const dw = (W - 0.01) / doors;
        for (let i = 0; i < doors; i++) {
          const x = -W / 2 + dw * (i + 0.5) + 0.005;
          blk(g, front, dw - 0.008, H - plinth - 0.02, 0.018, x, plinth + 0.01, D / 2, 0.004);
          blk(g, metal, 0.014, 0.3, 0.02, x + (i % 2 ? -1 : 1) * (dw / 2 - 0.05), H * 0.5, D / 2 + 0.018, 0.005);
        }
      } else if (p.type === 'drawers') {
        const n = count(name, /(\d)\s*-?\s*drawers?/i, /六斗/.test(name) ? 6 : /四斗/.test(name) ? 4 : /三斗/.test(name) ? 3 : Math.max(2, Math.round((H - plinth) / 0.2)));
        const cols = W > 1.1 && n >= 6 ? 2 : 1;
        const rows = Math.ceil(n / cols);
        const dh = (H - plinth - 0.02) / rows;
        for (let c = 0; c < cols; c++) for (let i = 0; i < rows; i++) {
          const fw = W / cols - 0.012;
          const x = -W / 2 + (W / cols) * (c + 0.5);
          const y = plinth + 0.01 + i * dh;
          blk(g, front, fw, dh - 0.008, 0.018, x, y, D / 2, 0.004);
          blk(g, metal, Math.min(0.2, fw * 0.4), 0.014, 0.02, x, y + dh * 0.62, D / 2 + 0.018, 0.005);
        }
      } else {
        // TV bench: two doors and an open shelf in the middle
        const third = W / 3;
        for (const x of [-third, third]) {
          blk(g, front, third - 0.01, H - plinth - 0.03, 0.018, x, plinth + 0.015, D / 2, 0.004);
          blk(g, metal, 0.12, 0.012, 0.018, x, H * 0.62, D / 2 + 0.018, 0.004);
        }
        blk(g, mat(darker(col, 0.15), 0.6), third - 0.03, H - plinth - 0.08, 0.01, 0, plinth + 0.04, D / 2 - 0.02, 0.004);
      }
      break;
    }
    case 'lamp': {
      cyl(g, M.ink, Math.min(W, D) * 0.4, 0.03, 0, 0, 0, 24);
      cyl(g, metal, 0.012, H - 0.3, 0, 0.03, 0, 8);
      const shade = new THREE.MeshStandardMaterial({ color: '#f5efe2', roughness: 0.8, emissive: '#ffcf8a', emissiveIntensity: 0, side: THREE.DoubleSide });
      shade.name = 'lamp_shade';
      mesh(g, new THREE.CylinderGeometry(Math.min(W, D) * 0.32, Math.min(W, D) * 0.48, 0.3, 28, 1, true), shade, 0, H - 0.15, 0);
      break;
    }
    case 'rug': {
      blk(g, mat(col, 1), W, Math.max(0.008, H), D, 0, 0, 0, 0.004);
      blk(g, mat(darker(col, 0.12), 1), W - 0.12, Math.max(0.008, H) + 0.002, D - 0.12, 0, 0, 0, 0.004).scale.set(1, 1, 1);
      blk(g, mat(col, 1), W - 0.18, Math.max(0.008, H) + 0.004, D - 0.18, 0, 0, 0, 0.004);
      break;
    }
    default: {
      blk(g, body, W, H, D, 0, 0, 0, Math.min(0.03, r * 2));
      const s = labelSprite(p.name || p.typeLabel);
      s.position.set(0, H + 0.18, 0);
      g.add(s);
    }
  }
  g.traverse((o) => {
    const m = o as THREE.Mesh;
    if (m.isMesh) {
      m.castShadow = p.type !== 'rug';
      m.receiveShadow = true;
    }
  });

  return g;
}
