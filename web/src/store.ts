// What the page knows and does: the home, its furniture (with undo/redo, saved to the server),
// the devices and hints the server streams, and the view's own state.
import { create } from 'zustand';
import { toast } from 'sonner';
import { api, ApiError, get, TAB, type Automation, type Device, type Hint, type Home, type Item, type Layout, type Product, type Proposal, type Violation, type Warning } from './api';

export type Mode = 'view' | 'arrange' | 'devices';
export type Panel = 'library' | 'devices' | 'auto' | 'twin' | 'assistant' | 'ha' | 'import' | null;
export type Sel = { kind: 'item' | 'device' | 'room'; id: string } | null;
export interface Drag { id: string; x: number; z: number; rot: number }
export interface CamReq { n: number; op: 'home' | 'room' | 'point' | 'intro'; id?: string; at?: [number, number] }

interface State {
  home: Home | null;
  items: Item[];
  rev: number;
  past: Item[][];
  future: Item[][];
  warnings: Warning[];
  library: Product[];
  devices: Record<string, Device>;
  hints: Hint[];
  automations: Automation[];
  proposals: Proposal[];
  /** what the ontology's constraints find: structural, live and layout */
  constraints: Violation[];
  connected: boolean;

  mode: Mode;
  panel: Panel;
  sel: Sel;
  night: boolean;
  drag: Drag | null;
  dragWarnings: Warning[] | null;
  cam: CamReq | null;
  highlight: string[];
  ghostWalls: boolean;
  /** 实景: the scanned model in place of the drawn walls and floors */
  scan: boolean;

  setMode(m: Mode): void;
  setPanel(p: Panel): void;
  select(s: Sel): void;
  setNight(n: boolean): void;
  setScan(on: boolean): void;
  camera(op: CamReq['op'], id?: string, at?: [number, number]): void;
  setItems(items: Item[], record?: boolean): void;
  undo(): void;
  redo(): void;
  place(p: Product, at?: [number, number]): void;
  updateItem(id: string, patch: Partial<Item>): void;
  removeItem(id: string): void;
  setDrag(d: Drag | null): void;
  command(id: string, patch: Record<string, unknown>, confirm?: boolean): Promise<boolean>;
}

let saveTimer: ReturnType<typeof setTimeout> | undefined;
let pendingSave = false;
let checkTimer: ReturnType<typeof setTimeout> | undefined;
let checking = false;

function save() {
  clearTimeout(saveTimer);
  pendingSave = true;
  saveTimer = setTimeout(async () => {
    pendingSave = false;
    const { items, rev } = useStore.getState();
    try {
      const r = await api<{ layout: Layout; warnings: Warning[] }>('PUT', 'api/layout', { items, base: rev });
      useStore.setState({ rev: r.layout.rev, warnings: r.warnings ?? [] });
    } catch (e) {
      if (e instanceof ApiError && e.status === 409) {
        // someone else saved first: take theirs
        const l = e.body.layout as Layout;
        useStore.setState({ items: l.items, rev: l.rev, past: [], future: [] });
        toast.warning('布局在别的页面改过了，已经换成最新的');
        refreshWarnings();
      } else toast.error(`布局没保存上：${(e as Error).message}`);
    }
  }, 350);
}

// a change still waiting to be saved goes out as the page closes
if (typeof window !== 'undefined') {
  window.addEventListener('pagehide', () => {
    if (!saveTimer || !pendingSave) return;
    clearTimeout(saveTimer);
    const { items, rev } = useStore.getState();
    fetch('api/layout', { method: 'PUT', keepalive: true, headers: { 'Content-Type': 'application/json', 'X-Tab': TAB }, body: JSON.stringify({ items, base: rev }) });
  });
}

async function refreshWarnings() {
  const r = await get<{ warnings: Warning[] }>('api/layout');
  useStore.setState({ warnings: r.warnings ?? [] });
}

