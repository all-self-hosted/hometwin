// Types the server speaks, and the calls to it.

export type P = [number, number];

export interface Room { id: string; name: string; kind: string; poly: P[]; floor?: string; /** the whole room's floor above the main floor, m */ y?: number }
export interface Wall { id: string; a: P; b: P; thick: number; h?: number; exterior?: boolean; railing?: number; glazed?: boolean }
export interface Door { id: string; name?: string; color?: string; wall: string; at: number; width: number; kind: 'swing' | 'sliding' | 'opening'; hinge?: 'a' | 'b'; side?: number; entry?: boolean; height?: number }
export interface Win {
  id: string; wall: string; at: number; width: number; sill: number; height: number;
  /** how far a bay window stands out from the wall's outside face */
  bay?: number;
}
export interface Fixture { id: string; kind: string; name: string; x: number; z: number; w: number; d: number; h: number; rot: number }
export interface FloorLevel { id: string; room: string; kind: 'platform' | 'step' | string; y: number; poly: P[]; floor?: string }
export interface Home {
  id: string; name: string; area: number; wallHeight: number; cutHeight: number;
  rooms: Room[]; walls: Wall[]; doors: Door[]; windows: Win[]; fixtures?: Fixture[]; levels?: FloorLevel[];
  /** the scanned model (glTF binary in the home's frame), when the home came from a VR scan */
  scan?: string;
}

export interface Measure { key: string; name: string; text: string; cm: number; kg?: number }
export interface Pack { w: number; h: number; l: number; kg?: number; n: number }
export interface Product {
  source: 'ikea' | 'manual' | 'polyfork'; url?: string; name: string; family?: string; type: string; typeLabel: string;
  color?: string; colorHex: string; image?: string; article?: string; price?: number; currency?: string;
  w: number; d: number; h: number; dimsFrom: string; measures?: Measure[]; packages?: Pack[];
  /** a ready model (GLB) shown instead of a built one, and its licence line */
  model?: string; credit?: string;
}
/** A model in the catalogue (Polyfork), sizes in cm. */
export interface Hit { id: string; title: string; image: string; page: string; kit?: string; triangles: number; w: number; d: number; h: number; type: string; typeLabel: string }
export interface Item { id: string; product: Product; x: number; z: number; rot: number }
export interface Layout { home: string; rev: number; items: Item[] }
export interface Warning { item: string; with?: string; kind: string; level: 'warn' | 'info'; text: string; at?: P }

export type Kind = 'light' | 'ac' | 'lock' | 'curtain' | 'vacuum' | 'sensor';
export interface Device {
  id: string; kind: Kind; name: string; room: string; x: number; y: number; z: number; rot: number;
  width?: number; door?: string; topic: string; source: string; entity?: string;
  state: Record<string, any>; t: number;
}
export interface Hint { id: string; kind: string; level: 'info' | 'warn'; device: string; room: string; text: string; since: number }
export type Level = 'advise' | 'guarded' | 'auto';
export interface Automation { id: string; name: string; when: string; level: Level; enabled: boolean; actions: { device: string; patch: Record<string, any> }[]; trigger: { kind: string } }
export interface Proposal { id: string; automation: string; name: string; why: string; steps: string[]; status: 'pending' | 'done' | 'rejected' | 'failed'; reason?: string; by?: string; t: number }
export interface Violation { constraint: string; kind: 'structural' | 'live' | 'layout'; node: string; name: string; text: string; why: string; value?: number; since?: number }
export interface Prediction { room: string; now: number; outdoor: number; target: number; mode: string; eta: number | null; curve: { s: number; t: number }[] }
export interface Answer { text: string; nodes: string[]; rows?: { id: string; name: string; detail?: string }[] }
export interface TwinInfo {
  goals: { id: string; question: string; before: string; after: string; data: string[]; level: string }[];
  levels: { id: string; name: string; what: string; status: 'done' | 'partial' | 'next'; has: string[]; next?: string[] }[];
  model: Record<string, number>; mqtt: string;
}
export interface ModelInfo { available: boolean; ready?: boolean; url?: string; key?: string; error?: string }

/** This tab, so the server can tell which page a change came from. */
export const TAB = Math.random().toString(36).slice(2, 10);

export class ApiError extends Error {
  constructor(public status: number, message: string, public body: any) { super(message); }
}

export async function api<T>(method: string, path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method,
    headers: { 'Content-Type': 'application/json', 'X-Tab': TAB },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await res.text();
  let data: any = null;
  try { data = text ? JSON.parse(text) : null; } catch { /* not json */ }
  if (!res.ok) throw new ApiError(res.status, data?.error ?? data?.confirm ?? `出错了（${res.status}）`, data);
  return data as T;
}

export const get = <T,>(path: string) => api<T>('GET', path);
