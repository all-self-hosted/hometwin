// The side panels: every device by room, the automations and what waits for you, and what
// this twin is for (its goals, how far it goes, and questions about the home).
import { useEffect, useState } from 'react';
import { BrainCircuit, Check, Cpu, FileImage, Play, Search, ShieldCheck, Workflow, X } from 'lucide-react';
import { toast } from 'sonner';
import { api, get, type Answer, type Level, type TwinInfo } from '@/api';
import { cn } from '@/lib/utils';
import { useStore } from '@/store';
import { DeviceIcon, Panel, Switch, isOn, summary } from './bits';

export function DevicesPanel() {
  const home = useStore((s) => s.home)!;
  const devices = useStore((s) => s.devices);
  const command = useStore((s) => s.command);
  const s = useStore.getState();
  const rooms = [...home.rooms.map((r) => ({ id: r.id, name: r.name })), { id: 'outdoors', name: '室外' }];
  const all = Object.values(devices);
  const on = all.filter((d) => isOn(d) && d.kind !== 'sensor').length;
  const scene = async (name: string, label: string) => {
    await api('POST', `api/sim/${name}`);
    toast(`模拟：${label}`);
  };
  return (
    <Panel title="设备" sub={`${all.length} 个设备，${on} 个在工作 · ${all.filter((d) => d.source === 'ha').length ? `${all.filter((d) => d.source === 'ha').length} 个来自 Home Assistant` : '模拟设备'}`} icon={<span className="grid size-9 place-items-center rounded-xl bg-[#fff1df] text-primary"><Cpu className="size-5" /></span>} onClose={() => s.setPanel(null)}>
      <div className="grid gap-3">
        <button type="button" onClick={() => s.setPanel('ha')} className="flex items-center gap-2 rounded-2xl bg-[#e6f1fb] px-3 py-2.5 text-left text-[12.5px] font-bold text-[#1f78c8]">
          接入 Home Assistant
          <span className="ml-auto text-[11.5px] font-semibold text-[#1f78c8]/80">用家里真实的设备 →</span>
        </button>
        {rooms.map((r) => {
          const ds = all.filter((d) => d.room === r.id);
          if (!ds.length) return null;
          return (
            <div key={r.id}>
              <h4 className="mb-1.5 px-1 text-[12px] font-bold text-muted-foreground">{r.name}</h4>
              <div className="grid gap-1.5">
                {ds.map((d) => (
                  <div key={d.id} className="flex items-center gap-2.5 rounded-2xl bg-white/70 p-2">
                    <button type="button" className="flex min-w-0 flex-1 items-center gap-2.5 text-left" onClick={() => { s.select({ kind: 'device', id: d.id }); s.camera('point', undefined, [d.x, d.z]); }}>
                      <DeviceIcon d={d} size="sm" />
                      <span className="min-w-0"><b className="block truncate text-[13px]">{d.name}</b><small className="block truncate text-[11.5px] text-muted-foreground">{d.source === 'ha' ? '实测 · ' : ''}{summary(d)}</small></span>
                    </button>
                    {(d.kind === 'light' || d.kind === 'ac') && <Switch on={isOn(d)} onChange={(v) => command(d.id, { on: v })} />}
                  </div>
                ))}
              </div>
            </div>
          );
        })}
        <div className="rounded-2xl bg-white/50 p-3">
          <h4 className="text-[12px] font-bold">试一下提醒</h4>
          <p className="mt-0.5 text-[11.5px] text-muted-foreground">让模拟的家发生点事，看孪生会不会发现</p>
          <div className="mt-2 flex flex-wrap gap-1.5">
            {[['shower', '有人洗澡'], ['door-open', '门开着没关'], ['door-close', '关上门'], ['hot', '屋里变热 3℃']].map(([n, l]) => (
              <button key={n} type="button" onClick={() => scene(n, l)} className="h-8 rounded-lg bg-white px-2.5 text-[12px] font-bold shadow-sm">{l}</button>
            ))}
          </div>
        </div>
      </div>
    </Panel>
  );
}

