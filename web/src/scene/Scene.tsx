// The 3D view: renderer, daylight or night, the shell, furniture, devices, labels, camera.
import { useEffect, useMemo } from 'react';
import { Canvas, useFrame, useThree } from '@react-three/fiber';
import { Html, OrbitControls } from '@react-three/drei';
import * as THREE from 'three';
import { RoomEnvironment } from 'three/addons/environments/RoomEnvironment.js';
import type { Home } from '@/api';
import { centroid } from '@/geom';
import { useStore } from '@/store';
import { M, follow } from '@/three/kit';
import { CameraRig, homeBox, homeView } from './rig';
import { Devices } from './Devices';
import { Furniture } from './Furniture';
import { Shell } from './Shell';
import { invalidateShadows, takeShadow } from './shadows';
import { Stats } from './stats';
import { DeviceCard3D } from '@/ui/Selection';

export const coarse = typeof window !== 'undefined' && window.matchMedia('(pointer: coarse)').matches;


const DAY = { bg: new THREE.Color('#e4ebe6'), hemiSky: new THREE.Color('#f4f7ff'), hemiGround: new THREE.Color('#cdbd9f'), hemi: 0.85, sun: 2.4, sunColor: new THREE.Color('#ffe9cc'), env: 0.35, exposure: 0.95 };
const NIGHT = { bg: new THREE.Color('#1c2333'), hemiSky: new THREE.Color('#5a6d9a'), hemiGround: new THREE.Color('#141720'), hemi: 0.22, sun: 0.28, sunColor: new THREE.Color('#9db4ff'), env: 0.08, exposure: 1.1 };

/** Day and night blend into each other over a second. */
function Lighting({ home }: { home: Home }) {
  const scene = useThree((s) => s.scene);
  const gl = useThree((s) => s.gl);
  const b = homeBox(home);
  const cx = (b.x0 + b.x1) / 2, cz = (b.z0 + b.z1) / 2;
  const sun = useMemo(() => {
    const l = new THREE.DirectionalLight('#fff1dc', 2.7);
    l.position.set(cx - 6, 14, cz + 6);
    l.target.position.set(cx, 0, cz);
    l.castShadow = true;
    l.shadow.mapSize.set(coarse ? 1024 : 2048, coarse ? 1024 : 2048);
    const s = 9;
    Object.assign(l.shadow.camera, { left: -s, right: s, top: s, bottom: -s, near: 2, far: 40 });
    l.shadow.bias = -0.0004;
    l.shadow.normalBias = 0.02;
    l.shadow.radius = 4;
    return l;
  }, [cx, cz]);
  const hemi = useMemo(() => new THREE.HemisphereLight('#ffffff', '#d9ccb6', 1.15), []);
  const k = useMemo(() => ({ v: 0 }), []);
  const bg = useMemo(() => new THREE.Color(), []);

  useEffect(() => {
    const pmrem = new THREE.PMREMGenerator(gl);
    scene.environment = pmrem.fromScene(new RoomEnvironment(), 0.04).texture;
    scene.background = bg;
    scene.fog = new THREE.Fog(bg, 30, 70);
    gl.shadowMap.autoUpdate = false;
    invalidateShadows(gl, 4);
    return () => pmrem.dispose();
  }, [gl, scene, bg]);

  useFrame(({ camera }, dt) => {
    const night = useStore.getState().night;
    // fog starts beyond the home, wherever the camera stands
    const dist = camera.position.length();
    const fog = scene.fog as THREE.Fog;
    fog.near = dist * 1.15;
    fog.far = dist * 2.6;
    const was = k.v;
    k.v = follow(k.v, night ? 1 : 0, dt, 3);
    if (Math.abs(was - k.v) > 1e-4) invalidateShadows(gl);
    const t = k.v;
    bg.copy(DAY.bg).lerp(NIGHT.bg, t);
    (scene.fog as THREE.Fog).color.copy(bg);
    hemi.color.copy(DAY.hemiSky).lerp(NIGHT.hemiSky, t);
    hemi.groundColor.copy(DAY.hemiGround).lerp(NIGHT.hemiGround, t);
    hemi.intensity = THREE.MathUtils.lerp(DAY.hemi, NIGHT.hemi, t);
    sun.intensity = THREE.MathUtils.lerp(DAY.sun, NIGHT.sun, t);
    sun.color.copy(DAY.sunColor).lerp(NIGHT.sunColor, t);
    scene.environmentIntensity = THREE.MathUtils.lerp(DAY.env, NIGHT.env, t);
    gl.toneMappingExposure = THREE.MathUtils.lerp(DAY.exposure, NIGHT.exposure, t);
    // the ghost walls above the cut glow against a night sky; keep them faint then
    M.ghost.opacity = THREE.MathUtils.lerp(0.055, 0.006, t);
    M.ghostLine.opacity = THREE.MathUtils.lerp(0.55, 0.12, t);
    if (takeShadow()) gl.shadowMap.needsUpdate = true;
    if (useStore.getState().drag) gl.shadowMap.needsUpdate = true;
  });
  return (
    <>
      <primitive object={hemi} />
      <primitive object={sun} />
      <primitive object={sun.target} />
    </>
  );
}

