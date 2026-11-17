// The card of the selected device: what it is doing and what it can be told to do.
// Wide screens: a frosted card floating next to the device. Phones: a bottom sheet.
import { useEffect, useState } from 'react';
import { Battery, DoorClosed, DoorOpen, Droplets, Lock, LockOpen, Minus, Plus, X } from 'lucide-react';
import { get, type Device, type Prediction } from '@/api';
import { Slider } from '@/components/ui/slider';
import { cn } from '@/lib/utils';
import { useStore } from '@/store';
import { DeviceIcon, FAN_NAME, MODE_NAME, PHASE_NAME, Switch, summary } from './bits';

const CT = [
  { k: 2700, c: '#ffb46b', name: '暖黄' },
  { k: 3000, c: '#ffc887', name: '暖白' },
  { k: 4000, c: '#ffe7c9', name: '自然' },
  { k: 5000, c: '#f4f1ff', name: '冷白' },
  { k: 6500, c: '#dfe9ff', name: '日光' },
];

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="grid gap-2">
      <small className="text-[12px] font-semibold text-muted-foreground">{label}</small>
      {children}
    </div>
  );
}

function Seg<T extends string>({ value, options, onChange }: { value: T; options: [T, string][]; onChange(v: T): void }) {
  return (
    <div className="flex rounded-xl bg-black/[0.05] p-1">
      {options.map(([v, l]) => (
        <button key={v} type="button" onClick={() => onChange(v)} className={cn('h-8 flex-1 rounded-lg text-[12.5px] font-bold transition-colors', value === v ? 'bg-white text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground')}>
          {l}
        </button>
      ))}
    </div>
  );
}

function BrightnessRow({ d }: { d: Device }) {
  const command = useStore((s) => s.command);
  const [v, setV] = useState<number>(d.state.brightness ?? 80);
  useEffect(() => setV(d.state.brightness ?? 80), [d.state.brightness]);
  return (
    <Row label="亮度">
      <div className="grid grid-cols-[1fr_auto] items-center gap-3">
        <Slider min={1} max={100} step={1} value={[v]} onValueChange={([x]) => setV(x)} onValueCommit={([x]) => command(d.id, { brightness: x, on: true })} />
        <output className="w-11 text-right text-[15px] font-extrabold tabular-nums">{v}%</output>
      </div>
    </Row>
  );
}

function fmtMin(s: number) {
  if (s < 60) return '1 分钟内';
  const m = Math.round(s / 60);
  return m < 90 ? `约 ${m} 分钟` : `约 ${(m / 60).toFixed(1)} 小时`;
}

function Curve({ p }: { p: Prediction }) {
  const pts = p.curve;
  if (pts.length < 2) return null;
  const ts = pts.map((x) => x.t);
  const lo = Math.min(...ts, p.target) - 0.3, hi = Math.max(...ts, p.target) + 0.3;
  const W = 260, H = 54;
  const x = (s: number) => (s / pts[pts.length - 1].s) * W;
  const y = (t: number) => H - ((t - lo) / (hi - lo)) * H;
  const d = pts.map((q, i) => `${i ? 'L' : 'M'}${x(q.s).toFixed(1)},${y(q.t).toFixed(1)}`).join(' ');
  return (
    <svg viewBox={`0 0 ${W} ${H + 14}`} className="w-full">
      <line x1={0} x2={W} y1={y(p.target)} y2={y(p.target)} stroke="#d8692f" strokeDasharray="3 3" strokeWidth={1} />
      <path d={d} fill="none" stroke="#2f7de1" strokeWidth={2} />
      <text x={0} y={H + 12} fontSize={10} fill="#6b6e75">现在 {p.now}℃</text>
      <text x={W} y={H + 12} fontSize={10} fill="#6b6e75" textAnchor="end">1 小时后</text>
    </svg>
  );
}