/** Checks the layout with the item being dragged where it is now, a few times a second. */
function liveCheck() {
  if (checking) return;
  clearTimeout(checkTimer);
  checkTimer = setTimeout(async () => {
    const { drag, items } = useStore.getState();
    if (!drag) return;
    checking = true;
    try {
      const moved = items.map((it) => (it.id === drag.id ? { ...it, x: drag.x, z: drag.z, rot: drag.rot } : it));
      const r = await api<{ warnings: Warning[] }>('POST', 'api/layout/check', { items: moved });
      if (useStore.getState().drag) useStore.setState({ dragWarnings: r.warnings ?? [] });
    } catch { /* the next move tries again */ } finally {
      checking = false;
    }
  }, 120);
}

const uid = () => Math.random().toString(36).slice(2, 9);

export const useStore = create<State>((set, getState) => ({
  home: null, items: [], rev: 0, past: [], future: [], warnings: [], library: [],
  devices: {}, hints: [], automations: [], proposals: [], constraints: [], connected: false,
  mode: 'view', panel: null, sel: null, night: false, drag: null, dragWarnings: null, cam: null, highlight: [], ghostWalls: true, scan: false,

  setMode: (mode) => set({ mode, sel: getState().sel?.kind === 'room' ? getState().sel : null }),
  setPanel: (panel) => set({ panel, highlight: [] }),
  select: (sel) => set({ sel, highlight: [] }),
  setNight: (night) => set({ night }),
  setScan: (scan) => set({ scan }),
  camera: (op, id, at) => set((s) => ({ cam: { n: (s.cam?.n ?? 0) + 1, op, id, at } })),
  setItems(items, record = true) {
    const s = getState();
    set({ items, past: record ? [...s.past.slice(-60), s.items] : s.past, future: record ? [] : s.future });
    save();
  },
  undo() {
    const s = getState();
    const prev = s.past[s.past.length - 1];
    if (!prev) return;
    set({ items: prev, past: s.past.slice(0, -1), future: [s.items, ...s.future] });
    save();
  },
  redo() {
    const s = getState();
    const next = s.future[0];
    if (!next) return;
    set({ items: next, past: [...s.past, s.items], future: s.future.slice(1) });
    save();
  },
  place(p, at) {
    const s = getState();
    const h = s.home;
    let x = 6.0, z = 5.4;
    if (at) [x, z] = at;
    else if (s.sel?.kind === 'room' && h) {
      const r = h.rooms.find((r) => r.id === s.sel!.id);
      if (r) {
        const xs = r.poly.map((p) => p[0]), zs = r.poly.map((p) => p[1]);
        x = (Math.min(...xs) + Math.max(...xs)) / 2;
        z = (Math.min(...zs) + Math.max(...zs)) / 2;
      }
    }
    const it: Item = { id: uid(), product: p, x, z, rot: 0 };
    s.setItems([...s.items, it]);
    set({ mode: 'arrange', sel: { kind: 'item', id: it.id }, panel: null });
  },
  updateItem(id, patch) {
    const s = getState();
    s.setItems(s.items.map((it) => (it.id === id ? { ...it, ...patch } : it)));
  },
  removeItem(id) {
    const s = getState();
    s.setItems(s.items.filter((it) => it.id !== id));
    if (s.sel?.id === id) set({ sel: null });
  },
  setDrag(drag) {
    set(drag ? { drag } : { drag: null, dragWarnings: null });
    if (drag) liveCheck();
  },
  async command(id, patch, confirm = false) {
    // show the change at once; the server's answer (or the stream) settles it
    const before = getState().devices[id];
    if (before) set((s) => ({ devices: { ...s.devices, [id]: { ...before, state: { ...before.state, ...patch } } } }));
    try {
      const d = await api<Device>('POST', `api/devices/${id}/command`, { patch, confirm });
      set((s) => ({ devices: { ...s.devices, [id]: d } }));
      return true;
    } catch (e) {
      if (before) set((s) => ({ devices: { ...s.devices, [id]: before } }));
      if (e instanceof ApiError && e.status === 409 && e.body?.confirm) {
        useConfirm.getState().ask(e.body.confirm, () => getState().command(id, patch, true));
        return false;
      }
      toast.error((e as Error).message);
      return false;
    }
  },
}));

