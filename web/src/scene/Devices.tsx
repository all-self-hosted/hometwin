// The devices in the scene, kept in step with their state, and the robot on its route.
import { useEffect, useMemo, useRef } from 'react';
import { useFrame, useThree, type ThreeEvent } from '@react-three/fiber';
import { Html } from '@react-three/drei';
import { KIND_ICON, isOn, summary } from '@/ui/bits';
import * as THREE from 'three';
import type { Device, Home } from '@/api';
import { floorAt } from '@/geom';
import { useStore } from '@/store';
import { buildDevice, type DeviceModel } from '@/three/devices';
import { glowTex } from '@/three/kit';
import { pathLength, pointAt, vacuumRoute } from '@/three/path';
import { invalidateShadows } from './shadows';

export const anchors = new Map<string, THREE.Object3D>();

function Gadget({ d, home }: { d: Device; home: Home }) {
  const model = useMemo<DeviceModel & { robot?: THREE.Group }>(() => buildDevice(d, home.cutHeight), [d.id, d.kind, d.width, home]); // eslint-disable-line react-hooks/exhaustive-deps
  // what stands on the floor or hangs on a wall goes up with a raised floor; the ceiling stays put
  const lift = useMemo(() => (d.y < home.wallHeight - 0.3 ? floorAt(home, [d.x, d.z]) : 0), [home, d.x, d.y, d.z]);
  const gl = useThree((s) => s.gl);
  const selected = useStore((s) => s.sel?.kind === 'device' && s.sel.id === d.id);
  const lit = useStore((s) => s.highlight.includes(`device:${d.id}`));
  const ring = useMemo(() => {
    const m = new THREE.Sprite(new THREE.SpriteMaterial({ map: glowTex, color: '#d8692f', transparent: true, depthWrite: false, depthTest: false, opacity: 0.9 }));
    m.scale.setScalar(0.55);
    return m;
  }, []);
  const anchor = useRef<THREE.Object3D>(null!);
  const mode = useStore((s) => s.mode);
  const live = useStore((s) => s.devices[d.id]);

  useEffect(() => {
    anchors.set(d.id, anchor.current);
    return () => { anchors.delete(d.id); };
  }, [d.id]);
  useEffect(() => invalidateShadows(gl), [gl, d.x, d.z]);

  useFrame(({ clock }, dt) => {
    const dev = useStore.getState().devices[d.id] ?? d;
    const night = useStore.getState().night;
    model.update(dev, { night, cut: home.cutHeight, t: clock.elapsedTime, dt: Math.min(dt, 0.05) });
    ring.visible = selected || lit;
    (ring.material as THREE.SpriteMaterial).opacity = 0.55 + 0.35 * Math.sin(clock.elapsedTime * 4);
  });

  const click = (e: ThreeEvent<MouseEvent>) => {
    if (e.delta > 6) return;
    e.stopPropagation();
    useStore.getState().select({ kind: 'device', id: d.id });
  };

  return (
    <>
      <group position={[d.x, d.y + lift, d.z]} rotation={[0, ((d.rot ?? 0) * Math.PI) / 180, 0]}>
        <primitive object={model.root} onClick={click} />
        <group ref={anchor} position={model.anchor}>
          <primitive object={ring} />
          {mode === 'devices' && live && !selected && d.kind !== 'sensor' && <Chip d={live} />}
        </group>
      </group>
      {model.robot && <Robot d={d} home={home} robot={model.robot} onClick={click} />}
    </>
  );
}

