// Small shared pieces: a switch, the floating panel that becomes a bottom sheet on phones,
// device icons and words.
import { useEffect, useState, type ReactNode } from 'react';
import { AirVent, Blinds, Bot, Lightbulb, Lock, Thermometer, X } from 'lucide-react';
import type { Device, Kind } from '@/api';
import { cn } from '@/lib/utils';

export function Switch({ on, onChange, label }: { on: boolean; onChange(v: boolean): void; label?: string }) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={on}
      aria-label={label}
      onClick={() => onChange(!on)}
      className={cn('relative h-[26px] w-[46px] shrink-0 rounded-full transition-colors', on ? 'bg-primary' : 'bg-[#d9d4cc]')}
    >
      <i className={cn('absolute top-[3px] size-5 rounded-full bg-white shadow transition-[left]', on ? 'left-[23px]' : 'left-[3px]')} />
    </button>
  );
}

export function useNarrow() {
  const q = '(max-width: 767px)';
  const [narrow, setNarrow] = useState(() => window.matchMedia(q).matches);
  useEffect(() => {
    const m = window.matchMedia(q);
    const on = () => setNarrow(m.matches);
    m.addEventListener('change', on);
    return () => m.removeEventListener('change', on);
  }, []);
  return narrow;
}

/**
 * A panel: a frosted card floating at one side on wide screens, a bottom sheet on phones that
 * can be pulled down to its title.
 */
export function Panel({ title, sub, icon, onClose, children, side = 'left', wide, peeked, peekContent }: { title: ReactNode; sub?: ReactNode; icon?: ReactNode; onClose(): void; children: ReactNode; side?: 'left' | 'right'; wide?: boolean; peeked?: boolean; peekContent?: ReactNode }) {
  const narrow = useNarrow();
  const [peek, setPeek] = useState(!!peeked);
  if (narrow) {
    return (
      <section className="glass-dim fixed inset-x-0 bottom-0 z-40 flex max-h-[72dvh] flex-col rounded-t-3xl pb-[env(safe-area-inset-bottom)] animate-in slide-in-from-bottom-8 duration-200">
        <button type="button" className="flex w-full justify-center pb-1 pt-2.5" onClick={() => setPeek(!peek)} aria-label={peek ? '展开' : '收起'}>
          <i className="h-1.5 w-10 rounded-full bg-[#cfc9bf]" />
        </button>
        <header className="flex items-center gap-2.5 px-4 pb-2">
          {icon}
          <div className="min-w-0 flex-1">
            <h2 className="truncate text-[15px] font-extrabold">{title}</h2>
            {sub && <p className="truncate text-[12px] text-muted-foreground">{sub}</p>}
          </div>
          <button type="button" onClick={onClose} className="grid size-8 place-items-center rounded-full bg-secondary" aria-label="关闭"><X className="size-4" /></button>
        </header>
        {peek ? peekContent && <div className="px-4 pb-3">{peekContent}</div> : <div className="no-scrollbar min-h-0 flex-1 overflow-y-auto px-4 pb-4">{children}</div>}
      </section>
    );
  }
  return (
    <section className={cn('glass absolute top-[76px] z-30 flex max-h-[calc(100dvh-96px)] flex-col rounded-[22px] animate-in fade-in slide-in-from-top-2 duration-200', side === 'left' ? 'left-4' : 'right-4', wide ? 'w-[400px]' : 'w-[360px]')}>
      <header className="flex items-center gap-2.5 px-4 pb-2 pt-4">
        {icon}
        <div className="min-w-0 flex-1">
          <h2 className="truncate text-[15px] font-extrabold">{title}</h2>
          {sub && <p className="truncate text-[12px] text-muted-foreground">{sub}</p>}
        </div>
        <button type="button" onClick={onClose} className="grid size-8 place-items-center rounded-full bg-white/70 hover:bg-white" aria-label="关闭"><X className="size-4" /></button>
      </header>
      <div className="no-scrollbar min-h-0 flex-1 overflow-y-auto px-4 pb-4">{children}</div>
    </section>
  );
}

export const KIND_ICON: Record<Kind, typeof Lightbulb> = { light: Lightbulb, ac: AirVent, lock: Lock, curtain: Blinds, vacuum: Bot, sensor: Thermometer };
export const KIND_NAME: Record<Kind, string> = { light: '灯', ac: '空调', lock: '门锁', curtain: '窗帘', vacuum: '扫地机器人', sensor: '温湿度' };
export const MODE_NAME: Record<string, string> = { cool: '制冷', heat: '制热', dry: '除湿', fan: '送风', auto: '自动' };
export const FAN_NAME: Record<string, string> = { auto: '自动', low: '低', mid: '中', high: '高' };
export const PHASE_NAME: Record<string, string> = { docked: '在座上', cleaning: '打扫中', returning: '回座中', paused: '暂停' };

/** One line on what a device is doing now. */
export function summary(d: Device): string {
  const s = d.state;
  switch (d.kind) {
    case 'light': return s.on ? `开 · ${s.brightness}%` : '关';
    case 'ac': return s.on ? `${MODE_NAME[s.mode] ?? s.mode} ${s.target}℃${s.current !== undefined ? ` · 室内 ${s.current}℃` : ''}` : '关';
    case 'lock': return `${s.locked ? '已上锁' : '没锁'}${s.open ? ' · 门开着' : ''}`;
    case 'curtain': return s.position === 0 ? '拉上' : s.position === 100 ? '全开' : `开 ${Math.round(s.position)}%`;
    case 'vacuum': return `${PHASE_NAME[s.phase] ?? s.phase} · 电量 ${Math.round(s.battery)}%`;
    case 'sensor': return `${Number(s.temp).toFixed(1)}℃ · ${Math.round(s.humidity)}%`;
  }
  return '';
}

export function isOn(d: Device) {
  if (d.kind === 'lock') return !d.state.locked;
  if (d.kind === 'curtain') return d.state.position > 0;
  if (d.kind === 'vacuum') return d.state.phase === 'cleaning';
  return d.state.on === true;
}

export function DeviceIcon({ d, size = 'md' }: { d: Device; size?: 'sm' | 'md' }) {
  const Icon = KIND_ICON[d.kind];
  const on = isOn(d) && d.kind !== 'sensor';
  return (
    <span className={cn('grid shrink-0 place-items-center rounded-xl', size === 'sm' ? 'size-8' : 'size-10', on ? 'bg-[#fff1df] text-primary' : 'bg-secondary text-muted-foreground')}>
      <Icon className={size === 'sm' ? 'size-4' : 'size-5'} />
    </span>
  );
}

export const money = (v?: number, cur?: string) => (v ? `${{ USD: '$', GBP: '£', CNY: '¥', EUR: '€' }[cur ?? ''] ?? (cur ? `${cur} ` : '')}${v.toLocaleString()}` : '');
export const sizeText = (p: { w: number; d: number; h: number }) => `${Math.round(p.w)} × ${Math.round(p.d)} × ${Math.round(p.h)} cm`;
