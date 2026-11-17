// The furniture in the scene: generated models at their real size, dragged on the floor with
// snapping, with live size, distances to walls and warnings while arranging.
import { useEffect, useMemo, useRef, useState } from 'react';
import { Html } from '@react-three/drei';
import { useThree, type ThreeEvent } from '@react-three/fiber';
import * as THREE from 'three';
import type { OrbitControls as Impl } from 'three/addons/controls/OrbitControls.js';
import type { Home, Item, P } from '@/api';
import { fixtureRect, floorAt, gaps, itemRect, piecePoly, pieces, snap } from '@/geom';
import { useStore } from '@/store';
import { blobTex } from '@/three/kit';
import { buildFurniture } from '@/three/furniture';
import { fitted, generated, kitUrl } from '@/three/models';
import { invalidateShadows } from './shadows';

const floor = new THREE.Plane(new THREE.Vector3(0, 1, 0), 0);
const hit = new THREE.Vector3();
const blobMat = new THREE.MeshBasicMaterial({ map: blobTex, transparent: true, depthWrite: false, opacity: 0.55 });

function Segment({ a, b, color }: { a: P; b: P; color: string }) {
  const line = useMemo(() => new THREE.Line(new THREE.BufferGeometry(), new THREE.LineBasicMaterial({ color })), [color]);
  useEffect(() => {
    line.geometry.setFromPoints([new THREE.Vector3(a[0], 0.03, a[1]), new THREE.Vector3(b[0], 0.03, b[1])]);
  }, [line, a, b]);
  return <primitive object={line} />;
}

