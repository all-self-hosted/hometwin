// The frame around the 3D view: the top bar (mode, day/night, undo), the dock that opens the
// panels, the hint chips, and the confirm dialog.
import { Box, BrainCircuit, CircleAlert, Cpu, Eye, FileImage, Hand, House, MessageCircle, Moon, Package, Redo2, ScanEye, Sun, Undo2, Workflow } from 'lucide-react';
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog';
import { cn } from '@/lib/utils';
import { useConfirm, useStore, type Mode, type Panel } from '@/store';
import { useNarrow } from './bits';

const MODES: [Mode, string, typeof Eye][] = [['view', '看看', Eye], ['arrange', '布置', Hand], ['devices', '设备', Cpu]];

export function TopBar() {
  const home = useStore((s) => s.home);
  const mode = useStore((s) => s.mode);
  const night = useStore((s) => s.night);
  const scan = useStore((s) => s.scan);
  const past = useStore((s) => s.past.length);
  const future = useStore((s) => s.future.length);
  const connected = useStore((s) => s.connected);
  const s = useStore.getState();
  return (
    <header className="pointer-events-none absolute inset-x-0 top-0 z-30 flex items-start justify-between gap-2 p-4 pt-[max(16px,env(safe-area-inset-top))] max-md:p-3">
      <button type="button" onClick={() => { s.select(null); s.camera('home'); }} className="glass-dim pointer-events-auto flex h-11 items-center gap-2.5 rounded-2xl pl-2 pr-3.5 max-md:h-10">
        <span className="grid size-8 place-items-center rounded-xl bg-primary text-white max-md:size-7"><House className="size-4" /></span>
        <span className="text-left leading-tight">
          <b className={cn('block whitespace-nowrap text-[14px] font-extrabold', home?.scan && 'max-[480px]:hidden')}>HomeTwin</b>
          <small className="block text-[11px] text-muted-foreground max-md:hidden">{home?.name}</small>
        </span>
        <i className={cn('ml-1 size-2 rounded-full', connected ? 'bg-success' : 'bg-warning animate-pulse')} title={connected ? '已连上' : '连接中'} />
      </button>
      <nav className="glass-dim pointer-events-auto flex h-11 items-center gap-1 rounded-2xl p-1 max-md:h-10">
        {MODES.map(([m, label, Icon]) => (
          <button key={m} type="button" onClick={() => s.setMode(m)} className={cn('flex h-9 items-center gap-1.5 whitespace-nowrap rounded-xl px-3 text-[13px] font-bold transition-colors max-md:h-8 max-md:px-2.5 max-[400px]:px-2', mode === m ? 'bg-foreground text-white' : 'text-muted-foreground hover:text-foreground')}>
            <Icon className="size-4 max-[400px]:hidden" />{label}
          </button>
        ))}
      </nav>
      <div className="pointer-events-auto flex gap-2">
        {mode === 'arrange' && (
          <div className="glass-dim flex h-11 items-center gap-0.5 rounded-2xl p-1 max-md:hidden">
            <button type="button" disabled={!past} onClick={() => s.undo()} className="grid size-9 place-items-center rounded-xl disabled:opacity-35 hover:bg-white" aria-label="撤销"><Undo2 className="size-4" /></button>
            <button type="button" disabled={!future} onClick={() => s.redo()} className="grid size-9 place-items-center rounded-xl disabled:opacity-35 hover:bg-white" aria-label="重做"><Redo2 className="size-4" /></button>
          </div>
        )}
        {/* on a phone the bar has no room for it: it is in the 孪生 panel */}
        <button type="button" onClick={() => s.setPanel('import')} className="glass-dim flex h-11 items-center gap-1.5 rounded-2xl px-3 text-[13px] font-bold max-md:hidden">
          <FileImage className="size-4 text-primary" />导入户型
        </button>
        {home?.scan && (
          <button type="button" onClick={() => s.setScan(!scan)} aria-pressed={scan} className={cn('flex h-11 items-center gap-1.5 rounded-2xl px-3 text-[13px] font-bold max-md:h-10 max-md:px-2.5', scan ? 'bg-foreground text-white' : 'glass-dim')} aria-label={scan ? '换回模型' : '看实景'}>
            {scan ? <ScanEye className="size-4" /> : <Box className="size-4 text-primary" />}
            <span className="max-md:hidden">实景</span>
          </button>
        )}
        <button type="button" onClick={() => s.setNight(!night)} className="glass-dim flex h-11 items-center gap-1.5 rounded-2xl px-3 text-[13px] font-bold max-md:h-10 max-md:px-2.5" aria-label={night ? '切到白天' : '切到夜晚'}>
          {night ? <Moon className="size-4 text-[#6b7bd8]" /> : <Sun className="size-4 text-primary" />}
          <span className="max-md:hidden">{night ? '夜晚' : '白天'}</span>
        </button>
      </div>
    </header>
  );
}

