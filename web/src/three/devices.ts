// The smart devices as models that follow their state: lights that really light the room,
// an AC that blows, curtains that open, a lock that shows red or green, a robot on its route.
import * as THREE from 'three';
import type { Device } from '@/api';
import { M, blk, cyl, cylGeo, follow, glowTex, kelvin, mesh, rbox } from './kit';

export interface Env { night: boolean; cut: number; t: number; dt: number }
export interface DeviceModel {
  root: THREE.Group;
  /** where the card and the label point at, local */
  anchor: THREE.Vector3;
  update(d: Device, env: Env): void;
  /** the meshes that pick the device */
  pick: THREE.Object3D[];
}

const tmpC = new THREE.Color();

function light(d: Device): DeviceModel {
  const root = new THREE.Group();
  const pendant = d.name.includes('吊灯');
  const y0 = pendant ? -0.85 : 0;
  const shadeMat = new THREE.MeshStandardMaterial({ color: '#fbf8f2', roughness: 0.5, emissive: '#ffd9a0', emissiveIntensity: 0 });
  if (pendant) {
    mesh(root, cylGeo(0.004, 0.004, 0.85, 6), M.ink, 0, -0.425, 0, false);
    const dome = mesh(root, new THREE.SphereGeometry(0.2, 32, 16, 0, Math.PI * 2, 0, Math.PI / 2), M.white, 0, y0, 0, false);
    dome.scale.y = 0.75;
    mesh(root, new THREE.CircleGeometry(0.19, 32), shadeMat, 0, y0 + 0.005, 0, false).rotation.x = Math.PI / 2;
  } else {
    mesh(root, cylGeo(0.17, 0.19, 0.045, 40), M.white, 0, -0.022, 0, false);
    mesh(root, new THREE.CircleGeometry(0.165, 40), shadeMat, 0, -0.046, 0, false).rotation.x = Math.PI / 2;
  }
  const lamp = new THREE.PointLight('#ffd9a0', 0, 7, 1.6);
  lamp.position.set(0, y0 - 0.15, 0);
  lamp.castShadow = false;
  root.add(lamp);
  // the warm pool it throws on the floor at night
  const pool = new THREE.Mesh(new THREE.PlaneGeometry(3.6, 3.6), new THREE.MeshBasicMaterial({ map: glowTex, transparent: true, opacity: 0, depthWrite: false, blending: THREE.AdditiveBlending, color: '#ffcf8a' }));
  pool.rotation.x = -Math.PI / 2;
  pool.position.y = -d.y + 0.012;
  pool.renderOrder = 2;
  root.add(pool);
  // a halo around the lamp, so it reads as lit from far away
  const halo = new THREE.Sprite(new THREE.SpriteMaterial({ map: glowTex, transparent: true, opacity: 0, depthWrite: false, blending: THREE.AdditiveBlending, color: '#ffd9a0' }));
  halo.scale.setScalar(1.1);
  halo.position.y = y0 - 0.05;
  root.add(halo);
  let k = 0;
  return {
    root, anchor: new THREE.Vector3(0, y0, 0), pick: [root],
    update(dv, env) {
      const on = dv.state.on === true;
      const b = on ? (dv.state.brightness ?? 80) / 100 : 0;
      k = follow(k, b, env.dt, 8);
      kelvin(dv.state.ct ?? 3000, tmpC);
      lamp.color.copy(tmpC);
      shadeMat.emissive.copy(tmpC);
      shadeMat.emissiveIntensity = k * (env.night ? 2.2 : 0.9);
      lamp.intensity = k * (env.night ? 5.5 : 1.2);
      (pool.material as THREE.MeshBasicMaterial).color.copy(tmpC);
      (pool.material as THREE.MeshBasicMaterial).opacity = k * (env.night ? 0.5 : 0.1);
      (halo.material as THREE.SpriteMaterial).color.copy(tmpC);
      (halo.material as THREE.SpriteMaterial).opacity = k * (env.night ? 0.85 : 0.35);
    },
  };
}