/** In 设备 mode every device wears a chip: what it is and what it is doing; tap to open it. */
function Chip({ d }: { d: Device }) {
  const Icon = KIND_ICON[d.kind];
  const on = isOn(d) && d.kind !== 'sensor';
  return (
    <Html center zIndexRange={[18, 6]} style={{ pointerEvents: 'auto' }}>
      <button type="button" onClick={() => useStore.getState().select({ kind: 'device', id: d.id })}
        className={`flex items-center gap-1 whitespace-nowrap rounded-full py-0.5 pl-1 pr-2 text-[10.5px] font-bold shadow-float ${on ? 'bg-primary text-white' : 'bg-white/92 text-foreground'}`}>
        <span className={`grid size-5 place-items-center rounded-full ${on ? 'bg-white/25' : 'bg-secondary'}`}><Icon className="size-3" /></span>
        {summary(d)}
      </button>
    </Html>
  );
}

/** The robot drives its route in world space; the dock stays where the device stands. */
function Robot({ d, home, robot, onClick }: { d: Device; home: Home; robot: THREE.Group; onClick: (e: ThreeEvent<MouseEvent>) => void }) {
  const items = useStore((s) => s.items);
  const route = useMemo(() => vacuumRoute(home, items, d), [home, items, d]);
  const tube = useMemo(() => {
    const pts = route.clean.map(([x, z]) => new THREE.Vector3(x, 0.015, z));
    const curve = new THREE.CatmullRomCurve3(pts, false, 'catmullrom', 0.05);
    const segs = Math.max(64, Math.round(pathLength(route.clean) * 12));
    const geo = new THREE.TubeGeometry(curve, segs, 0.018, 6, false);
    const done = new THREE.Mesh(geo, new THREE.MeshBasicMaterial({ color: '#2bc3a7', transparent: true, opacity: 0.9, depthWrite: false }));
    const all = new THREE.Mesh(geo, new THREE.MeshBasicMaterial({ color: '#2bc3a7', transparent: true, opacity: 0.22, depthWrite: false }));
    done.renderOrder = all.renderOrder = 3;
    const g = new THREE.Group();
    g.add(all, done);
    return { g, done, count: geo.index!.count };
  }, [route]);

  const pos = useRef<[number, number]>([d.x, d.z]);
  const yaw = useRef(0);
  useFrame((_, dt) => {
    const dev = useStore.getState().devices[d.id] ?? d;
    const phase = dev.state.phase as string;
    const prog = Number(dev.state.progress ?? 0);
    let target = { p: [d.x, d.z] as [number, number], yaw: ((d.rot ?? 0) * Math.PI) / 180 + Math.PI };
    if (phase === 'cleaning' || phase === 'paused') target = pointAt(route.clean, prog);
    else if (phase === 'returning') target = pointAt(route.back, Number(dev.state.back ?? 0));
    // glide toward where the server says it is
    const k = 1 - Math.exp(-dt * 6);
    pos.current = [pos.current[0] + (target.p[0] - pos.current[0]) * k, pos.current[1] + (target.p[1] - pos.current[1]) * k];
    let dy = target.yaw - yaw.current;
    while (dy > Math.PI) dy -= 2 * Math.PI;
    while (dy < -Math.PI) dy += 2 * Math.PI;
    yaw.current += dy * k;
    robot.position.set(pos.current[0], floorAt(home, pos.current), pos.current[1]);
    robot.rotation.y = yaw.current;
    const active = phase === 'cleaning' || phase === 'paused' || phase === 'returning';
    tube.g.visible = active;
    tube.done.geometry.setDrawRange(0, Math.floor((tube.count * (phase === 'returning' ? 1 : prog)) / 3) * 3);
  });
  return (
    <>
      <primitive object={robot} onClick={onClick} />
      <primitive object={tube.g} />
    </>
  );
}

export function Devices({ home }: { home: Home }) {
  const devices = useStore((s) => s.devices);
  // placement changes rebuild a device; state changes do not
  const placed = useMemo(() => Object.values(devices).map((d) => ({ ...d, state: d.state })), [Object.values(devices).map((d) => `${d.id}:${d.x}:${d.y}:${d.z}:${d.rot}`).join('|')]); // eslint-disable-line react-hooks/exhaustive-deps
  return <>{placed.map((d) => <Gadget key={d.id} d={d} home={home} />)}</>;
}