/** A question that needs a yes before a change goes out (unlocking). */
export const useConfirm = create<{ text: string | null; yes: (() => void) | null; ask(text: string, yes: () => void): void; close(): void }>((set) => ({
  text: null, yes: null,
  ask: (text, yes) => set({ text, yes }),
  close: () => set({ text: null, yes: null }),
}));

export async function boot() {
  const [home, lay, lib] = await Promise.all([
    get<Home>('api/home'),
    get<{ layout: Layout; warnings: Warning[] }>('api/layout'),
    get<Product[]>('api/library'),
  ]);
  get<Violation[]>('api/constraints').then((c) => useStore.setState({ constraints: c ?? [] })).catch(() => {});
  useStore.setState({ home, items: lay.layout.items, rev: lay.layout.rev, warnings: lay.warnings ?? [], library: lib ?? [] });
  connect();
}

let retry = 500;
function connect() {
  const ws = new WebSocket(new URL('api/ws', location.href).href.replace(/^http/, 'ws'));
  ws.onopen = () => { retry = 500; useStore.setState({ connected: true }); };
  ws.onclose = () => {
    useStore.setState({ connected: false });
    setTimeout(connect, retry);
    retry = Math.min(8000, retry * 2);
  };
  ws.onmessage = (ev) => {
    const m = JSON.parse(ev.data);
    switch (m.type) {
      case 'snapshot': {
        const devices: Record<string, Device> = {};
        for (const d of m.devices as Device[]) devices[d.id] = d;
        useStore.setState({ devices, hints: m.hints ?? [], automations: m.automations ?? [], proposals: m.proposals ?? [] });
        const s = useStore.getState();
        if (m.layout.rev !== s.rev) useStore.setState({ items: m.layout.items, rev: m.layout.rev });
        break;
      }
      case 'constraints': {
        const prev = new Set(useStore.getState().constraints.map((v) => v.constraint + v.node));
        useStore.setState({ constraints: m.constraints ?? [] });
        for (const v of (m.constraints ?? []) as Violation[]) if (v.kind === 'live' && !prev.has(v.constraint + v.node)) toast.warning(v.text, { description: v.why, duration: 8000 });
        break;
      }
      case 'device':
        useStore.setState((s) => ({ devices: { ...s.devices, [m.device.id]: m.device } }));
        break;
      case 'removed':
        useStore.setState((s) => { const d = { ...s.devices }; delete d[m.id]; return { devices: d }; });
        break;
      case 'layout': {
        if (m.by === TAB) break;
        const s = useStore.getState();
        if (m.layout.rev > s.rev) {
          useStore.setState({ items: m.layout.items, rev: m.layout.rev });
          refreshWarnings();
        }
        break;
      }
      case 'hints':
        useStore.setState({ hints: m.hints ?? [] });
        if (m.started) (m.started.level === 'warn' ? toast.warning : toast.info)(m.started.text, { duration: 6000 });
        break;
      case 'automations': {
        const prev = new Set(useStore.getState().proposals.map((p) => p.id));
        useStore.setState({ automations: m.automations ?? [], proposals: m.proposals ?? [] });
        for (const p of m.proposals as Proposal[]) {
          if (prev.has(p.id)) continue;
          if (p.status === 'pending') toast(`「${p.name}」等你确认`, { description: p.reason, action: { label: '查看', onClick: () => useStore.getState().setPanel('auto') }, duration: 8000 });
          else if (p.status === 'done') toast.success(`已执行「${p.name}」`, { description: p.steps.join('，') });
        }
        break;
      }
    }
  };
}
