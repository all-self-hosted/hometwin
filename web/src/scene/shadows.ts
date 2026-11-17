// The sun's shadow map is drawn only when something moved, not every frame.
import type * as THREE from 'three';

let pending = 2;
export function invalidateShadows(_gl?: THREE.WebGLRenderer, frames = 2) {
  pending = Math.max(pending, frames);
}
/** Called once per frame: says whether the shadow map must be redrawn this frame. */
export function takeShadow(): boolean {
  if (pending <= 0) return false;
  pending--;
  return true;
}
