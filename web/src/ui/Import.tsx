// 导入户型图: a picture of a floor plan goes up, the server reads it (a vision model for the
// numbers and names, the image itself for the positions, an agent to check the result), and
// the draft comes back drawn over the picture with each room's area against the printed one.
// Confirming it makes it the home; the server restarts on it and the page reloads.
import { useEffect, useMemo, useRef, useState } from 'react';
import { CircleAlert, FileImage, House, Info, LoaderCircle, RefreshCw, Upload, X } from 'lucide-react';
import { toast } from 'sonner';
import { api, ApiError, get, TAB, type Home, type P } from '@/api';
import { cn } from '@/lib/utils';
import { useStore } from '@/store';
import { SCAN_KEEP, ScanImport } from './ScanImport';

interface Frame { crop: [number, number, number, number]; x: [number, number][]; z: [number, number][]; pxPerM: [number, number]; size: [number, number] }
interface RoomArea { name: string; kind: string; printed: number; computed: number; diff: number }
interface Issue { kind: string; level: 'warn' | 'info'; text: string; room?: string; at?: P }
interface Draft { home: Home; areas: RoomArea[]; issues: Issue[]; convention: string; frame: Frame }
interface Job { id: string; status: 'running' | 'done' | 'failed'; steps: { t: number; text: string }[]; error?: string; draft?: Draft; created: number }
interface Status { configured: boolean; notice?: string; home: { id: string; name: string }; sample: boolean }

const KEEP = 'hometwin-import';

const KIND_FILL: Record<string, string> = { living: '#f2a541', bedroom: '#5b8def', kitchen: '#3fb68b', bath: '#20a4c9', balcony: '#9b7fe0', entry: '#c9a36b' };

/** Metres to picture pixels along one axis, through the marks the server measured. */
function along(marks: [number, number][], pxPerM: number, m: number) {
  if (!marks.length) return m * pxPerM;
  if (m <= marks[0][0]) return marks[0][1] + (m - marks[0][0]) * pxPerM;
  for (let i = 1; i < marks.length; i++) {
    if (m <= marks[i][0]) {
      const [m0, p0] = marks[i - 1], [m1, p1] = marks[i];
      return p0 + ((m - m0) / (m1 - m0)) * (p1 - p0);
    }
  }
  const [ml, pl] = marks[marks.length - 1];
  return pl + (m - ml) * pxPerM;
}