const DOCK: [Exclude<Panel, null>, string, typeof Package][] = [['library', '家具库', Package], ['devices', '设备', Cpu], ['assistant', '助手', MessageCircle], ['auto', '自动化', Workflow], ['twin', '孪生', BrainCircuit]];

export function Dock() {
  const panel = useStore((s) => s.panel);
  const sel = useStore((s) => s.sel);
  const pending = useStore((s) => s.proposals.filter((p) => p.status === 'pending').length);
  const mode = useStore((s) => s.mode);
  const past = useStore((s) => s.past.length);
  const future = useStore((s) => s.future.length);
  const narrow = useNarrow();
  const s = useStore.getState();
  // on a phone the sheet takes the bottom; the dock comes back when it closes
  if (narrow && (panel || (sel && sel.kind !== 'room') || sel?.kind === 'room')) return null;
  return (
    <nav className="glass-dim absolute bottom-5 left-1/2 z-30 flex -translate-x-1/2 items-center gap-1 rounded-[20px] p-1.5 max-md:bottom-[max(12px,env(safe-area-inset-bottom))]">
      {DOCK.map(([p, label, Icon]) => (
        <button key={p} type="button" onClick={() => s.setPanel(panel === p ? null : p)} className={cn('relative flex h-12 min-w-[64px] flex-col items-center justify-center gap-0.5 rounded-2xl px-2 text-[11.5px] font-bold transition-colors max-[400px]:min-w-[54px] max-[400px]:px-1', panel === p ? 'bg-foreground text-white' : 'text-foreground hover:bg-white')}>
          <Icon className="size-[18px]" />{label}
          {p === 'auto' && pending > 0 && <b className="absolute right-1.5 top-1 grid size-[18px] place-items-center rounded-full bg-primary text-[10.5px] text-white">{pending}</b>}
        </button>
      ))}
      {narrow && mode === 'arrange' && (
        <>
          <i className="mx-0.5 h-8 w-px bg-black/10" />
          <button type="button" disabled={!past} onClick={() => s.undo()} className="grid size-11 place-items-center rounded-2xl disabled:opacity-35" aria-label="撤销"><Undo2 className="size-4" /></button>
          <button type="button" disabled={!future} onClick={() => s.redo()} className="grid size-11 place-items-center rounded-2xl disabled:opacity-35" aria-label="重做"><Redo2 className="size-4" /></button>
        </>
      )}
    </nav>
  );
}

export function Hints() {
  const raw = useStore((s) => s.hints);
  const constraints = useStore((s) => s.constraints);
  // live constraints from the ontology sit with the hints
  const hints = [...constraints.filter((v) => v.kind === 'live').map((v) => ({ id: `${v.constraint}:${v.node}`, kind: 'constraint', level: 'warn' as const, device: v.node.startsWith('device:') ? v.node.slice(7) : '', room: '', text: v.text, since: v.since ?? 0 })), ...raw];
  const panel = useStore((s) => s.panel);
  const sel = useStore((s) => s.sel);
  const narrow = useNarrow();
  if (!hints.length) return null;
  // keep the right column free when a card is there
  const right = !narrow && !(sel && sel.kind !== 'device');
  if (!right && !narrow) return null;
  if (narrow && (panel || sel)) return null;
  const s = useStore.getState();
  return (
    <div className={cn('absolute z-20 grid gap-1.5', narrow ? 'inset-x-3 top-[64px]' : 'right-4 top-[76px] w-[300px]')}>
      {hints.slice(0, narrow ? 1 : 3).map((h) => (
        <button key={h.id} type="button" onClick={() => { if (h.device) { s.select({ kind: 'device', id: h.device }); const d = s.devices[h.device]; if (d) s.camera('point', undefined, [d.x, d.z]); } }}
          className={cn('glass-dim flex items-start gap-2 rounded-2xl px-3 py-2 text-left text-[12.5px] font-semibold', h.level === 'warn' ? 'text-destructive' : 'text-warning')}>
          <CircleAlert className="mt-px size-4 shrink-0" />
          <span className="text-foreground">{h.text}</span>
        </button>
      ))}
    </div>
  );
}

export function Confirm() {
  const text = useConfirm((s) => s.text);
  const yes = useConfirm((s) => s.yes);
  const close = useConfirm((s) => s.close);
  return (
    <Dialog open={!!text} onOpenChange={(o) => !o && close()}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>需要你确认</DialogTitle>
          <DialogDescription>{text}</DialogDescription>
        </DialogHeader>
        <DialogFooter className="gap-2">
          <button type="button" onClick={close} className="h-10 rounded-xl bg-secondary px-4 text-[13px] font-bold">算了</button>
          <button type="button" onClick={() => { yes?.(); close(); }} className="h-10 rounded-xl bg-foreground px-4 text-[13px] font-bold text-white">确定开锁</button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
