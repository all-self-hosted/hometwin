// The home's shell in the scene: floors you can click to focus a room, doors that follow the
// lock, door swing arcs while arranging, and the props.
import { useEffect, useMemo, useRef, useState } from 'react';
import { GLTFLoader } from 'three/addons/loaders/GLTFLoader.js';
import { toast } from 'sonner';
import { useFrame, useThree, type ThreeEvent } from '@react-three/fiber';
import * as THREE from 'three';
import type { Home } from '@/api';
import { swingArc } from '@/geom';
import { useStore } from '@/store';
import { buildShell, setDoorOpen } from '@/three/shell';
import { follow } from '@/three/kit';
import { fitted, kitUrl, prop } from '@/three/models';
import { invalidateShadows } from './shadows';

const PROPS: [string, number, number, number][] = [
  ['/models/plant-tall.glb', 3.95, 9.45, 0],
  ['/models/plant-small.glb', 4.35, 9.5, 0.6],
  ['/models/plant-tall.glb', 8.0, 2.55, 1.2],
  ['/models/plant-small.glb', 0.35, 7.95, 2],
];

const gltf = new GLTFLoader();

/** The home's scanned model, cut at the cut height like the walls. */
function loadScan(url: string, cut: number): Promise<THREE.Group> {
  return new Promise((resolve, reject) => gltf.load(url, (g) => {
    const plane = new THREE.Plane(new THREE.Vector3(0, -1, 0), cut);
    g.scene.traverse((o) => {
      const m = o as THREE.Mesh;
      if (!m.isMesh) return;
      for (const mat of Array.isArray(m.material) ? m.material : [m.material]) {
        mat.clippingPlanes = [plane];
        mat.side = THREE.DoubleSide;
      }
      m.receiveShadow = false;
      m.castShadow = false;
    });
    g.scene.name = 'scan';
    resolve(g.scene);
  }, undefined, reject));
}

export function Shell({ home }: { home: Home }) {
  const shell = useMemo(() => buildShell(home), [home]);
  const scanOn = useStore((s) => s.scan);
  const [scan, setScan] = useState<THREE.Group | null>(null);
  useEffect(() => {
    if (!scanOn || !home.scan || scan) return;
    let alive = true;
    loadScan(home.scan, home.cutHeight).then((g) => { if (alive) setScan(g); }).catch(() => {
      toast.error('实景模型没加载出来');
      useStore.getState().setScan(false);
    });
    return () => { alive = false; };
  }, [scanOn, home.scan, home.cutHeight, scan]);
  const showScan = scanOn && !!scan;
  useEffect(() => {
    shell.structure.visible = !showScan;
    for (const f of shell.floors) f.visible = !showScan;
    invalidateShadows(gl);
  }, [shell, showScan]); // eslint-disable-line react-hooks/exhaustive-deps
  const ghostWalls = useStore((s) => s.ghostWalls);
  const mode = useStore((s) => s.mode);
  const gl = useThree((s) => s.gl);
  const open = useRef(new Map<string, number>());

  useEffect(() => { shell.ghost.visible = ghostWalls; }, [shell, ghostWalls]);
  useEffect(() => {
    let alive = true;
    // the potted plants are placed for the sample flat
    for (const [url, x, z, r] of home.id === 'chengdu-89' ? PROPS : []) {
      prop(url).then((g) => {
        if (!g || !alive) return;
        g.position.set(x, 0, z);
        g.rotation.y = r;
        shell.root.add(g);
        invalidateShadows(gl);
      });
    }
    // fixtures the kit has a model for; the built one stays until it loads
    for (const f of home.fixtures ?? []) {
      const url = kitUrl(f.kind);
      const slot = url && shell.root.getObjectByName(`fixture:${f.id}`);
      if (!slot) continue;
      fitted(url, f.w, f.h, f.d).then((g) => {
        if (!g || !alive) return;
        slot.clear();
        slot.add(g);
        invalidateShadows(gl);
      });
    }
    return () => { alive = false; };
  }, [shell, gl]);

  // doors: room doors stand open; the front door follows the lock's door contact
  useFrame((_, dt) => {
    const devices = useStore.getState().devices;
    const lock = Object.values(devices).find((d) => d.kind === 'lock');
    for (const [id, pivot] of shell.leaves) {
      const want = pivot.userData.entry ? (lock?.state.open ? 0.75 : 0) : 0.62;
      const cur = open.current.get(id) ?? want;
      const next = follow(cur, want, dt, 4);
      if (Math.abs(next - cur) > 1e-4 || !open.current.has(id)) {
        setDoorOpen(pivot, next);
        open.current.set(id, next);
        invalidateShadows(gl);
      }
    }
  });

  const arcs = useMemo(() => {
    const g = new THREE.Group();
    for (const d of home.doors) {
      const a = swingArc(home, d);
      if (!a) continue;
      const pts = [...a, a[0]].map(([x, z]) => new THREE.Vector3(x, 0.012, z));
      const line = new THREE.Line(new THREE.BufferGeometry().setFromPoints(pts), new THREE.LineDashedMaterial({ color: '#d8692f', dashSize: 0.06, gapSize: 0.05, transparent: true, opacity: 0.8 }));
      line.computeLineDistances();
      g.add(line);
      const fill = new THREE.Mesh(
        new THREE.ShapeGeometry(new THREE.Shape(a.map(([x, z]) => new THREE.Vector2(x, -z)))).rotateX(-Math.PI / 2),
        new THREE.MeshBasicMaterial({ color: '#d8692f', transparent: true, opacity: 0.08, depthWrite: false }),
      );
      fill.position.y = 0.01;
      g.add(fill);
    }
    return g;
  }, [home]);

  const onFloor = (e: ThreeEvent<MouseEvent>) => {
    if (e.delta > 6) return;
    const room = (e.object as THREE.Mesh).userData.room as string | undefined;
    const s = useStore.getState();
    if (!room || s.drag) return;
    e.stopPropagation();
    if (s.mode === 'arrange' && s.sel?.kind === 'item') {
      s.select(null);
      return;
    }
    s.select({ kind: 'room', id: room });
    s.camera('room', room);
  };

  return (
    <group>
      <primitive object={shell.root} />
      {shell.floors.map((f) => <primitive key={f.uuid} object={f} onClick={onFloor} />)}
      {mode === 'arrange' && <primitive object={arcs} />}
      {showScan && <primitive object={scan} />}
    </group>
  );
}