function Overlay({ job, focus }: { job: Job; focus: string | null }) {
  const d = job.draft!;
  const f = d.frame;
  const px = (p: P): [number, number] => [f.crop[0] + along(f.x, f.pxPerM[0], p[0]), f.crop[1] + along(f.z, f.pxPerM[1], p[1])];
  const [x0, y0, x1, y1] = f.crop;
  const w = x1 - x0, h = y1 - y0;
  const font = Math.max(10, Math.min(w, h) * 0.026);
  const walls = new Map(d.home.walls.map((wl) => [wl.id, wl]));
  const at = (wallId: string, t: number) => {
    const wl = walls.get(wallId)!;
    const len = Math.hypot(wl.b[0] - wl.a[0], wl.b[1] - wl.a[1]);
    return [wl.a[0] + ((wl.b[0] - wl.a[0]) * t) / len, wl.a[1] + ((wl.b[1] - wl.a[1]) * t) / len] as P;
  };
  return (
    <svg viewBox={`${x0} ${y0} ${w} ${h}`} className="block h-full w-full" preserveAspectRatio="xMidYMid meet" role="img" aria-label="识别出的户型叠在原图上">
      <image href={`api/floorplan/${job.id}/image`} x={0} y={0} width={f.size[0]} height={f.size[1]} />
      <rect x={x0} y={y0} width={w} height={h} fill="white" opacity={0.35} />
      {d.home.rooms.map((r) => {
        const pts = r.poly.map(px);
        const cx = pts.reduce((s, p) => s + p[0], 0) / pts.length, cy = pts.reduce((s, p) => s + p[1], 0) / pts.length;
        const hot = focus === r.name;
        return (
          <g key={r.id}>
            <polygon points={pts.map((p) => p.join(',')).join(' ')} fill={KIND_FILL[r.kind] ?? '#999'} fillOpacity={hot ? 0.5 : 0.26} stroke={KIND_FILL[r.kind] ?? '#999'} strokeWidth={hot ? font * 0.3 : font * 0.1} />
            <text x={cx} y={cy} textAnchor="middle" dominantBaseline="middle" fontSize={font} fontWeight={800} fill="#1d1a16" stroke="white" strokeWidth={font * 0.22} paintOrder="stroke">{r.name}</text>
          </g>
        );
      })}
      {d.home.walls.map((wl) => {
        const a = px(wl.a), b = px(wl.b);
        return <line key={wl.id} x1={a[0]} y1={a[1]} x2={b[0]} y2={b[1]} stroke={wl.exterior ? '#c0392b' : '#e0644f'} strokeOpacity={0.75} strokeWidth={Math.max(2, wl.thick * f.pxPerM[0])} strokeLinecap="square" />;
      })}
      {d.home.windows.map((wn) => {
        const a = px(at(wn.wall, wn.at - wn.width / 2)), b = px(at(wn.wall, wn.at + wn.width / 2));
        return <line key={wn.id} x1={a[0]} y1={a[1]} x2={b[0]} y2={b[1]} stroke="#1f78c8" strokeWidth={font * 0.45} strokeLinecap="butt" />;
      })}
      {d.home.doors.map((dr) => {
        const a = px(at(dr.wall, dr.at - dr.width / 2)), b = px(at(dr.wall, dr.at + dr.width / 2));
        return <line key={dr.id} x1={a[0]} y1={a[1]} x2={b[0]} y2={b[1]} stroke={dr.entry ? '#e8590c' : '#2f9e44'} strokeWidth={font * 0.5} strokeLinecap="butt" />;
      })}
      {d.issues.filter((i) => i.at && i.level === 'warn').map((i, n) => {
        const p = px(i.at!);
        return <circle key={n} cx={p[0]} cy={p[1]} r={font * 0.6} fill="#e03131" stroke="white" strokeWidth={font * 0.15} />;
      })}
    </svg>
  );
}

function Legend() {
  const item = (color: string, label: string) => (
    <span className="flex items-center gap-1"><i className="h-1.5 w-4 rounded-full" style={{ background: color }} />{label}</span>
  );
  return (
    <div className="flex flex-wrap gap-x-3 gap-y-1 text-[11.5px] font-semibold text-muted-foreground">
      {item('#c0392b', '外墙')}{item('#e0644f', '内墙')}{item('#2f9e44', '门')}{item('#e8590c', '入户门')}{item('#1f78c8', '窗')}
    </div>
  );
}