function ACControls({ d }: { d: Device }) {
  const command = useStore((s) => s.command);
  const s = d.state;
  const [target, setTarget] = useState<number>(s.target ?? 24);
  const [pred, setPred] = useState<Prediction | null>(null);
  useEffect(() => setTarget(s.target ?? 24), [s.target]);
  // the twin's room model: how long to the temperature asked for
  useEffect(() => {
    let alive = true;
    const load = () => get<Prediction>(`/api/predict/${d.id}?target=${target}&mode=${s.mode ?? 'cool'}`).then((p) => alive && setPred(p)).catch(() => {});
    load();
    const t = setInterval(load, 5000);
    return () => { alive = false; clearInterval(t); };
  }, [d.id, target, s.mode, s.on]);
  const bump = (dv: number) => {
    const v = Math.max(16, Math.min(30, target + dv));
    setTarget(v);
    command(d.id, { target: v });
  };
  const verb = s.mode === 'heat' ? '升到' : '降到';
  return (
    <>
      <div className="flex items-center justify-between rounded-2xl bg-white/60 px-3 py-2">
        <button type="button" onClick={() => bump(-0.5)} className="grid size-10 place-items-center rounded-full bg-white shadow-sm" aria-label="调低"><Minus className="size-4" /></button>
        <div className="text-center">
          <div className="text-[34px] font-extrabold leading-none tabular-nums">{target.toFixed(1)}<span className="text-[16px]">℃</span></div>
          <small className="text-[11.5px] text-muted-foreground">室内 {pred?.now ?? s.current ?? '–'}℃ · 室外 {pred?.outdoor ?? '–'}℃</small>
        </div>
        <button type="button" onClick={() => bump(0.5)} className="grid size-10 place-items-center rounded-full bg-white shadow-sm" aria-label="调高"><Plus className="size-4" /></button>
      </div>
      {pred && (pred.mode === 'cool' || pred.mode === 'heat') && (
        <div className="rounded-2xl bg-white/50 px-3 py-2">
          <p className="text-[12.5px] font-semibold">
            {pred.eta === 0 ? `已经在 ${target}℃ 了` : pred.eta === null ? `${verb} ${target}℃ 有点难：空调和室外在 ${verb === '降到' ? '更高' : '更低'}的温度上平衡` : `${s.on ? '' : '现在打开，'}${fmtMin(pred.eta)}${verb} ${target}℃`}
          </p>
          <Curve p={pred} />
        </div>
      )}
      <Row label="模式">
        <Seg value={s.mode ?? 'cool'} options={Object.entries(MODE_NAME) as [string, string][]} onChange={(m) => command(d.id, { mode: m })} />
      </Row>
      <Row label="风速">
        <Seg value={s.fan ?? 'auto'} options={Object.entries(FAN_NAME) as [string, string][]} onChange={(f) => command(d.id, { fan: f })} />
      </Row>
    </>
  );
}

export function DeviceControls({ d }: { d: Device }) {
  const command = useStore((s) => s.command);
  const s = d.state;
  switch (d.kind) {
    case 'light':
      return (
        <>
          <BrightnessRow d={d} />
          <Row label="色温">
            <div className="flex gap-2.5">
              {CT.map((c) => (
                <button key={c.k} type="button" title={`${c.name} ${c.k}K`} onClick={() => command(d.id, { ct: c.k, on: true })}
                  className={cn('size-9 rounded-full border border-black/5 shadow-sm transition-transform', Math.abs((s.ct ?? 3000) - c.k) < 1 && 'ring-2 ring-primary ring-offset-2 ring-offset-white/60')}
                  style={{ background: c.c }} aria-label={c.name} />
              ))}
            </div>
          </Row>
        </>
      );
    case 'ac':
      return <ACControls d={d} />;
    case 'lock':
      return (
        <>
          <div className="grid grid-cols-2 gap-2">
            <div className={cn('rounded-2xl px-3 py-2.5', s.locked ? 'bg-success-soft text-success' : 'bg-warning-soft text-warning')}>
              {s.locked ? <Lock className="size-5" /> : <LockOpen className="size-5" />}
              <b className="mt-1 block text-[15px]">{s.locked ? '已上锁' : '没锁'}</b>
            </div>
            <div className={cn('rounded-2xl px-3 py-2.5', s.open ? 'bg-warning-soft text-warning' : 'bg-white/60 text-foreground')}>
              {s.open ? <DoorOpen className="size-5" /> : <DoorClosed className="size-5" />}
              <b className="mt-1 block text-[15px]">{s.open ? '门开着' : '门关着'}</b>
            </div>
          </div>
          <div className="flex items-center gap-2 text-[12px] text-muted-foreground"><Battery className="size-4" />电量 {Math.round(s.battery ?? 0)}%</div>
          {s.locked
            ? <button type="button" onClick={() => command(d.id, { locked: false })} className="h-10 rounded-xl bg-foreground text-[13.5px] font-bold text-white">开锁</button>
            : <button type="button" disabled={!!s.open} onClick={() => command(d.id, { locked: true })} className="h-10 rounded-xl bg-primary text-[13.5px] font-bold text-white disabled:opacity-50">{s.open ? '门开着，关上才能锁' : '上锁'}</button>}
        </>
      );
    case 'curtain': {
      return <CurtainControls d={d} />;
    }
    case 'vacuum': {
      const cleaning = s.phase === 'cleaning';
      return (
        <>
          <div className="grid grid-cols-2 gap-2">
            <div className="rounded-2xl bg-white/60 px-3 py-2"><small className="text-[11.5px] text-muted-foreground">状态</small><b className="block text-[15px]">{PHASE_NAME[s.phase] ?? s.phase}</b></div>
            <div className="rounded-2xl bg-white/60 px-3 py-2"><small className="text-[11.5px] text-muted-foreground">电量</small><b className="block text-[15px] tabular-nums">{Math.round(s.battery)}%</b>
              <div className="mt-1 h-1.5 overflow-hidden rounded-full bg-black/10"><i className="block h-full rounded-full bg-success" style={{ width: `${s.battery}%` }} /></div></div>
          </div>
          {(cleaning || s.phase === 'paused') && <p className="text-[12.5px] text-muted-foreground">已经扫了 {Math.round((s.progress ?? 0) * 100)}%，路线在地上亮着</p>}
          <div className="grid grid-cols-3 gap-2">
            <button type="button" onClick={() => command(d.id, { phase: 'cleaning' })} disabled={cleaning} className="h-10 rounded-xl bg-primary text-[13px] font-bold text-white disabled:opacity-40">{s.phase === 'paused' ? '继续' : '开始打扫'}</button>
            <button type="button" onClick={() => command(d.id, { phase: 'paused' })} disabled={!cleaning} className="h-10 rounded-xl bg-white text-[13px] font-bold shadow-sm disabled:opacity-40">暂停</button>
            <button type="button" onClick={() => command(d.id, { phase: 'returning' })} disabled={s.phase === 'docked' || s.phase === 'returning'} className="h-10 rounded-xl bg-white text-[13px] font-bold shadow-sm disabled:opacity-40">回座充电</button>
          </div>
        </>
      );
    }
    case 'sensor':
      return (
        <div className="grid grid-cols-2 gap-2">
          <div className="rounded-2xl bg-white/60 px-3 py-2.5"><small className="text-[11.5px] text-muted-foreground">温度</small><b className="block text-[26px] font-extrabold tabular-nums">{Number(s.temp).toFixed(1)}<span className="text-[14px]">℃</span></b></div>
          <div className="rounded-2xl bg-white/60 px-3 py-2.5"><small className="flex items-center gap-1 text-[11.5px] text-muted-foreground"><Droplets className="size-3" />湿度</small><b className="block text-[26px] font-extrabold tabular-nums">{Math.round(s.humidity)}<span className="text-[14px]">%</span></b></div>
        </div>
      );
  }
  return null;
}

