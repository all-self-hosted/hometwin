// What is selected: a piece of furniture (size, warnings, turn, remove), a room (its devices
// and furniture), or a device (its card).
import { Html } from '@react-three/drei';
import { CircleAlert, Copy, Info, RotateCcw, RotateCw, Sofa, Trash } from 'lucide-react';
import type { Device, Item } from '@/api';
import { area } from '@/geom';
import { cn } from '@/lib/utils';
import { useStore } from '@/store';
import { DeviceCardBody } from './DeviceCard';
import { DeviceIcon, Panel, Switch, isOn, money, sizeText, summary, useNarrow } from './bits';
import { ProductCard } from './Library';

const MEASURE: Record<string, string> = {
  seatWidth: '座宽', seatDepth: '座深', seatHeight: '座高', armrestHeight: '扶手高', armrestWidth: '扶手宽', backrestHeight: '靠背高',
  headboardHeight: '床头高', footboardHeight: '床尾高', mattressWidth: '床垫宽', mattressLength: '床垫长', heightUnderFurniture: '底下净高',
  maxLoad: '最大承重', maxLoadShelf: '每层承重', maxLoadDrawer: '每个抽屉承重', shelfWidth: '搁板宽', shelfDepth: '搁板深',
  drawerDepthInside: '抽屉内深', tableTopHeight: '桌面高', length: '长', diameter: '直径',
};

export function turn(it: Item, by: number) {
  const r = (((it.rot + by) % 360) + 360) % 360;
  useStore.getState().updateItem(it.id, { rot: r });
}

function ItemPanel({ it }: { it: Item }) {
  const all = useStore((s) => (s.drag?.id === it.id ? s.dragWarnings ?? s.warnings : s.warnings));
  const warnings = all.filter((w) => w.item === it.id || w.with === it.id);
  const home = useStore((s) => s.home);
  const mode = useStore((s) => s.mode);
  const p = it.product;
  const room = home?.rooms.find((r) => {
    const pts = r.poly;
    let ins = false;
    for (let i = 0, j = pts.length - 1; i < pts.length; j = i++) {
      const a = pts[i], b = pts[j];
      if ((a[1] > it.z) !== (b[1] > it.z) && it.x < ((b[0] - a[0]) * (it.z - a[1])) / (b[1] - a[1]) + a[0]) ins = !ins;
    }
    return ins;
  });
  const s = useStore.getState();
  const narrow = useNarrow();
  const actions = (
      <div className="grid grid-cols-4 gap-1.5">
          <button type="button" onClick={() => turn(it, -90)} className="flex h-10 flex-col items-center justify-center rounded-xl bg-white text-[11px] font-bold shadow-sm"><RotateCcw className="size-4" />转 90°</button>
          <button type="button" onClick={() => turn(it, 15)} className="flex h-10 flex-col items-center justify-center rounded-xl bg-white text-[11px] font-bold shadow-sm"><RotateCw className="size-4" />转 15°</button>
          <button type="button" onClick={() => { const c = { ...it, id: Math.random().toString(36).slice(2, 9), x: it.x + 0.3, z: it.z + 0.3 }; s.setItems([...s.items, c]); s.select({ kind: 'item', id: c.id }); }} className="flex h-10 flex-col items-center justify-center rounded-xl bg-white text-[11px] font-bold shadow-sm"><Copy className="size-4" />再来一件</button>
          <button type="button" onClick={() => s.removeItem(it.id)} className="flex h-10 flex-col items-center justify-center rounded-xl bg-white text-[11px] font-bold text-destructive shadow-sm"><Trash className="size-4" />拿走</button>
        </div>
  );
  const extra = (p.measures ?? []).filter((m) => !['width', 'depth', 'height', 'length'].includes(m.key)).slice(0, 6);
  return (
    <Panel side="right" title={p.family ? `${p.family} ${p.typeLabel}` : p.name} sub={`${room?.name ?? '房间外'} · ${sizeText(p)}`}
      icon={<span className="grid size-9 place-items-center rounded-xl bg-[#fff1df] text-primary"><Sofa className="size-5" /></span>}
      onClose={() => s.select(null)} peeked={narrow && mode === 'arrange'} peekContent={actions}>
      <div className="grid gap-3">
        <ProductCard p={p} />
        {extra.length > 0 && (
          <dl className="grid grid-cols-2 gap-1.5 text-[12px]">
            {extra.map((m) => (
              <div key={m.key} className="rounded-xl bg-white/60 px-2.5 py-1.5"><dt className="text-[11px] text-muted-foreground">{MEASURE[m.key] ?? m.name}</dt><dd className="font-bold">{m.kg ? `${m.kg} kg` : `${Math.round(m.cm)} cm`}</dd></div>
            ))}
          </dl>
        )}
        <div className="grid gap-1.5">
          {warnings.length === 0 && <p className="rounded-xl bg-success-soft px-3 py-2 text-[12.5px] font-semibold text-success">放得下，没发现问题</p>}
          {warnings.map((w, i) => (
            <p key={i} className={cn('flex items-start gap-2 rounded-xl px-3 py-2 text-[12.5px] font-semibold', w.level === 'warn' ? 'bg-danger-soft text-destructive' : 'bg-warning-soft text-warning')}>
              {w.level === 'warn' ? <CircleAlert className="mt-px size-4 shrink-0" /> : <Info className="mt-px size-4 shrink-0" />}
              <span>{w.item !== it.id ? `${s.items.find((x) => x.id === w.item)?.product.family ?? '另一件'}：` : ''}{w.text}</span>
            </p>
          ))}
        </div>
        {actions}
        {mode !== 'arrange'
          ? <button type="button" onClick={() => s.setMode('arrange')} className="h-9 rounded-xl bg-foreground text-[12.5px] font-bold text-white">进入布置，拖动它</button>
          : <p className="text-[11.5px] text-muted-foreground">{narrow ? '用一根手指在地上拖动它，靠近墙和别的家具会贴齐。' : '在地上拖动它，靠近墙和别的家具会贴齐；按住 Alt 不贴齐。Q / E 转 90°，Shift+Q / E 转 15°。'}{p.price ? ` 价格 ${money(p.price, p.currency)}。` : ''}</p>}
      </div>
    </Panel>
  );
}

