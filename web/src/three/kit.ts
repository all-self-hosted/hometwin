// Materials, procedural textures and geometry helpers for the stylized-real interior look.
import * as THREE from 'three';
import { RoundedBoxGeometry } from 'three/addons/geometries/RoundedBoxGeometry.js';

export const C = {
  wall: '#f5f2ec',
  wallCap: '#b9ae9e',
  wallOut: '#ece6dc',
  plinth: '#dcd5c8',
  lawn: '#c8d9ae',
  lawnDark: '#b5cc98',
  path: '#e3ddd2',
  oak: '#c9a882',
  oakLight: '#d8c09f',
  tile: '#e8e6e1',
  tileWarm: '#e3d8c8',
  white: '#fbfaf7',
  ink: '#2a2d33',
  steel: '#c7cacd',
  glass: '#cfe3ee',
  leaf: '#94bb80',
  leafDark: '#76a065',
  trunk: '#9a7655',
};

const std = (color: THREE.ColorRepresentation, o: THREE.MeshStandardMaterialParameters = {}) => new THREE.MeshStandardMaterial({ color, roughness: 0.7, metalness: 0, ...o });

export function canvasTex(w: number, h: number, draw: (g: CanvasRenderingContext2D, w: number, h: number) => void, repeat = true): THREE.CanvasTexture {
  const c = document.createElement('canvas');
  c.width = w;
  c.height = h;
  draw(c.getContext('2d')!, w, h);
  const t = new THREE.CanvasTexture(c);
  t.colorSpace = THREE.SRGBColorSpace;
  t.anisotropy = 8;
  if (repeat) t.wrapS = t.wrapT = THREE.RepeatWrapping;
  return t;
}

// a seeded random so the planks look the same on every load
function rng(seed: number) {
  return () => {
    seed = (seed * 16807) % 2147483647;
    return (seed - 1) / 2147483646;
  };
}

/** Oak planks, 1 texture = 2.4 × 1.2 m: 8 rows of 15 cm planks with staggered joints. */
function planks(base: string, seed: number) {
  return canvasTex(1024, 512, (g, w, h) => {
    const r = rng(seed);
    const col = new THREE.Color(base);
    const rows = 8;
    const rh = h / rows;
    for (let i = 0; i < rows; i++) {
      let x = -r() * w * 0.5;
      while (x < w) {
        const pw = w * (0.35 + r() * 0.3);
        const c = col.clone().offsetHSL((r() - 0.5) * 0.012, (r() - 0.5) * 0.05, (r() - 0.5) * 0.06);
        g.fillStyle = `#${c.getHexString()}`;
        g.fillRect(x, i * rh, pw, rh);
        // grain
        g.globalAlpha = 0.07;
        for (let k = 0; k < 14; k++) {
          g.strokeStyle = r() > 0.5 ? '#7a5634' : '#ffffff';
          g.lineWidth = 0.6 + r() * 1.2;
          g.beginPath();
          const y = i * rh + r() * rh;
          g.moveTo(x, y);
          g.bezierCurveTo(x + pw * 0.3, y + (r() - 0.5) * 6, x + pw * 0.7, y + (r() - 0.5) * 6, x + pw, y + (r() - 0.5) * 4);
          g.stroke();
        }
        g.globalAlpha = 1;
        g.fillStyle = 'rgba(80,55,30,0.28)';
        g.fillRect(x, i * rh, 1.5, rh);
        x += pw;
      }
      g.fillStyle = 'rgba(80,55,30,0.22)';
      g.fillRect(0, i * rh, w, 1.5);
    }
  });
}

function tiles(base: string, n: number, grout: string) {
  return canvasTex(512, 512, (g, w) => {
    const r = rng(7);
    const s = w / n;
    g.fillStyle = grout;
    g.fillRect(0, 0, w, w);
    const col = new THREE.Color(base);
    for (let i = 0; i < n; i++) for (let j = 0; j < n; j++) {
      const c = col.clone().offsetHSL(0, 0, (r() - 0.5) * 0.025);
      g.fillStyle = `#${c.getHexString()}`;
      g.fillRect(i * s + 1.5, j * s + 1.5, s - 3, s - 3);
    }
  });
}