function RoomLabels({ home }: { home: Home }) {
  const devices = useStore((s) => s.devices);
  const sel = useStore((s) => s.sel);
  const mode = useStore((s) => s.mode);
  return (
    <>
      {home.rooms.map((r) => {
        const c = centroid(r.poly);
        const sensor = Object.values(devices).find((d) => d.kind === 'sensor' && d.room === r.id);
        const active = sel?.kind === 'room' && sel.id === r.id;
        return (
          <Html key={r.id} position={[c[0], (r.y ?? 0) + 0.05, c[1]]} center zIndexRange={[15, 5]} style={{ pointerEvents: mode === 'arrange' ? 'none' : 'auto' }}>
            <button
              type="button"
              onClick={() => { useStore.getState().select({ kind: 'room', id: r.id }); useStore.getState().camera('room', r.id); }}
              className={`flex items-center gap-1.5 whitespace-nowrap rounded-full px-2.5 py-1 text-[11.5px] shadow-float transition-colors max-md:text-[10.5px] ${active ? 'bg-primary text-white' : 'bg-white/88 text-foreground'} ${mode === 'arrange' ? 'opacity-70' : ''}`}
            >
              <b className="font-bold">{r.name}</b>
              {sensor && (
                <span className={`tabular-nums ${active ? 'text-white/85' : 'text-muted-foreground'}`}>
                  {Number(sensor.state.temp).toFixed(1)}° · {Math.round(sensor.state.humidity)}%
                </span>
              )}
            </button>
          </Html>
        );
      })}
    </>
  );
}

export function Scene({ home }: { home: Home }) {
  const init = homeView(home, typeof window === 'undefined' ? 1.6 : window.innerWidth / window.innerHeight);
  return (
    <Canvas
      className="!absolute inset-0 touch-none"
      shadows={{ type: THREE.PCFSoftShadowMap }}
      dpr={[1, coarse ? 1.75 : 2]}
      camera={{ fov: 30, near: 0.1, far: 200, position: init.pos.toArray() }}
      gl={{ antialias: true, powerPreference: 'high-performance' }}
      onCreated={({ gl }) => {
        gl.toneMapping = THREE.NeutralToneMapping;
        gl.localClippingEnabled = true; // the scanned model is cut like the walls
        gl.outputColorSpace = THREE.SRGBColorSpace;
      }}
      onPointerMissed={(e) => { if (e.type === 'click') useStore.getState().select(null); }}
    >
      <Lighting home={home} />
      <OrbitControls
        makeDefault
        target={init.target}
        enableDamping
        dampingFactor={0.08}
        minDistance={3.5}
        maxDistance={60}
        minPolarAngle={0.15}
        maxPolarAngle={1.32}
        screenSpacePanning={false}
        mouseButtons={{ LEFT: THREE.MOUSE.ROTATE, MIDDLE: THREE.MOUSE.DOLLY, RIGHT: THREE.MOUSE.PAN }}
      />
      <CameraRig home={home} />
      <Shell home={home} />
      <Furniture home={home} />
      <Devices home={home} />
      <RoomLabels home={home} />
      <DeviceCard3D />
      <Stats />
    </Canvas>
  );
}