function ac(d: Device): DeviceModel {
  const root = new THREE.Group();
  const body = blk(root, M.white, 0.92, 0.3, 0.22, 0, -0.15, -0.11 + 0.005, 0.05);
  blk(root, M.frame, 0.86, 0.035, 0.04, 0, -0.14, 0.0, 0.01); // louver
  const led = new THREE.MeshStandardMaterial({ color: '#333', emissive: '#4fb6ff', emissiveIntensity: 0 });
  mesh(root, rbox(0.04, 0.012, 0.01, 0.004), led, 0.32, 0.06, 0.006, false);
  // airflow: drifting motes from the outlet, forward and down
  const n = 70;
  const pos = new Float32Array(n * 3);
  const life = new Float32Array(n).map(() => Math.random());
  const geo = new THREE.BufferGeometry();
  geo.setAttribute('position', new THREE.BufferAttribute(pos, 3));
  const motes = new THREE.Points(geo, new THREE.PointsMaterial({ color: '#8fd0ff', size: 0.16, transparent: true, opacity: 0, depthWrite: false, map: glowTex, blending: THREE.NormalBlending }));
  motes.frustumCulled = false;
  root.add(motes);
  const seed = new Float32Array(n).map(() => Math.random() - 0.5);
  let k = 0;
  return {
    root, anchor: new THREE.Vector3(0, -0.15, 0.1), pick: [body],
    update(dv, env) {
      const on = dv.state.on === true;
      const blowing = on && (dv.state.running !== false || dv.state.mode === 'fan');
      k = follow(k, blowing ? 1 : 0, env.dt, 3);
      const heat = dv.state.mode === 'heat';
      (motes.material as THREE.PointsMaterial).color.set(heat ? '#ff9d5c' : '#6cc0ff');
      (motes.material as THREE.PointsMaterial).opacity = 0.75 * k;
      led.emissive.set(heat ? '#ff9a4a' : '#4fb6ff');
      led.emissiveIntensity = on ? 2 : 0;
      if (k < 0.01) return;
      for (let i = 0; i < n; i++) {
        life[i] += env.dt * 0.45;
        if (life[i] > 1) life[i] -= 1;
        const f = life[i];
        pos[i * 3] = seed[i] * 0.8 + seed[i] * f * 0.6;
        pos[i * 3 + 1] = -0.25 - f * f * 1.1;
        pos[i * 3 + 2] = 0.05 + f * 1.6;
      }
      geo.attributes.position.needsUpdate = true;
    },
  };
}

function lock(): DeviceModel {
  const root = new THREE.Group();
  const plate = blk(root, M.ink, 0.07, 0.24, 0.035, 0, -0.12, 0.02, 0.012);
  const led = new THREE.MeshStandardMaterial({ color: '#222', emissive: '#30d07a', emissiveIntensity: 2 });
  mesh(root, cylGeo(0.012, 0.012, 0.01, 12), led, 0, 0.05, 0.04, false).rotation.x = Math.PI / 2;
  const ring = new THREE.Sprite(new THREE.SpriteMaterial({ map: glowTex, color: '#30d07a', transparent: true, opacity: 0.6, depthWrite: false, blending: THREE.AdditiveBlending }));
  ring.scale.setScalar(0.18);
  ring.position.set(0, 0.05, 0.05);
  root.add(ring);
  return {
    root, anchor: new THREE.Vector3(0, 0, 0.05), pick: [plate],
    update(dv, env) {
      const c = dv.state.locked ? '#30d07a' : '#ffb020';
      led.emissive.set(c);
      (ring.material as THREE.SpriteMaterial).color.set(c);
      (ring.material as THREE.SpriteMaterial).opacity = dv.state.locked ? 0.5 : 0.6 + 0.3 * Math.sin(env.t * 5);
    },
  };
}

