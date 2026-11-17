// Frame timing, for measuring: window.__fps holds the average frames per second over the last
// second, and the renderer's draw calls.
import { useFrame } from '@react-three/fiber';

declare global {
  interface Window { __fps?: { fps: number; calls: number; tris: number }; __cam?: unknown }
}

let frames = 0, since = performance.now();
export function Stats() {
  useFrame(({ gl, camera, controls, scene }) => {
    frames++;
    window.__cam = { camera, controls, scene };
    const now = performance.now();
    if (now - since >= 1000) {
      window.__fps = { fps: Math.round((frames * 1000) / (now - since)), calls: gl.info.render.calls, tris: gl.info.render.triangles };
      frames = 0;
      since = now;
    }
  });
  return null;
}