function grass() {
  return canvasTex(512, 512, (g, w, h) => {
    const r = rng(3);
    g.fillStyle = C.lawn;
    g.fillRect(0, 0, w, h);
    for (let i = 0; i < 4000; i++) {
      g.fillStyle = r() > 0.5 ? 'rgba(150,185,120,0.35)' : 'rgba(225,238,200,0.3)';
      g.fillRect(r() * w, r() * h, 2, 2 + r() * 3);
    }
  });
}

export const TEX = {
  oak: planks(C.oak, 11),
  oakLight: planks(C.oakLight, 23),
  tile: tiles(C.tile, 4, '#d4d1cb'),
  tileSmall: tiles('#eceae6', 8, '#d0cdc7'),
  tileWarm: tiles(C.tileWarm, 4, '#cfc3b2'),
  grass: grass(),
};

/** Floor texture metres per repeat: how big one texture is on the floor. */
export const TEX_SIZE: Record<string, [number, number]> = { oak: [2.4, 1.2], oakLight: [2.4, 1.2], tile: [2.4, 2.4], tileSmall: [2.4, 2.4], tileWarm: [2.4, 2.4], grass: [6, 6] };

export const M = {
  wall: std(C.wall, { roughness: 0.92 }),
  wallCap: std(C.wallCap, { roughness: 0.9 }),
  wallOut: std(C.wallOut, { roughness: 0.95 }),
  ghost: new THREE.MeshBasicMaterial({ color: '#ffffff', transparent: true, opacity: 0.055, depthWrite: false, side: THREE.DoubleSide }),
  ghostLine: new THREE.LineBasicMaterial({ color: '#cfc6b8', transparent: true, opacity: 0.55, depthWrite: false }),
  plinth: std(C.plinth, { roughness: 0.95 }),
  path: std(C.path, { roughness: 0.95 }),
  white: std(C.white, { roughness: 0.45 }),
  satin: std('#f1eee8', { roughness: 0.55 }),
  worktop: std('#e9e4dc', { roughness: 0.35 }),
  ceramic: std('#ffffff', { roughness: 0.18 }),
  steel: std(C.steel, { roughness: 0.3, metalness: 0.7 }),
  dark: std('#3a3d42', { roughness: 0.5 }),
  ink: std(C.ink, { roughness: 0.4 }),
  frame: std('#e9e6e0', { roughness: 0.5 }),
  frameDark: std('#55585e', { roughness: 0.45, metalness: 0.3 }),
  glass: new THREE.MeshPhysicalMaterial({ color: C.glass, roughness: 0.05, metalness: 0, transmission: 0, transparent: true, opacity: 0.32, depthWrite: false }),
  door: std('#efe9df', { roughness: 0.55 }),
  doorEntry: std('#8a6a4f', { roughness: 0.5 }),
  leaf: std(C.leaf, { roughness: 0.8 }),
  leafDark: std(C.leafDark, { roughness: 0.8 }),
  trunk: std(C.trunk, { roughness: 0.9 }),
  pot: std('#d9cbb8', { roughness: 0.8 }),
};

const cache = new Map<string, THREE.BufferGeometry>();
export function rbox(w: number, h: number, d: number, r = 0.02): THREE.BufferGeometry {
  const rr = Math.max(0, Math.min(r, w / 2 - 0.001, h / 2 - 0.001, d / 2 - 0.001));
  const key = `rb:${w.toFixed(4)}:${h.toFixed(4)}:${d.toFixed(4)}:${rr.toFixed(4)}`;
  let g = cache.get(key);
  if (!g) {
    g = rr > 0.003 ? new RoundedBoxGeometry(w, h, d, 2, rr) : new THREE.BoxGeometry(w, h, d);
    cache.set(key, g);
  }
  return g;
}
export function cylGeo(rt: number, rb: number, h: number, seg = 24): THREE.BufferGeometry {
  const key = `cy:${rt}:${rb}:${h}:${seg}`;
  let g = cache.get(key);
  if (!g) cache.set(key, (g = new THREE.CylinderGeometry(rt, rb, h, seg)));
  return g;
}

