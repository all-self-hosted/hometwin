// The camera: a three-second fly-in when the page opens, flights to a room or back home, and
// the controls.
import { useEffect, useRef } from 'react';
import { useFrame, useThree } from '@react-three/fiber';
import * as THREE from 'three';
import type { OrbitControls as Impl } from 'three/addons/controls/OrbitControls.js';
import type { Home } from '@/api';
import { centroid } from '@/geom';
import { useStore } from '@/store';

const ease = (t: number) => (t < 0.5 ? 4 * t * t * t : 1 - Math.pow(-2 * t + 2, 3) / 2);
interface Fly { t: number; dur: number; fromT: THREE.Vector3; fromP: THREE.Vector3; toT: THREE.Vector3; toP: THREE.Vector3 }

export function homeBox(h: Home) {
  const xs = h.walls.flatMap((w) => [w.a[0], w.b[0]]), zs = h.walls.flatMap((w) => [w.a[1], w.b[1]]);
  return { x0: Math.min(...xs), x1: Math.max(...xs), z0: Math.min(...zs), z1: Math.max(...zs) };
}

/** How far back a 30° camera must stand to fit `size` metres across the narrower of its two fields of view. */
export function fitDistance(size: number, aspect: number, k: number) {
  const half = Math.tan(((30 / 2) * Math.PI) / 180);
  return (size * k) / (2 * Math.min(half, half * aspect));
}

/** The overview: from the south-east, looking down into the cut-open rooms. */
export function homeView(h: Home, aspect: number) {
  const b = homeBox(h);
  const target = new THREE.Vector3((b.x0 + b.x1) / 2, 0.3, (b.z0 + b.z1) / 2 + 0.2);
  const size = Math.max(b.x1 - b.x0, b.z1 - b.z0);
  const radius = fitDistance(size, aspect, aspect < 1 ? 1.4 : 1.28);
  const pos = target.clone().add(new THREE.Vector3().setFromSpherical(new THREE.Spherical(radius, 0.82, Math.PI / 5.5)));
  return { target, pos };
}

export function CameraRig({ home }: { home: Home }) {
  const camera = useThree((s) => s.camera) as THREE.PerspectiveCamera;
  const controls = useThree((s) => s.controls) as unknown as Impl | null;
  const fly = useRef<Fly | null>(null);
  const cam = useStore((s) => s.cam);
  const seen = useRef(0);

  // the fly-in: from high and to the side, down to the overview, in 3 s
  useEffect(() => {
    if (!controls) return;
    const v = homeView(home, camera.aspect);
    const from = v.target.clone().add(new THREE.Vector3().setFromSpherical(new THREE.Spherical(v.pos.distanceTo(v.target) * 2.1, 0.32, -Math.PI / 5)));
    camera.position.copy(from);
    controls.target.copy(v.target);
    fly.current = { t: 0, dur: 3, fromT: v.target.clone(), fromP: from, toT: v.target, toP: v.pos };
  }, [controls, camera, home]);

  useEffect(() => {
    if (!cam || !controls || cam.n <= seen.current) return;
    seen.current = cam.n;
    const go = (toT: THREE.Vector3, toP: THREE.Vector3, dur = 1.1) => {
      fly.current = { t: 0, dur, fromT: controls.target.clone(), fromP: camera.position.clone(), toT, toP };
    };
    if (cam.op === 'home') {
      const v = homeView(home, camera.aspect);
      go(v.target, v.pos);
    } else if (cam.op === 'room' && cam.id) {
      const r = home.rooms.find((x) => x.id === cam.id);
      if (!r) return;
      const c = centroid(r.poly);
      const xs = r.poly.map((p) => p[0]), zs = r.poly.map((p) => p[1]);
      const size = Math.max(Math.max(...xs) - Math.min(...xs), Math.max(...zs) - Math.min(...zs));
      const toT = new THREE.Vector3(c[0], 0.3, c[1]);
      const sph = new THREE.Spherical().setFromVector3(camera.position.clone().sub(controls.target));
      sph.radius = THREE.MathUtils.clamp(fitDistance(size, camera.aspect, 1.5), 6, 40);
      sph.phi = Math.min(sph.phi, 0.9);
      go(toT, toT.clone().add(new THREE.Vector3().setFromSpherical(sph)));
    } else if (cam.op === 'point' && cam.at) {
      const toT = new THREE.Vector3(cam.at[0], 0.3, cam.at[1]);
      const off = camera.position.clone().sub(controls.target).setLength(Math.min(11, camera.position.distanceTo(controls.target)));
      go(toT, toT.clone().add(off), 0.9);
    }
  }, [cam, camera, controls, home]);

  useEffect(() => {
    if (!controls) return;
    const stop = () => { fly.current = null; };
    controls.addEventListener('start', stop);
    return () => controls.removeEventListener('start', stop);
  }, [controls]);

  useFrame((_, dt) => {
    const f = fly.current;
    if (!f || !controls) return;
    f.t += Math.min(dt, 0.05);
    const k = ease(Math.min(1, f.t / f.dur));
    controls.target.lerpVectors(f.fromT, f.toT, k);
    camera.position.lerpVectors(f.fromP, f.toP, k);
    if (f.t >= f.dur) fly.current = null;
  });
  return null;
}