export function ImportPanel() {
  const s = useStore.getState();
  const [status, setStatus] = useState<Status | null>(null);
  const [job, setJobRaw] = useState<Job | null>(null);
  const setJob = (j: Job | null) => {
    setJobRaw(j && { ...j, steps: j.steps ?? [] });
    // the import survives closing the panel or reloading the page
    try { if (j) sessionStorage.setItem(KEEP, j.id); else sessionStorage.removeItem(KEEP); } catch { /* private mode */ }
  };
  const [preview, setPreview] = useState<string | null>(null);
  const [focus, setFocus] = useState<string | null>(null);
  const [switching, setSwitching] = useState<string | null>(null);
  const [now, setNow] = useState(Date.now());
  const fileRef = useRef<HTMLInputElement>(null);
  // 户型图 or VR 看房链接; a VR import under way reopens on its tab
  const [mode, setMode] = useState<'plan' | 'scan'>(() => { try { return sessionStorage.getItem(SCAN_KEEP) ? 'scan' : 'plan'; } catch { return 'plan'; } });

  // VR import is off unless the server was started with -vr-import
  const [vr, setVr] = useState(false);
  useEffect(() => {
    get<{ vrImport: boolean }>('api/features').then((f) => setVr(f.vrImport)).catch(() => setVr(false));
  }, []);

  useEffect(() => {
    get<Status>('api/floorplan').then(setStatus).catch((e) => toast.error((e as Error).message));
    let id: string | null = null;
    try { id = sessionStorage.getItem(KEEP); } catch { /* private mode */ }
    if (id) get<Job>(`api/floorplan/${id}`).then(setJob).catch(() => setJob(null));
  }, []);
  // follow a running import
  useEffect(() => {
    if (!job || job.status !== 'running') return;
    const t = setInterval(async () => {
      setNow(Date.now());
      try { setJob(await get<Job>(`api/floorplan/${job.id}`)); } catch { /* the next tick tries again */ }
    }, 1500);
    return () => clearInterval(t);
  }, [job?.id, job?.status]);
  useEffect(() => () => { if (preview) URL.revokeObjectURL(preview); }, [preview]);

  const upload = async (file: File) => {
    setPreview(URL.createObjectURL(file));
    setJob(null);
    const fd = new FormData();
    fd.append('image', file);
    try {
      const res = await fetch('api/floorplan', { method: 'POST', body: fd, headers: { 'X-Tab': TAB } });
      const body = await res.json();
      if (!res.ok) throw new Error(body?.error ?? `出错了（${res.status}）`);
      setJob(body);
    } catch (e) { toast.error((e as Error).message); }
  };
  const retry = async () => {
    if (!job) return;
    try { setJob(await api<Job>('POST', `api/floorplan/${job.id}/retry`)); } catch (e) { toast.error((e as Error).message); }
  };
  // the server restarts on the new home; the page reloads once it answers with it
  const waitFor = async (id: string) => {
    for (let i = 0; i < 120; i++) {
      await new Promise((r) => setTimeout(r, 700));
      try {
        const h = await get<Home>('api/home');
        if (h.id === id) {
          try { sessionStorage.removeItem(KEEP); } catch { /* private mode */ }
          location.reload();
          return;
        }
      } catch { /* restarting */ }
    }
    setSwitching(null);
    toast.error('服务没有按时回来，请刷新页面');
  };
  const confirm = async () => {
    if (!job) return;
    try {
      const r = await api<{ home: { id: string; name: string } }>('POST', `api/floorplan/${job.id}/confirm`);
      setSwitching(r.home.name);
      waitFor(r.home.id);
    } catch (e) { toast.error(e instanceof ApiError ? e.message : (e as Error).message); }
  };
  const sample = async () => {
    try {
      const r = await api<{ home: { id: string; name: string } }>('POST', 'api/floorplan/sample');
      setSwitching(r.home.name);
      waitFor(r.home.id);
    } catch (e) { toast.error((e as Error).message); }
  };

  const d = job?.draft;
  const warns = d?.issues.filter((i) => i.level === 'warn') ?? [];
  const infos = d?.issues.filter((i) => i.level === 'info') ?? [];
  const elapsed = job ? Math.max(0, Math.round((now - job.created) / 1000)) : 0;
  const totals = useMemo(() => d ? { printed: d.areas.reduce((s, a) => s + a.printed, 0), computed: d.areas.reduce((s, a) => s + a.computed, 0) } : null, [d]);

  return (
    <div className="fixed inset-0 z-50 flex flex-col bg-[#f7f5f1] pt-[env(safe-area-inset-top)] animate-in fade-in duration-150" role="dialog" aria-label="导入户型图">
      <header className="flex items-center gap-2.5 border-b border-black/5 bg-white px-4 py-3 max-md:px-3">
        <span className="grid size-9 place-items-center rounded-xl bg-[#fff1df] text-primary"><FileImage className="size-5" /></span>
        <div className="min-w-0 flex-1">
          <h2 className="truncate text-[15px] font-extrabold">导入户型</h2>
          <p className="truncate text-[12px] text-muted-foreground">现在的户型：{status?.home.name ?? '…'}</p>
        </div>
        <button type="button" onClick={() => s.setPanel(null)} className="grid size-9 place-items-center rounded-full bg-secondary" aria-label="关闭"><X className="size-4" /></button>
      </header>

      {!switching && !job && vr && (
        <div className="flex justify-center border-b border-black/5 bg-white px-4 pb-3">
          <div className="flex rounded-xl bg-secondary p-1" role="tablist">
            {([['plan', '户型图'], ['scan', 'VR 看房链接']] as const).map(([m, label]) => (
              <button key={m} type="button" role="tab" aria-selected={mode === m} onClick={() => setMode(m)} className={cn('h-8 rounded-lg px-3.5 text-[12.5px] font-bold', mode === m ? 'bg-white shadow-sm' : 'text-muted-foreground')}>{m === 'scan' ? '贴 VR 看房链接' : '传户型图'}<span className="sr-only">{label}</span></button>
            ))}
          </div>
        </div>
      )}
      {switching ? (
        <div className="grid flex-1 place-items-center p-6 text-center">
          <div>
            <LoaderCircle className="mx-auto size-8 animate-spin text-primary" />
            <p className="mt-3 text-[15px] font-extrabold">正在换成「{switching}」</p>
            <p className="mt-1 text-[12.5px] text-muted-foreground">服务会重启一下，好了页面自己刷新</p>
          </div>
        </div>
      ) : vr && mode === 'scan' && !job ? (
        <ScanImport onSwitch={(h) => { setSwitching(h.name); waitFor(h.id); }} onReset={() => {}} />
      ) : !job ? (
        <div className="grid flex-1 place-items-center overflow-y-auto p-6 max-md:p-4">
          <div className="w-full max-w-md text-center">
            <button type="button" disabled={status?.configured === false} onClick={() => fileRef.current?.click()}
              onDragOver={(e) => e.preventDefault()} onDrop={(e) => { e.preventDefault(); const f = e.dataTransfer.files[0]; if (f) upload(f); }}
              className="flex w-full flex-col items-center gap-2 rounded-3xl border-2 border-dashed border-[#d9d4cc] bg-white px-6 py-10 transition-colors hover:border-primary disabled:opacity-50">
              <span className="grid size-12 place-items-center rounded-2xl bg-[#fff1df] text-primary"><Upload className="size-6" /></span>
              <b className="text-[15px] font-extrabold">选一张户型图</b>
              <small className="text-[12.5px] text-muted-foreground">链家、贝壳那种带尺寸标注的 JPG / PNG，手机截图也行</small>
            </button>
            <input ref={fileRef} type="file" accept="image/jpeg,image/png" className="hidden" onChange={(e) => { const f = e.target.files?.[0]; if (f) upload(f); e.target.value = ''; }} />
            {status?.configured === false && <p className="mt-3 text-[12.5px] font-semibold text-destructive">{status.notice}</p>}
            <p className="mt-4 text-[12px] leading-relaxed text-muted-foreground">模型读图上的数字和房间名，位置按尺寸标注从图上量；识别完会把结果叠在原图上给你看，确认了才换。</p>
            {status && !status.sample && (
              <button type="button" onClick={sample} className="mt-4 inline-flex h-9 items-center gap-1.5 rounded-xl bg-white px-3 text-[12.5px] font-bold shadow-sm"><House className="size-4" />换回示例户型</button>
            )}
          </div>
        </div>
      ) : (
        <div className="flex min-h-0 flex-1 max-md:flex-col max-md:overflow-y-auto">
          <div className="relative min-h-0 flex-1 bg-white max-md:h-[56dvh] max-md:flex-none">
            {d ? <Overlay job={job} focus={focus} /> : <img src={preview ?? `api/floorplan/${job.id}/image`} alt="上传的户型图" className="h-full w-full object-contain opacity-60" />}
            {job.status === 'running' && (
              <div className="absolute inset-x-0 bottom-0 flex items-center gap-2 bg-white/85 px-4 py-2 text-[12.5px] font-bold backdrop-blur">
                <LoaderCircle className="size-4 animate-spin text-primary" />识别中 · {elapsed} 秒
              </div>
            )}
          </div>
          <aside className="flex w-[380px] flex-col border-l border-black/5 bg-[#fbfaf7] max-md:w-full max-md:border-l-0 max-md:border-t">
            <div className="no-scrollbar min-h-0 flex-1 overflow-y-auto p-4 max-md:overflow-visible">
              {d && totals && (
                <>
                  <div className="flex items-baseline justify-between">
                    <h3 className="text-[14px] font-extrabold">{d.home.name}</h3>
                    <small className="text-[11.5px] text-muted-foreground">{d.convention}</small>
                  </div>
                  <Legend />
                  <table className="mt-3 w-full text-[12.5px]">
                    <thead>
                      <tr className="text-left text-[11.5px] text-muted-foreground"><th className="py-1 font-semibold">房间</th><th className="py-1 text-right font-semibold">图上面积</th><th className="py-1 text-right font-semibold">算出面积</th></tr>
                    </thead>
                    <tbody>
                      {d.areas.map((a) => {
                        const off = a.printed > 0 && Math.abs(a.diff) > 0.1;
                        return (
                          <tr key={a.name} className={cn('cursor-default border-t border-black/5', focus === a.name && 'bg-[#fff1df]')} onMouseEnter={() => setFocus(a.name)} onMouseLeave={() => setFocus(null)} onClick={() => setFocus(focus === a.name ? null : a.name)}>
                            <td className="py-1.5 font-bold"><i className="mr-1.5 inline-block size-2.5 rounded-sm align-middle" style={{ background: KIND_FILL[a.kind] }} />{a.name}</td>
                            <td className="py-1.5 text-right tabular-nums">{a.printed ? `${a.printed.toFixed(1)} m²` : '—'}</td>
                            <td className={cn('py-1.5 text-right tabular-nums', off && 'font-bold text-destructive')}>{a.computed.toFixed(1)} m²{a.printed > 0 && <small className="ml-1 text-[10.5px] text-muted-foreground">{a.diff >= 0 ? '+' : ''}{(a.diff * 100).toFixed(0)}%</small>}</td>
                          </tr>
                        );
                      })}
                      <tr className="border-t border-black/10 font-extrabold"><td className="py-1.5">合计</td><td className="py-1.5 text-right tabular-nums">{totals.printed.toFixed(1)} m²</td><td className="py-1.5 text-right tabular-nums">{totals.computed.toFixed(1)} m²</td></tr>
                    </tbody>
                  </table>
                  <p className="mt-1 text-[11.5px] text-muted-foreground">{d.home.walls.length} 面墙 · {d.home.doors.length} 扇门 · {d.home.windows.length} 扇窗</p>
                  {(warns.length > 0 || infos.length > 0) ? (
                    <div className="mt-3 grid gap-1.5">
                      {warns.map((i, n) => <p key={`w${n}`} className="flex gap-1.5 rounded-xl bg-danger-soft px-2.5 py-2 text-[12px] font-semibold"><CircleAlert className="mt-px size-3.5 shrink-0 text-destructive" />{i.text}</p>)}
                      {infos.map((i, n) => <p key={`i${n}`} className="flex gap-1.5 rounded-xl bg-white px-2.5 py-2 text-[12px] text-muted-foreground"><Info className="mt-px size-3.5 shrink-0" />{i.text}</p>)}
                    </div>
                  ) : <p className="mt-3 rounded-xl bg-success-soft px-2.5 py-2 text-[12px] font-semibold">都对上了，没发现问题</p>}
                </>
              )}
              {job.status === 'failed' && <p className="flex gap-1.5 rounded-xl bg-danger-soft px-2.5 py-2 text-[12.5px] font-semibold"><CircleAlert className="mt-px size-4 shrink-0 text-destructive" />{job.error}</p>}
              <details className="mt-3" open={job.status === 'running'}>
                <summary className="cursor-pointer text-[12px] font-bold text-muted-foreground">识别过程（{job.steps.length} 步）</summary>
                <ol className="mt-1.5 grid gap-1 text-[12px]">
                  {job.steps.map((st, n) => <li key={n} className="flex gap-2"><span className="w-9 shrink-0 text-right tabular-nums text-muted-foreground">{Math.round((st.t - job.created) / 1000)}s</span><span className="min-w-0 break-words">{st.text}</span></li>)}
                </ol>
              </details>
            </div>
            <div className="flex gap-2 border-t border-black/5 bg-white p-3 pb-[max(12px,env(safe-area-inset-bottom))]">
              <button type="button" onClick={() => { setJob(null); setPreview(null); }} className="h-11 rounded-xl bg-secondary px-3 text-[13px] font-bold">换一张</button>
              <button type="button" disabled={job.status === 'running'} onClick={retry} className="flex h-11 items-center gap-1.5 rounded-xl bg-secondary px-3 text-[13px] font-bold disabled:opacity-40"><RefreshCw className="size-4" />重新识别</button>
              <button type="button" disabled={!d} onClick={confirm} className="ml-auto h-11 flex-1 rounded-xl bg-foreground px-4 text-[13px] font-bold text-white disabled:opacity-40">用这个户型</button>
            </div>
          </aside>
        </div>
      )}
    </div>
  );
}