export function mesh(parent: THREE.Object3D | null, geo: THREE.BufferGeometry, mat: THREE.Material | THREE.Material[], x = 0, y = 0, z = 0, shadow = true): THREE.Mesh {
  const m = new THREE.Mesh(geo, mat);
  m.position.set(x, y, z);
  m.castShadow = shadow;
  m.receiveShadow = true;
  parent?.add(m);
  return m;
}

/** A rounded block whose y is its bottom face. */
export const blk = (p: THREE.Object3D | null, mat: THREE.Material, w: number, h: number, d: number, x = 0, y = 0, z = 0, r = 0.02) => mesh(p, rbox(w, h, d, r), mat, x, y + h / 2, z);
export const cyl = (p: THREE.Object3D | null, mat: THREE.Material, r: number, h: number, x = 0, y = 0, z = 0, seg = 24) => mesh(p, cylGeo(r, r, h, seg), mat, x, y + h / 2, z);

/** Colour temperature in kelvin to an RGB colour (Tanner Helland's fit). */
export function kelvin(k: number, out = new THREE.Color()): THREE.Color {
  const t = k / 100;
  let r: number, g: number, b: number;
  if (t <= 66) {
    r = 255;
    g = 99.47 * Math.log(t) - 161.12;
    b = t <= 19 ? 0 : 138.52 * Math.log(t - 10) - 305.04;
  } else {
    r = 329.7 * Math.pow(t - 60, -0.1332);
    g = 288.12 * Math.pow(t - 60, -0.0755);
    b = 255;
  }
  const c = (v: number) => Math.max(0, Math.min(255, v)) / 255;
  return out.setRGB(c(r), c(g), c(b), THREE.SRGBColorSpace);
}

/** A soft round glow texture (white centre to transparent edge). */
export const glowTex = canvasTex(256, 256, (g, w) => {
  const grad = g.createRadialGradient(w / 2, w / 2, 0, w / 2, w / 2, w / 2);
  grad.addColorStop(0, 'rgba(255,255,255,1)');
  grad.addColorStop(0.35, 'rgba(255,255,255,0.45)');
  grad.addColorStop(1, 'rgba(255,255,255,0)');
  g.fillStyle = grad;
  g.fillRect(0, 0, w, w);
}, false);

/** The soft dark blob under a piece of furniture, standing in for contact shadow and AO. */
export const blobTex = canvasTex(128, 128, (g, w) => {
  const grad = g.createRadialGradient(w / 2, w / 2, w * 0.1, w / 2, w / 2, w / 2);
  grad.addColorStop(0, 'rgba(0,0,0,0.5)');
  grad.addColorStop(1, 'rgba(0,0,0,0)');
  g.fillStyle = grad;
  g.fillRect(0, 0, w, w);
}, false);

export const smooth = (t: number) => t * t * (3 - 2 * t);
export const follow = (cur: number, want: number, dt: number, k: number) => cur + (want - cur) * (1 - Math.exp(-dt * k));

/** Darkening from a wall's foot across the floor: baked-in-look ambient occlusion. */
export const edgeAOTex = canvasTex(4, 128, (g, _w, h) => {
  const grad = g.createLinearGradient(0, 0, 0, h);
  grad.addColorStop(0, 'rgba(60,40,20,0.30)');
  grad.addColorStop(0.35, 'rgba(60,40,20,0.12)');
  grad.addColorStop(1, 'rgba(60,40,20,0)');
  g.fillStyle = grad;
  g.fillRect(0, 0, 4, h);
}, false);