const LEVELS: [Level, string, string][] = [
  ['advise', '只建议', '提出来，你点了才做'],
  ['guarded', '有护栏', '规则允许就做，否则问你'],
  ['auto', '自主', '自己做，开锁仍要你批准'],
];

export function AutoPanel() {
  const autos = useStore((s) => s.automations);
  const proposals = useStore((s) => s.proposals);
  const pending = proposals.filter((p) => p.status === 'pending');
  const done = proposals.filter((p) => p.status !== 'pending').slice(0, 8);
  const s = useStore.getState();
  const decide = async (id: string, ok: boolean) => {
    try { await api('POST', `api/proposals/${id}/${ok ? 'approve' : 'reject'}`); } catch (e) { toast.error((e as Error).message); }
  };
  const setLevel = (id: string, level: Level, enabled: boolean) => api('PUT', `api/automations/${id}`, { level, enabled }).catch((e) => toast.error(e.message));
  return (
    <Panel wide title="自动化" sub="每条都有授权等级；开锁这类事一定要你批准" icon={<span className="grid size-9 place-items-center rounded-xl bg-[#fff1df] text-primary"><Workflow className="size-5" /></span>} onClose={() => s.setPanel(null)}>
      <div className="grid gap-3">
        {pending.length > 0 && (
          <div className="grid gap-1.5">
            <h4 className="px-1 text-[12px] font-bold text-primary">等你确认 {pending.length}</h4>
            {pending.map((p) => (
              <article key={p.id} className="rounded-2xl border border-primary/25 bg-white/85 p-3">
                <b className="text-[13.5px]">{p.name}</b>
                <p className="text-[11.5px] text-muted-foreground">{p.why}</p>
                <ul className="mt-1.5 list-disc pl-4 text-[12.5px]">{p.steps.map((x, i) => <li key={i}>{x}</li>)}</ul>
                {p.reason && <p className="mt-1.5 text-[11.5px] text-warning">{p.reason}</p>}
                <div className="mt-2 flex gap-2">
                  <button type="button" onClick={() => decide(p.id, true)} className="flex h-8 items-center gap-1 rounded-lg bg-primary px-3 text-[12px] font-bold text-white"><Check className="size-3.5" />批准执行</button>
                  <button type="button" onClick={() => decide(p.id, false)} className="flex h-8 items-center gap-1 rounded-lg bg-white px-3 text-[12px] font-bold shadow-sm"><X className="size-3.5" />不要</button>
                </div>
              </article>
            ))}
          </div>
        )}
        <div className="grid gap-1.5">
          {autos.map((a) => (
            <article key={a.id} className={cn('rounded-2xl bg-white/70 p-3', !a.enabled && 'opacity-60')}>
              <div className="flex items-center gap-2">
                <div className="min-w-0 flex-1">
                  <b className="block truncate text-[13.5px]">{a.name}</b>
                  <small className="block truncate text-[11.5px] text-muted-foreground">{a.when} · {a.actions.length} 个动作</small>
                </div>
                {a.trigger.kind === 'manual' && (
                  <button type="button" onClick={() => api('POST', `api/automations/${a.id}/run`).catch((e) => toast.error(e.message))} className="flex h-8 items-center gap-1 rounded-lg bg-foreground px-2.5 text-[12px] font-bold text-white"><Play className="size-3.5" />运行</button>
                )}
                <Switch on={a.enabled} onChange={(v) => setLevel(a.id, a.level, v)} label="启用" />
              </div>
              <div className="mt-2 flex rounded-xl bg-black/[0.05] p-1">
                {LEVELS.map(([l, name, tip]) => (
                  <button key={l} type="button" title={tip} onClick={() => setLevel(a.id, l, a.enabled)} className={cn('h-7 flex-1 rounded-lg text-[12px] font-bold', a.level === l ? 'bg-white shadow-sm' : 'text-muted-foreground')}>{name}</button>
                ))}
              </div>
            </article>
          ))}
        </div>
        {done.length > 0 && (
          <div>
            <h4 className="mb-1.5 px-1 text-[12px] font-bold text-muted-foreground">记录</h4>
            <ul className="grid gap-1">
              {done.map((p) => (
                <li key={p.id} className="rounded-xl bg-white/50 px-3 py-1.5 text-[12px]">
                  <b>{p.name}</b> <span className={cn('font-semibold', p.status === 'done' ? 'text-success' : p.status === 'failed' ? 'text-destructive' : 'text-muted-foreground')}>{{ done: '已执行', rejected: '没执行', failed: '失败', pending: '' }[p.status]}</span>
                  <span className="text-muted-foreground"> · {new Date(p.t).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit' })} · {p.by ?? ''}</span>
                  {p.status === 'failed' && p.reason && <span className="block text-destructive">{p.reason}</span>}
                </li>
              ))}
            </ul>
          </div>
        )}
      </div>
    </Panel>
  );
}

const VKIND: Record<string, string> = { structural: '结构', live: '读数', layout: '布置' };

/** What the ontology's constraints find, each with which rule and which facts. */
function Constraints() {
  const vs = useStore((s) => s.constraints);
  const s = useStore.getState();
  return (
    <section>
      <h4 className="mb-1.5 px-1 text-[12px] font-bold text-muted-foreground">约束检查 {vs.length ? `· ${vs.length} 条` : ''}</h4>
      {vs.length === 0 && <p className="rounded-2xl bg-success-soft px-3 py-2 text-[12.5px] font-semibold text-success">户型、设备和布置都符合约束</p>}
      <ul className="grid gap-1.5">
        {vs.slice(0, 12).map((v, i) => (
          <li key={i}>
            <button type="button" onClick={() => { if (v.node.startsWith('item:')) s.select({ kind: 'item', id: v.node.slice(5) }); else if (v.node.startsWith('device:')) s.select({ kind: 'device', id: v.node.slice(7) }); else if (v.node.startsWith('room:')) { s.select({ kind: 'room', id: v.node.slice(5) }); s.camera('room', v.node.slice(5)); } }}
              className="w-full rounded-2xl bg-white/70 p-2.5 text-left text-[12px]">
              <span className="mr-1.5 rounded-md bg-warning-soft px-1.5 py-0.5 text-[10.5px] font-bold text-warning">{VKIND[v.kind] ?? v.kind}</span>
              <b>{v.text}</b>
              <span className="mt-0.5 block text-[11px] text-muted-foreground">{v.why}</span>
            </button>
          </li>
        ))}
      </ul>
    </section>
  );
}

const STATUS: Record<string, [string, string]> = { done: ['已有', 'bg-success-soft text-success'], partial: ['部分', 'bg-warning-soft text-warning'], next: ['以后', 'bg-secondary text-muted-foreground'] };

export function TwinPanel() {
  const [info, setInfo] = useState<TwinInfo | null>(null);
  const [q, setQ] = useState('');
  const [answer, setAnswer] = useState<Answer | null>(null);
  const [examples, setExamples] = useState<string[]>([]);
  const s = useStore.getState();
  useEffect(() => {
    get<TwinInfo>('api/twin').then(setInfo);
    get<{ examples: string[] }>('api/graph').then((g) => setExamples(g.examples));
  }, []);
  const ask = async (text: string) => {
    setQ(text);
    const a = await get<Answer>(`api/graph/ask?q=${encodeURIComponent(text)}`);
    setAnswer(a);
    useStore.setState({ highlight: a.nodes });
    const room = a.nodes.find((n) => n.startsWith('room:'));
    if (room && !a.nodes.some((n) => n.startsWith('device:') || n.startsWith('item:'))) s.camera('room', room.slice(5));
  };
  return (
    <Panel wide title="这个孪生" sub="它为哪些问题而建，现在做到哪一级" icon={<span className="grid size-9 place-items-center rounded-xl bg-[#fff1df] text-primary"><BrainCircuit className="size-5" /></span>} onClose={() => { s.setPanel(null); useStore.setState({ highlight: [] }); }}>
      <div className="grid gap-4">
        <button type="button" onClick={() => s.setPanel('import')} className="flex items-center gap-2 rounded-2xl bg-[#fff1df] px-3 py-2.5 text-left text-[12.5px] font-bold text-primary">
          <FileImage className="size-4" />导入户型图
          <span className="ml-auto text-[11.5px] font-semibold text-primary/80">换成你家的户型 →</span>
        </button>
        <section>
          <h4 className="mb-1.5 px-1 text-[12px] font-bold text-muted-foreground">问问这个家</h4>
          <form onSubmit={(e) => { e.preventDefault(); if (q.trim()) ask(q); }} className="flex gap-2">
            <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="客厅有哪些设备？" className="h-10 min-w-0 flex-1 rounded-xl border border-input bg-white px-3 text-[13px]" />
            <button className="grid size-10 shrink-0 place-items-center rounded-xl bg-primary text-white" aria-label="问"><Search className="size-4" /></button>
          </form>
          <div className="mt-2 flex flex-wrap gap-1.5">
            {examples.slice(0, 6).map((e) => <button key={e} type="button" onClick={() => ask(e)} className="h-7 rounded-full bg-white/80 px-2.5 text-[11.5px] font-semibold shadow-sm">{e}</button>)}
          </div>
          {answer && (
            <div className="mt-2 rounded-2xl bg-white/85 p-3 text-[13px]">
              <p className="font-semibold">{answer.text}</p>
              {answer.rows && answer.rows.length > 0 && (
                <ul className="mt-1.5 grid gap-1 text-[12px]">
                  {answer.rows.map((r) => <li key={r.id} className="flex justify-between gap-2"><span>{r.name}</span><span className="text-muted-foreground">{r.detail}</span></li>)}
                </ul>
              )}
            </div>
          )}
        </section>
        <Constraints />
        {info && (
          <>
            <section>
              <h4 className="mb-1.5 px-1 text-[12px] font-bold text-muted-foreground">能力等级</h4>
              <ol className="grid gap-1.5">
                {info.levels.map((l, i) => (
                  <li key={l.id} className="rounded-2xl bg-white/70 p-3">
                    <div className="flex items-center gap-2">
                      <span className="grid size-6 place-items-center rounded-full bg-foreground text-[11px] font-extrabold text-white">{i + 1}</span>
                      <b className="text-[13.5px]">{l.name}</b>
                      <span className={cn('ml-auto rounded-md px-1.5 py-0.5 text-[11px] font-bold', STATUS[l.status][1])}>{STATUS[l.status][0]}</span>
                    </div>
                    <p className="mt-1 text-[12px] text-muted-foreground">{l.what}</p>
                    <ul className="mt-1 grid gap-0.5 text-[11.5px]">
                      {l.has.map((h) => <li key={h} className="flex gap-1.5"><ShieldCheck className="mt-px size-3.5 shrink-0 text-success" />{h}</li>)}
                      {l.next?.map((h) => <li key={h} className="flex gap-1.5 text-muted-foreground"><span className="mt-1.5 size-1.5 shrink-0 rounded-full bg-[#cfc9bf]" />{h}</li>)}
                    </ul>
                  </li>
                ))}
              </ol>
            </section>
            <section>
              <h4 className="mb-1.5 px-1 text-[12px] font-bold text-muted-foreground">为这些问题而建</h4>
              <ul className="grid gap-1.5">
                {info.goals.map((g) => (
                  <li key={g.id} className="rounded-2xl bg-white/60 p-3 text-[12px]">
                    <b className="text-[13px]">{g.question}</b>
                    <p className="mt-0.5 text-muted-foreground">以前：{g.before}</p>
                    <p>现在：{g.after}</p>
                    <p className="mt-1 text-[11px] text-muted-foreground">要建模：{g.data.join('；')}</p>
                  </li>
                ))}
              </ul>
              <p className="mt-2 px-1 text-[11px] text-muted-foreground">
                模型里有 {info.model.rooms} 个房间、{info.model.walls} 面墙、{info.model.doors} 扇门、{info.model.windows} 扇窗、{info.model.furniture} 件家具、{info.model.devices} 个设备、{info.model.points} 个数据点。
                {info.mqtt && <> MQTT：<code>{info.mqtt}</code>，订阅 <code>home/#</code>。</>}
              </p>
            </section>
          </>
        )}
      </div>
    </Panel>
  );
}