function CurtainControls({ d }: { d: Device }) {
  const command = useStore((s) => s.command);
  const [v, setV] = useState<number>(Math.round(d.state.target ?? d.state.position ?? 0));
  useEffect(() => setV(Math.round(d.state.target ?? d.state.position ?? 0)), [d.state.target]);
  return (
    <>
      <Row label="打开">
        <div className="grid grid-cols-[1fr_auto] items-center gap-3">
          <Slider min={0} max={100} step={5} value={[v]} onValueChange={([x]) => setV(x)} onValueCommit={([x]) => command(d.id, { position: x })} />
          <output className="w-11 text-right text-[15px] font-extrabold tabular-nums">{v}%</output>
        </div>
      </Row>
      <div className="grid grid-cols-2 gap-2">
        <button type="button" onClick={() => command(d.id, { position: 100 })} className="h-10 rounded-xl bg-white text-[13px] font-bold shadow-sm">全部拉开</button>
        <button type="button" onClick={() => command(d.id, { position: 0 })} className="h-10 rounded-xl bg-white text-[13px] font-bold shadow-sm">全部拉上</button>
      </div>
    </>
  );
}

/** Whether a device has an on/off switch in its card's header. */
const hasPower = (d: Device) => d.kind === 'light' || d.kind === 'ac';

export function DeviceCardBody({ d, onClose }: { d: Device; onClose(): void }) {
  const command = useStore((s) => s.command);
  const home = useStore((s) => s.home);
  const room = home?.rooms.find((r) => r.id === d.room)?.name ?? (d.room === 'outdoors' ? '室外' : d.room);
  return (
    <div className="grid gap-3.5">
      <header className="flex items-center gap-3">
        <DeviceIcon d={d} />
        <div className="min-w-0 flex-1">
          <h3 className="truncate text-[15.5px] font-extrabold">{d.name} <span className="font-semibold text-muted-foreground">· {room}</span></h3>
          <p className="truncate text-[12px] text-muted-foreground">{summary(d)}</p>
        </div>
        {hasPower(d) && <Switch on={d.state.on === true} onChange={(v) => command(d.id, { on: v })} label="开关" />}
        <button type="button" onClick={onClose} className="grid size-8 place-items-center rounded-full bg-white/70 hover:bg-white" aria-label="关闭"><X className="size-4" /></button>
      </header>
      <DeviceControls d={d} />
      <footer className="flex items-center justify-between border-t border-black/5 pt-2 text-[10.5px] text-muted-foreground">
        <code className="truncate">{d.topic}</code>
        <span className={d.source === 'ha' ? 'font-bold text-[#1f78c8]' : ''}>{d.source === 'sim' ? '模拟设备' : d.source === 'ha' ? '实测 · Home Assistant' : d.source}</span>
      </footer>
    </div>
  );
}