function curtain(d: Device, cut: number): DeviceModel {
  const root = new THREE.Group();
  const W = d.width ?? 2;
  const H = Math.min(2.6, cut + 0.1);
  const fabric = new THREE.MeshStandardMaterial({ color: '#efe6d8', roughness: 0.95, side: THREE.DoubleSide, transparent: true, opacity: 0.94 });
  const panels: THREE.Group[] = [];
  for (const s of [-1, 1]) {
    const pivot = new THREE.Group();
    pivot.position.set((s * W) / 2, 0, 0.08);
    // folds: a plane bent into soft waves, hung from its outer edge
    const pw = W / 2;
    const geo = new THREE.PlaneGeometry(pw, H, 36, 1);
    const p = geo.attributes.position as THREE.BufferAttribute;
    for (let i = 0; i < p.count; i++) {
      const x = p.getX(i);
      p.setZ(i, Math.sin(((x + pw / 2) / pw) * Math.PI * 9) * 0.035);
    }
    geo.computeVertexNormals();
    geo.translate((-s * pw) / 2, H / 2, 0);
    const m = mesh(pivot, geo, fabric, 0, 0, 0, true);
    m.receiveShadow = true;
    root.add(pivot);
    panels.push(pivot);
  }
  blk(root, M.frameDark, W + 0.1, 0.02, 0.03, 0, H, 0.08, 0.005);
  let pos = d.state.position ?? 100;
  return {
    root, anchor: new THREE.Vector3(0, H * 0.7, 0.1), pick: panels,
    update(dv, env) {
      pos = follow(pos, dv.state.position ?? 0, env.dt, 6);
      const closed = 1 - pos / 100;
      for (const p of panels) p.scale.x = 0.14 + 0.86 * closed;
    },
  };
}

function sensor(): DeviceModel {
  const root = new THREE.Group();
  const body = blk(root, M.white, 0.08, 0.08, 0.025, 0, -0.04, 0.0125, 0.012);
  const scr = new THREE.MeshStandardMaterial({ color: '#2a2d33', emissive: '#7fd6c2', emissiveIntensity: 0.6 });
  mesh(root, new THREE.PlaneGeometry(0.05, 0.03), scr, 0, 0.005, 0.0255, false);
  return { root, anchor: new THREE.Vector3(0, 0, 0.03), pick: [body], update() {} };
}

/** The robot and its dock; the robot itself moves along its route in the scene. */
function vacuum(): DeviceModel & { robot: THREE.Group } {
  const root = new THREE.Group();
  // dock against the wall
  const dock = blk(root, M.white, 0.3, 0.1, 0.12, 0, 0, 0.06, 0.03);
  const dl = new THREE.MeshStandardMaterial({ color: '#222', emissive: '#30d07a', emissiveIntensity: 1.5 });
  mesh(root, rbox(0.06, 0.01, 0.01, 0.003), dl, 0, 0.08, 0.121, false);
  const robot = new THREE.Group();
  cyl(robot, M.white, 0.17, 0.07, 0, 0.012, 0, 40);
  cyl(robot, M.ink, 0.172, 0.012, 0, 0, 0, 40);
  cyl(robot, M.dark, 0.05, 0.03, 0, 0.082, -0.04, 24); // lidar turret
  const eye = new THREE.MeshStandardMaterial({ color: '#111', emissive: '#4fe3c1', emissiveIntensity: 1.6 });
  mesh(robot, rbox(0.12, 0.012, 0.01, 0.004), eye, 0, 0.06, 0.165, false);
  return {
    root, robot, anchor: new THREE.Vector3(0, 0.15, 0.1), pick: [dock, robot],
    update(dv) {
      const charging = dv.state.phase === 'docked' && (dv.state.battery ?? 100) < 100;
      dl.emissive.set(charging ? '#ffb020' : '#30d07a');
    },
  };
}

export function buildDevice(d: Device, cut: number): DeviceModel & { robot?: THREE.Group } {
  switch (d.kind) {
    case 'light': return light(d);
    case 'ac': return ac(d);
    case 'lock': return lock();
    case 'curtain': return curtain(d, cut);
    case 'sensor': return sensor();
    case 'vacuum': return vacuum();
  }
  return sensor();
}