function Piece({ item, home }: { item: Item; home: Home }) {
  const gl = useThree((s) => s.gl);
  const controls = useThree((s) => s.controls) as unknown as Impl | null;
  const p = item.product;
  const holder = useRef<THREE.Group>(null!);
  const fallback = useMemo(() => buildFurniture(p), [p]);
  const [model, setModel] = useState<THREE.Object3D>(fallback);
  const selected = useStore((s) => s.sel?.kind === 'item' && s.sel.id === item.id);
  const lit = useStore((s) => s.highlight.includes(`item:${item.id}`));
  const drag = useStore((s) => (s.drag?.id === item.id ? s.drag : null));
  const mode = useStore((s) => s.mode);
  const warn = useStore((s) => (s.drag?.id === item.id ? s.dragWarnings ?? s.warnings : s.warnings).some((w) => (w.item === item.id || w.with === item.id) && w.level === 'warn'));
  const start = useRef<{ dx: number; dz: number; moved: boolean } | null>(null);

  useEffect(() => {
    setModel(fallback);
    let alive = true;
    const kit = p.model ?? kitUrl(p.type);
    (kit ? fitted(kit, p.w / 100, p.h / 100, p.d / 100).then((g) => g ?? generated(p)) : generated(p)).then((g) => {
      if (g && alive) {
        setModel(g);
        invalidateShadows(gl);
      }
    });
    return () => { alive = false; };
  }, [p, fallback, gl]);
  useEffect(() => invalidateShadows(gl), [item.x, item.z, item.rot, model, gl]);

  const x = drag?.x ?? item.x, z = drag?.z ?? item.z, rot = drag?.rot ?? item.rot;
  const W = p.w / 100, D = p.d / 100, H = p.h / 100;
  // standing on a platform or in a raised room, it stands on that floor
  const base = useMemo(() => floorAt(home, [x, z]), [home, x, z]);

  const solids = useMemo(() => pieces(home), [home]);
  const others = (): P[][] => [
    ...useStore.getState().items.filter((o) => o.id !== item.id && o.product.type !== 'rug' && o.product.type !== 'lamp').map((o) => itemRect(o)),
    ...(home.fixtures ?? []).map(fixtureRect),
  ];

  const down = (e: ThreeEvent<PointerEvent>) => {
    const s = useStore.getState();
    if (s.mode !== 'arrange') {
      if (e.delta <= 6) {
        e.stopPropagation();
        s.select({ kind: 'item', id: item.id });
      }
      return;
    }
    e.stopPropagation();
    s.select({ kind: 'item', id: item.id });
    if (!e.ray.intersectPlane(floor, hit)) return;
    start.current = { dx: item.x - hit.x, dz: item.z - hit.z, moved: false };
    (e.target as Element).setPointerCapture?.(e.pointerId);
    if (controls) controls.enabled = false;
  };
  const move = (e: ThreeEvent<PointerEvent>) => {
    const st = start.current;
    if (!st || !e.ray.intersectPlane(floor, hit)) return;
    e.stopPropagation();
    const nx = hit.x + st.dx, nz = hit.z + st.dz;
    if (!st.moved && Math.hypot(nx - item.x, nz - item.z) < 0.02) return;
    st.moved = true;
    const sn = e.altKey ? { x: nx, z: nz } : snap(item, nx, nz, rot, solids, others());
    useStore.getState().setDrag({ id: item.id, x: sn.x, z: sn.z, rot });
    invalidateShadows(gl);
  };
  const up = (e: ThreeEvent<PointerEvent>) => {
    const st = start.current;
    if (!st) return;
    e.stopPropagation();
    start.current = null;
    (e.target as Element).releasePointerCapture?.(e.pointerId);
    if (controls) controls.enabled = true;
    const s = useStore.getState();
    if (st.moved && s.drag) s.updateItem(item.id, { x: Math.round(s.drag.x * 1000) / 1000, z: Math.round(s.drag.z * 1000) / 1000 });
    s.setDrag(null);
  };

  const showMeasures = selected && mode === 'arrange';
  const gapLines = useMemo(() => {
    if (!showMeasures) return [];
    const obs = [...solids.map(piecePoly), ...others()];
    return gaps(item, obs, x, z, rot).filter((g) => g.m > 0.005);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [showMeasures, x, z, rot, item, solids]);

  const edges = useMemo(() => new THREE.EdgesGeometry(new THREE.BoxGeometry(W + 0.02, H + 0.02, D + 0.02)), [W, H, D]);
  const accent = warn ? '#d93636' : '#d8692f';

  return (
    <>
      <group ref={holder} position={[x, base, z]} rotation={[0, (rot * Math.PI) / 180, 0]} onPointerDown={down} onPointerMove={move} onPointerUp={up}>
        <primitive object={model} name={`model:${item.id}`} />
        {p.type !== 'rug' && (
          <mesh material={blobMat} rotation={[-Math.PI / 2, 0, 0]} position={[0, 0.006, 0]} scale={[W + 0.35, D + 0.35, 1]} renderOrder={1}>
            <planeGeometry />
          </mesh>
        )}
        {(selected || lit) && (
          <>
            <lineSegments geometry={edges} position={[0, H / 2, 0]}>
              <lineBasicMaterial color={lit && !selected ? '#2f7de1' : accent} transparent opacity={0.9} />
            </lineSegments>
            <mesh rotation={[-Math.PI / 2, 0, 0]} position={[0, 0.008, 0]}>
              <planeGeometry args={[W, D]} />
              <meshBasicMaterial color={lit && !selected ? '#2f7de1' : accent} transparent opacity={0.16} depthWrite={false} />
            </mesh>
          </>
        )}
        {selected && (
          <Html position={[0, H + 0.12, 0]} center zIndexRange={[20, 10]} style={{ pointerEvents: 'none' }}>
            <div className="whitespace-nowrap rounded-full bg-white/92 px-2.5 py-1 text-[11.5px] font-bold text-foreground shadow-float">
              {Math.round(p.w)} × {Math.round(p.d)} × {Math.round(p.h)} cm
            </div>
          </Html>
        )}
      </group>
      {gapLines.map((g, i) => {
        const mid: [number, number, number] = [(g.from[0] + g.to[0]) / 2, 0.03, (g.from[1] + g.to[1]) / 2];
        const tight = g.m >= 0.3 && g.m < 0.8;
        return (
          <group key={i}>
            <Segment a={g.from} b={g.to} color={tight ? '#d93636' : '#4b5563'} />
            <Html position={mid} center zIndexRange={[19, 10]} style={{ pointerEvents: 'none' }}>
              <div className={`whitespace-nowrap rounded-md px-1.5 py-0.5 text-[10.5px] font-bold tabular-nums shadow-sm ${tight ? 'bg-destructive text-white' : 'bg-white/90 text-foreground'}`}>{Math.round(g.m * 100)} cm</div>
            </Html>
          </group>
        );
      })}
    </>
  );
}

export function Furniture({ home }: { home: Home }) {
  const items = useStore((s) => s.items);
  return <>{items.map((it) => <Piece key={it.id} item={it} home={home} />)}</>;
}