function RoomPanel({ id }: { id: string }) {
  const home = useStore((s) => s.home)!;
  const devices = useStore((s) => s.devices);
  const items = useStore((s) => s.items);
  const command = useStore((s) => s.command);
  const r = home.rooms.find((x) => x.id === id);
  if (!r) return null;
  const devs = Object.values(devices).filter((d) => d.room === id);
  const sensor = devs.find((d) => d.kind === 'sensor');
  const inRoom = items.filter((it) => {
    let ins = false;
    for (let i = 0, j = r.poly.length - 1; i < r.poly.length; j = i++) {
      const a = r.poly[i], b = r.poly[j];
      if ((a[1] > it.z) !== (b[1] > it.z) && it.x < ((b[0] - a[0]) * (it.z - a[1])) / (b[1] - a[1]) + a[0]) ins = !ins;
    }
    return ins;
  });
  const s = useStore.getState();
  return (
    <Panel side="right" title={r.name} sub={`${area(r.poly).toFixed(1)} ㎡${sensor ? ` · ${Number(sensor.state.temp).toFixed(1)}℃ · 湿度 ${Math.round(sensor.state.humidity)}%` : ''}`} onClose={() => { s.select(null); s.camera('home'); }}>
      <div className="grid gap-2">
        {devs.filter((d) => d.kind !== 'sensor').map((d) => (
          <div key={d.id} className="flex items-center gap-2.5 rounded-2xl bg-white/70 p-2">
            <button type="button" className="flex min-w-0 flex-1 items-center gap-2.5 text-left" onClick={() => s.select({ kind: 'device', id: d.id })}>
              <DeviceIcon d={d} size="sm" />
              <span className="min-w-0"><b className="block truncate text-[13px]">{d.name}</b><small className="block truncate text-[11.5px] text-muted-foreground">{summary(d)}</small></span>
            </button>
            {(d.kind === 'light' || d.kind === 'ac') && <Switch on={isOn(d)} onChange={(v) => command(d.id, { on: v })} />}
          </div>
        ))}
        <h4 className="mt-2 text-[12px] font-bold text-muted-foreground">家具 {inRoom.length} 件</h4>
        {inRoom.map((it) => (
          <button key={it.id} type="button" onClick={() => s.select({ kind: 'item', id: it.id })} className="flex items-center justify-between rounded-xl bg-white/60 px-3 py-2 text-left text-[12.5px]">
            <b>{it.product.family ? `${it.product.family} ${it.product.typeLabel}` : it.product.name}</b>
            <span className="text-muted-foreground tabular-nums">{sizeText(it.product)}</span>
          </button>
        ))}
        <button type="button" onClick={() => s.setPanel('library')} className="mt-1 h-9 rounded-xl bg-foreground text-[12.5px] font-bold text-white">往{r.name}放家具</button>
      </div>
    </Panel>
  );
}

/** The device card floating by the device (wide screens), drawn inside the 3D view. */
export function DeviceCard3D() {
  const sel = useStore((s) => s.sel);
  const d = useStore((s) => (s.sel?.kind === 'device' ? s.devices[s.sel.id] : undefined));
  const narrow = useNarrow();
  if (!sel || sel.kind !== 'device' || !d || narrow) return null;
  return (
    <Html position={[d.x, d.y, d.z]} zIndexRange={[40, 30]} style={{ pointerEvents: 'auto' }}>
      <div className="glass w-[318px] -translate-y-1/2 translate-x-8 rounded-[22px] p-4 animate-in fade-in zoom-in-95 duration-150" onPointerDown={(e) => e.stopPropagation()}>
        <DeviceCardBody d={d} onClose={() => useStore.getState().select(null)} />
      </div>
    </Html>
  );
}

function DeviceSheet({ d }: { d: Device }) {
  return (
    <section className="glass-dim fixed inset-x-0 bottom-0 z-40 rounded-t-3xl px-4 pb-[calc(16px+env(safe-area-inset-bottom))] pt-3 animate-in slide-in-from-bottom-8 duration-200">
      <i className="mx-auto mb-3 block h-1.5 w-10 rounded-full bg-[#cfc9bf]" />
      <DeviceCardBody d={d} onClose={() => useStore.getState().select(null)} />
    </section>
  );
}

export function Selection() {
  const sel = useStore((s) => s.sel);
  const it = useStore((s) => (s.sel?.kind === 'item' ? s.items.find((x) => x.id === s.sel!.id) : undefined));
  const d = useStore((s) => (s.sel?.kind === 'device' ? s.devices[s.sel.id] : undefined));
  const narrow = useNarrow();
  if (!sel) return null;
  if (sel.kind === 'item' && it) return <ItemPanel it={it} />;
  if (sel.kind === 'room') return <RoomPanel id={sel.id} />;
  if (sel.kind === 'device' && d && narrow) return <DeviceSheet d={d} />;
  return null;
}
