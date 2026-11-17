// 导入 VR 看房: a 贝壳/如视 share link goes up (with the listing's floor plan, if there is one),
// the server downloads the scan and reads walls, doors, windows, rooms and the platforms off
// the mesh, and the result comes back drawn over the scan seen from above, each room's area
// beside the listing's. Confirming it makes it the home, with the scan to switch on as 实景.
import { useEffect, useMemo, useRef, useState } from 'react';
import { CircleAlert, FileImage, Info, Link2, LoaderCircle, X } from 'lucide-react';
import { toast } from 'sonner';
import { api, ApiError, get, TAB, type Home, type P } from '@/api';
import { cn } from '@/lib/utils';

interface RoomLevel { room: string; floor: number; ceiling: number; area: number; gross: number; printed?: number; added?: boolean }
interface Level { id: string; room: string; kind: string; y: number; poly: P[]; area: number; floor?: string }
interface Result { home: Home; rooms: RoomLevel[]; levels?: Level[]; notes?: string[]; frame: { ceiling: number } }
interface View { x0: number; z0: number; cell: number; w: number; h: number }
export interface ScanJob { id: string; status: 'running' | 'done' | 'failed'; steps: { t: number; text: string }[]; error?: string; result?: Result; view?: View; plan: boolean; created: number }

export const SCAN_KEEP = 'hometwin-scan';

const KIND_FILL: Record<string, string> = { living: '#f2a541', bedroom: '#5b8def', kitchen: '#3fb68b', bath: '#20a4c9', balcony: '#9b7fe0', entry: '#c9a36b' };

function Overlay({ job, focus }: { job: ScanJob; focus: string | null }) {
  const r = job.result!, v = job.view!;
  const h = r.home;
  const W = v.w * v.cell, H = v.h * v.cell;
  const walls = new Map(h.walls.map((w) => [w.id, w]));
  const at = (wallId: string, t: number): P => {
    const w = walls.get(wallId)!;
    const l = Math.hypot(w.b[0] - w.a[0], w.b[1] - w.a[1]);
    return [w.a[0] + ((w.b[0] - w.a[0]) * t) / l, w.a[1] + ((w.b[1] - w.a[1]) * t) / l];
  };
  const font = 0.26;
  return (
    <svg viewBox={`${v.x0 - 0.3} ${v.z0 - 0.3} ${W + 0.6} ${H + 0.6}`} className="block h-full w-full" preserveAspectRatio="xMidYMid meet" role="img" aria-label="从扫描读出的户型叠在实景俯视图上">
      <image href={`/api/scan/${job.id}/top.png`} x={v.x0} y={v.z0} width={W} height={H} preserveAspectRatio="none" />
      {h.rooms.map((rm) => {
        const hot = focus === rm.id;
        const c = rm.poly.reduce((s, p) => [s[0] + p[0] / rm.poly.length, s[1] + p[1] / rm.poly.length], [0, 0]);
        const added = r.rooms.find((x) => x.room === rm.id)?.added;
        return (
          <g key={rm.id}>
            <polygon points={rm.poly.map((p) => p.join(',')).join(' ')} fill={KIND_FILL[rm.kind] ?? '#999'} fillOpacity={hot ? 0.45 : added ? 0.3 : 0.14} stroke={KIND_FILL[rm.kind] ?? '#999'} strokeWidth={hot ? 0.07 : 0.03} strokeDasharray={added ? '0.15 0.1' : undefined} />
            <text x={c[0]} y={c[1]} textAnchor="middle" dominantBaseline="middle" fontSize={font} fontWeight={800} fill="#1d1a16" stroke="white" strokeWidth={font * 0.25} paintOrder="stroke">{rm.name}</text>
          </g>
        );
      })}
      {(h.levels ?? []).map((l) => (
        <polygon key={l.id} points={l.poly.map((p) => p.join(',')).join(' ')} fill={l.kind === 'step' ? '#f5d742' : '#e8890c'} fillOpacity={0.45} stroke={l.kind === 'step' ? '#c9a800' : '#b85f00'} strokeWidth={0.03} />
      ))}
      {h.walls.map((w) => <line key={w.id} x1={w.a[0]} y1={w.a[1]} x2={w.b[0]} y2={w.b[1]} stroke={w.exterior ? '#c0392b' : '#e0644f'} strokeOpacity={0.8} strokeWidth={Math.max(0.05, w.thick)} strokeLinecap="square" />)}
      {h.windows.map((wn) => { const a = at(wn.wall, wn.at - wn.width / 2), b = at(wn.wall, wn.at + wn.width / 2); return <line key={wn.id} x1={a[0]} y1={a[1]} x2={b[0]} y2={b[1]} stroke="#1f78c8" strokeWidth={0.12} />; })}
      {h.doors.map((d) => { const a = at(d.wall, d.at - d.width / 2), b = at(d.wall, d.at + d.width / 2); return <line key={d.id} x1={a[0]} y1={a[1]} x2={b[0]} y2={b[1]} stroke={d.entry ? '#e8590c' : '#2f9e44'} strokeWidth={0.14} />; })}
    </svg>
  );
}

function Legend() {
  const item = (color: string, label: string) => <span className="flex items-center gap-1"><i className="h-1.5 w-4 rounded-full" style={{ background: color }} />{label}</span>;
  return (
    <div className="flex flex-wrap gap-x-3 gap-y-1 text-[11.5px] font-semibold text-muted-foreground">
      {item('#c0392b', '外墙')}{item('#e0644f', '内墙')}{item('#2f9e44', '门')}{item('#e8590c', '入户门')}{item('#1f78c8', '窗')}{item('#e8890c', '地台')}{item('#f5d742', '台阶')}
    </div>
  );
}

const sign = (v: number) => `${v >= 0 ? '+' : ''}${v.toFixed(2)}`;

export function ScanImport({ onSwitch, onReset }: { onSwitch: (h: { id: string; name: string }) => void; onReset: () => void }) {
  const [job, setJobRaw] = useState<ScanJob | null>(null);
  const setJob = (j: ScanJob | null) => {
    setJobRaw(j && { ...j, steps: j.steps ?? [] });
    try { if (j) sessionStorage.setItem(SCAN_KEEP, j.id); else sessionStorage.removeItem(SCAN_KEEP); } catch { /* private mode */ }
  };
  const [link, setLink] = useState('');
  const [plan, setPlan] = useState<File | null>(null);
  const [busy, setBusy] = useState(false);
  const [focus, setFocus] = useState<string | null>(null);
  const [now, setNow] = useState(Date.now());
  const fileRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    let id: string | null = null;
    try { id = sessionStorage.getItem(SCAN_KEEP); } catch { /* private mode */ }
    if (id) get<ScanJob>(`/api/scan/${id}`).then(setJob).catch(() => setJob(null));
  }, []);
  useEffect(() => {
    if (!job || job.status !== 'running') return;
    const t = setInterval(async () => {
      setNow(Date.now());
      try { setJob(await get<ScanJob>(`/api/scan/${job.id}`)); } catch { /* next tick */ }
    }, 1200);
    return () => clearInterval(t);
  }, [job?.id, job?.status]); // eslint-disable-line react-hooks/exhaustive-deps

  const start = async () => {
    setBusy(true);
    const fd = new FormData();
    fd.append('link', link);
    if (plan) fd.append('image', plan);
    try {
      const res = await fetch('/api/scan', { method: 'POST', body: fd, headers: { 'X-Tab': TAB } });
      const body = await res.json();
      if (!res.ok) throw new Error(body?.error ?? `出错了（${res.status}）`);
      setJob(body);
    } catch (e) { toast.error((e as Error).message); }
    setBusy(false);
  };
  const confirm = async () => {
    if (!job) return;
    try {
      const r = await api<{ home: { id: string; name: string } }>('POST', `/api/scan/${job.id}/confirm`);
      try { sessionStorage.removeItem(SCAN_KEEP); } catch { /* private mode */ }
      onSwitch(r.home);
    } catch (e) { toast.error(e instanceof ApiError ? e.message : (e as Error).message); }
  };

  const r = job?.result;
  const rows = useMemo(() => r ? r.home.rooms.map((rm) => ({ rm, rl: r.rooms.find((x) => x.room === rm.id)! })) : [], [r]);
  const elapsed = job ? Math.max(0, Math.round((now - job.created) / 1000)) : 0;

  if (!job) {
    return (
      <div className="grid flex-1 place-items-center overflow-y-auto p-6 max-md:p-4">
        <div className="w-full max-w-md">
          <label className="block text-[13px] font-extrabold" htmlFor="scan-link">VR 看房链接</label>
          <textarea id="scan-link" value={link} onChange={(e) => setLink(e.target.value)} rows={3} placeholder="在贝壳 / 如视里点「分享」→「复制链接」，整段贴进来" className="mt-1.5 w-full resize-none rounded-2xl border border-[#d9d4cc] bg-white px-3.5 py-3 text-[13px] outline-none focus:border-primary" />
          <button type="button" onClick={() => fileRef.current?.click()} className="mt-2 flex w-full items-center gap-2 rounded-2xl border border-dashed border-[#d9d4cc] bg-white px-3.5 py-2.5 text-left text-[12.5px] hover:border-primary">
            <FileImage className="size-4 shrink-0 text-primary" />
            <span className="min-w-0 flex-1 truncate">{plan ? plan.name : '再加一张户型图（可选）：补上没扫到的阳台和关着的门，对照面积'}</span>
            {plan && <X className="size-4 shrink-0" onClick={(e) => { e.stopPropagation(); setPlan(null); }} />}
          </button>
          <input ref={fileRef} type="file" accept="image/jpeg,image/png" className="hidden" onChange={(e) => { setPlan(e.target.files?.[0] ?? null); e.target.value = ''; }} />
          <button type="button" disabled={!link.trim() || busy} onClick={start} className="mt-3 flex h-11 w-full items-center justify-center gap-1.5 rounded-xl bg-foreground text-[13.5px] font-bold text-white disabled:opacity-40">
            {busy ? <LoaderCircle className="size-4 animate-spin" /> : <Link2 className="size-4" />}导入
          </button>
          <p className="mt-3 text-[12px] leading-relaxed text-muted-foreground">服务器下载这套房子的三维扫描，切开墙找出房间、门窗、地台和台阶；做完把结果叠在俯视实景上给你看，确认了才换。链接只用来下载，不存也不记日志。</p>
        </div>
      </div>
    );
  }

  return (
    <div className="flex min-h-0 flex-1 max-md:flex-col max-md:overflow-y-auto">
      <div className="relative min-h-0 flex-1 bg-white max-md:h-[56dvh] max-md:flex-none">
        {r && job.view ? <Overlay job={job} focus={focus} /> : <div className="grid h-full place-items-center text-[13px] text-muted-foreground">{job.status === 'failed' ? '没做成' : '正在下载和切模型…'}</div>}
        {job.status === 'running' && (
          <div className="absolute inset-x-0 bottom-0 flex items-center gap-2 bg-white/85 px-4 py-2 text-[12.5px] font-bold backdrop-blur">
            <LoaderCircle className="size-4 animate-spin text-primary" />处理中 · {elapsed} 秒
          </div>
        )}
      </div>
      <aside className="flex w-[400px] flex-col border-l border-black/5 bg-[#fbfaf7] max-md:w-full max-md:border-l-0 max-md:border-t">
        <div className="no-scrollbar min-h-0 flex-1 overflow-y-auto p-4 max-md:overflow-visible">
          {r && (
            <>
              <div className="flex items-baseline justify-between">
                <h3 className="text-[14px] font-extrabold">{r.home.name}</h3>
                <small className="text-[11.5px] text-muted-foreground">层高 {r.frame.ceiling.toFixed(2)} m</small>
              </div>
              <Legend />
              <table className="mt-3 w-full text-[12.5px]">
                <thead>
                  <tr className="text-left text-[11px] text-muted-foreground">
                    <th className="py-1 font-semibold">房间</th><th className="py-1 text-right font-semibold">地面</th><th className="py-1 text-right font-semibold">净面积</th><th className="py-1 text-right font-semibold">到墙中</th>{job.plan && <th className="py-1 text-right font-semibold">图上</th>}
                  </tr>
                </thead>
                <tbody>
                  {rows.map(({ rm, rl }) => {
                    const off = rl.printed ? (rl.gross / rl.printed - 1) : 0;
                    return (
                      <tr key={rm.id} className={cn('cursor-default border-t border-black/5', focus === rm.id && 'bg-[#fff1df]')} onMouseEnter={() => setFocus(rm.id)} onMouseLeave={() => setFocus(null)} onClick={() => setFocus(focus === rm.id ? null : rm.id)}>
                        <td className="py-1.5 font-bold"><i className="mr-1.5 inline-block size-2.5 rounded-sm align-middle" style={{ background: KIND_FILL[rm.kind] }} />{rm.name}{rl.added && <small className="ml-1 font-semibold text-muted-foreground">按图补</small>}</td>
                        <td className="py-1.5 text-right tabular-nums text-muted-foreground">{rm.y ? sign(rm.y) : '±0'}</td>
                        <td className="py-1.5 text-right tabular-nums">{rl.area.toFixed(1)}</td>
                        <td className="py-1.5 text-right tabular-nums">{rl.gross.toFixed(1)}</td>
                        {job.plan && <td className={cn('py-1.5 text-right tabular-nums', Math.abs(off) > 0.12 && 'font-bold text-destructive')}>{rl.printed ? rl.printed.toFixed(1) : '—'}</td>}
                      </tr>
                    );
                  })}
                </tbody>
              </table>
              <p className="mt-1 text-[11.5px] text-muted-foreground">m² · {r.home.walls.length} 面墙 · {r.home.doors.length} 扇门 · {r.home.windows.length} 扇窗</p>
              {(r.levels ?? []).length > 0 && (
                <div className="mt-3 rounded-xl bg-white p-2.5 text-[12px]">
                  <b className="font-extrabold">地台和台阶</b>
                  {r.levels!.map((l) => <p key={l.id} className="mt-0.5 text-muted-foreground">{l.kind === 'step' ? '台阶' : '地台'} · 高 {(l.y * 100).toFixed(0)} cm · {l.area.toFixed(1)} m²{l.floor === 'wood' ? ' · 木地板' : l.floor === 'tile' ? ' · 瓷砖' : ''}</p>)}
                </div>
              )}
              {(r.notes ?? []).length > 0 && (
                <div className="mt-3 grid gap-1.5">
                  {r.notes!.map((n, i) => <p key={i} className="flex gap-1.5 rounded-xl bg-white px-2.5 py-2 text-[12px] text-muted-foreground"><Info className="mt-px size-3.5 shrink-0" />{n}</p>)}
                </div>
              )}
            </>
          )}
          {job.status === 'failed' && <p className="flex gap-1.5 rounded-xl bg-danger-soft px-2.5 py-2 text-[12.5px] font-semibold"><CircleAlert className="mt-px size-4 shrink-0 text-destructive" />{job.error}</p>}
          <details className="mt-3" open={job.status !== 'done'}>
            <summary className="cursor-pointer text-[12px] font-bold text-muted-foreground">处理过程（{job.steps.length} 步）</summary>
            <ol className="mt-1.5 grid gap-1 text-[12px]">
              {job.steps.map((st, n) => <li key={n} className="flex gap-2"><span className="w-9 shrink-0 text-right tabular-nums text-muted-foreground">{Math.round((st.t - job.created) / 1000)}s</span><span className="min-w-0 break-words">{st.text}</span></li>)}
            </ol>
          </details>
        </div>
        <div className="flex gap-2 border-t border-black/5 bg-white p-3 pb-[max(12px,env(safe-area-inset-bottom))]">
          <button type="button" onClick={() => { setJob(null); onReset(); }} className="h-11 rounded-xl bg-secondary px-3 text-[13px] font-bold">重新贴链接</button>
          <button type="button" disabled={!r} onClick={confirm} className="ml-auto h-11 flex-1 rounded-xl bg-foreground px-4 text-[13px] font-bold text-white disabled:opacity-40">用这个户型</button>
        </div>
      </aside>
    </div>
  );
}

