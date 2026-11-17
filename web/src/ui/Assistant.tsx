// 家庭助手: ask about the home in words. It answers from the twin's own tools, changes what the
// house rules allow, and leaves what a person must decide (unlocking, putting a piece in a
// room) as a card or an approval for that person.
import { useEffect, useRef, useState } from 'react';
import { Check, CircleAlert, LoaderCircle, MessageCircle, Plus, Send, ShieldCheck, Sofa, Trash } from 'lucide-react';
import { toast } from 'sonner';
import { api, get, TAB, type Product } from '@/api';
import { cn } from '@/lib/utils';
import { useStore } from '@/store';
import { Panel, sizeText } from './bits';

interface Place { product: Product; x: number; z: number; rot: number; room: string; text: string; warnings?: string[] }
interface Step { tool: string; label: string; result?: unknown }
interface Msg { role: 'user' | 'assistant'; text: string; steps?: Step[]; places?: Place[]; approvals?: string[]; status: string; t?: string }
interface Conv { id: string; title: string; updated: string; count: number }
interface Status { configured: boolean; model: string; examples: string[]; notice?: string }

function PlaceCard({ p }: { p: Place }) {
  const [done, setDone] = useState(false);
  const home = useStore((s) => s.home);
  const room = home?.rooms.find((r) => r.id === p.room)?.name ?? p.room;
  const put = () => {
    const s = useStore.getState();
    s.place(p.product, [p.x, p.z]);
    const sel = useStore.getState().sel;
    if (sel?.kind === 'item' && p.rot) useStore.getState().updateItem(sel.id, { rot: ((p.rot % 360) + 360) % 360 });
    setDone(true);
    toast.success(`${p.product.family ? `${p.product.family} ${p.product.typeLabel}` : p.product.name} 放进${room}了`, { description: '拖动它可以再挪位置' });
  };
  return (
    <div className="mt-2 flex gap-3 rounded-2xl border border-primary/20 bg-white/85 p-2.5">
      <div className="grid size-14 shrink-0 place-items-center overflow-hidden rounded-xl bg-white">
        {p.product.image ? <img src={p.product.image} alt="" className="size-full object-contain" referrerPolicy="no-referrer" /> : <Sofa className="size-6 text-muted-foreground" />}
      </div>
      <div className="min-w-0 flex-1">
        <b className="block truncate text-[13px]">{p.product.family ? `${p.product.family} ${p.product.typeLabel}` : p.product.name}</b>
        <small className="block text-[11.5px] text-muted-foreground">{sizeText(p.product)} · 放进{room}</small>
        {p.warnings && p.warnings.length > 0 && (
          <ul className="mt-1 grid gap-0.5 text-[11.5px] text-destructive">{p.warnings.map((w, i) => <li key={i}>{w}</li>)}</ul>
        )}
        <button type="button" disabled={done} onClick={put} className="mt-1.5 flex h-7 items-center gap-1 rounded-lg bg-primary px-3 text-[12px] font-bold text-white disabled:bg-success">
          {done ? <><Check className="size-3.5" />已放进去</> : '放进去'}
        </button>
      </div>
    </div>
  );
}

function Bubble({ m }: { m: Msg }) {
  if (m.role === 'user') {
    return <div className="ml-auto max-w-[85%] rounded-2xl rounded-br-md bg-foreground px-3 py-2 text-[13px] text-white">{m.text}</div>;
  }
  return (
    <div className="max-w-[94%]">
      {m.steps && m.steps.length > 0 && (
        <div className="mb-1 flex flex-wrap gap-1">
          {m.steps.map((s, i) => (
            <span key={i} className="rounded-full bg-black/[0.05] px-2 py-0.5 text-[11px] text-muted-foreground">{s.label || s.tool}</span>
          ))}
        </div>
      )}
      {(m.text || m.status === 'running') && (
        <div className={cn('whitespace-pre-wrap rounded-2xl rounded-bl-md bg-white/85 px-3 py-2 text-[13px] leading-relaxed', m.status === 'failed' && 'text-destructive')}>
          {m.text}
          {m.status === 'running' && <LoaderCircle className="ml-1 inline size-3.5 animate-spin text-muted-foreground" />}
        </div>
      )}
      {m.places?.map((p, i) => <PlaceCard key={i} p={p} />)}
      {m.approvals?.map((a, i) => (
        <button key={i} type="button" onClick={() => useStore.getState().setPanel('auto')} className="mt-2 flex w-full items-center gap-2 rounded-2xl bg-warning-soft px-3 py-2 text-left text-[12px] font-semibold text-warning">
          <ShieldCheck className="size-4 shrink-0" />
          <span className="min-w-0 flex-1">「{a}」等你确认 · 去自动化面板</span>
        </button>
      ))}
    </div>
  );
}

/** Reads a server-sent event stream, one parsed JSON per event. */
async function readEvents(res: Response, on: (e: any) => void) {
  const reader = res.body!.getReader();
  const dec = new TextDecoder();
  let buf = '';
  for (;;) {
    const { value, done } = await reader.read();
    if (done) break;
    buf += dec.decode(value, { stream: true });
    let i: number;
    while ((i = buf.indexOf('\n\n')) >= 0) {
      const chunk = buf.slice(0, i);
      buf = buf.slice(i + 2);
      for (const line of chunk.split('\n')) {
        if (line.startsWith('data: ')) {
          try { on(JSON.parse(line.slice(6))); } catch { /* a partial line */ }
        }
      }
    }
  }
}

export function AssistantPanel() {
  const [status, setStatus] = useState<Status | null>(null);
  const [convs, setConvs] = useState<Conv[]>([]);
  const [cur, setCur] = useState<string | null>(null);
  const [msgs, setMsgs] = useState<Msg[]>([]);
  const [text, setText] = useState('');
  const [busy, setBusy] = useState(false);
  const end = useRef<HTMLDivElement>(null);
  const stop = useRef<AbortController | null>(null);

  const loadList = async () => {
    const list = await get<Conv[]>('api/assistant/conversations');
    setConvs(list ?? []);
    return list ?? [];
  };
  const open = async (id: string) => {
    setCur(id);
    const c = await get<{ messages: Msg[] }>(`api/assistant/conversations/${id}`);
    setMsgs(c.messages ?? []);
  };
  useEffect(() => {
    get<Status>('api/assistant/status').then(setStatus).catch(() => setStatus({ configured: false, model: '', examples: [], notice: '家庭助手没有接上' }));
    loadList().then((l) => { if (l[0]) open(l[0].id); });
    return () => stop.current?.abort();
  }, []);
  useEffect(() => { end.current?.scrollIntoView({ block: 'end' }); }, [msgs]);

  const fresh = async () => {
    const c = await api<{ id: string }>('POST', 'api/assistant/conversations');
    setCur(c.id);
    setMsgs([]);
    loadList();
    return c.id;
  };
  const drop = async (id: string) => {
    await api('DELETE', `api/assistant/conversations/${id}`);
    const l = await loadList();
    if (id === cur) {
      if (l[0]) open(l[0].id);
      else { setCur(null); setMsgs([]); }
    }
  };

  const send = async (q: string) => {
    q = q.trim();
    if (!q || busy) return;
    const id = cur ?? (await fresh());
    setText('');
    setBusy(true);
    const answer: Msg = { role: 'assistant', text: '', steps: [], places: [], approvals: [], status: 'running' };
    setMsgs((m) => [...m, { role: 'user', text: q, status: 'done' }, answer]);
    const patch = (f: (a: Msg) => Msg) => setMsgs((m) => [...m.slice(0, -1), f(m[m.length - 1])]);
    const ctl = new AbortController();
    stop.current = ctl;
    try {
      const res = await fetch(`api/assistant/conversations/${id}/messages`, {
        method: 'POST', headers: { 'Content-Type': 'application/json', 'X-Tab': TAB }, body: JSON.stringify({ text: q }), signal: ctl.signal,
      });
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        patch((a) => ({ ...a, text: body.error ?? `出错了（${res.status}）`, status: 'failed' }));
        return;
      }
      await readEvents(res, (e) => {
        switch (e.kind) {
          case 'tool_call': patch((a) => ({ ...a, steps: [...(a.steps ?? []), { tool: e.tool, label: e.label }] })); break;
          case 'delta': patch((a) => ({ ...a, text: a.text + e.text })); break;
          case 'reset': patch((a) => ({ ...a, text: '' })); break;
          case 'place': patch((a) => ({ ...a, places: [...(a.places ?? []), e.place] })); break;
          case 'approval': patch((a) => ({ ...a, approvals: [...(a.approvals ?? []), e.text] })); break;
          case 'highlight': useStore.setState({ highlight: e.nodes ?? [] }); break;
          case 'complete': patch((a) => ({ ...a, text: e.text || a.text, status: 'done' })); break;
          case 'error': patch((a) => ({ ...a, text: e.text, status: 'failed' })); break;
        }
      });
      patch((a) => (a.status === 'running' ? { ...a, status: 'done' } : a));
    } catch (err) {
      if ((err as Error).name !== 'AbortError') patch((a) => ({ ...a, text: (err as Error).message, status: 'failed' }));
    } finally {
      setBusy(false);
      stop.current = null;
      loadList();
    }
  };

  const off = status && !status.configured;
  return (
    <Panel wide title="家庭助手" sub={off ? '还没有配置模型' : '问家里的事，或者让它帮你做'}
      icon={<span className="grid size-9 place-items-center rounded-xl bg-[#fff1df] text-primary"><MessageCircle className="size-5" /></span>}
      onClose={() => useStore.getState().setPanel(null)}>
      <div className="flex min-h-[52dvh] flex-col gap-3 md:min-h-[420px]">
        {convs.length > 0 && (
          <div className="no-scrollbar -mx-1 flex gap-1.5 overflow-x-auto px-1 pb-0.5">
            <button type="button" onClick={fresh} className="flex h-7 shrink-0 items-center gap-1 rounded-full bg-foreground px-2.5 text-[11.5px] font-bold text-white"><Plus className="size-3.5" />新对话</button>
            {convs.map((c) => (
              <span key={c.id} className={cn('flex h-7 shrink-0 items-center rounded-full pl-2.5 text-[11.5px] font-semibold', c.id === cur ? 'bg-white shadow-sm' : 'bg-black/[0.05] text-muted-foreground')}>
                <button type="button" onClick={() => open(c.id)} className="max-w-[140px] truncate">{c.title || '新对话'}</button>
                <button type="button" onClick={() => drop(c.id)} className="grid size-7 place-items-center text-muted-foreground hover:text-destructive" aria-label="删掉这段对话"><Trash className="size-3" /></button>
              </span>
            ))}
          </div>
        )}
        {off && (
          <p className="flex items-start gap-2 rounded-2xl bg-warning-soft px-3 py-2.5 text-[12.5px] font-semibold text-warning">
            <CircleAlert className="mt-px size-4 shrink-0" />
            <span>{status?.notice ?? '还没有配置模型'}。其他功能照常能用。</span>
          </p>
        )}
        <div className="grid flex-1 content-start gap-2.5">
          {msgs.length === 0 && !off && (
            <div className="rounded-2xl bg-white/60 p-3 text-[12.5px] text-muted-foreground">
              它能看房间、家具和设备，开关灯和空调，算空调多久到温，读宜家链接看放不放得下。开门锁、把家具放进房间这类事，它只提出来，由你点确认。
            </div>
          )}
          {msgs.map((m, i) => <Bubble key={i} m={m} />)}
          <div ref={end} />
        </div>
        {msgs.length === 0 && status && status.examples.length > 0 && (
          <div className="flex flex-wrap gap-1.5">
            {status.examples.map((e) => (
              <button key={e} type="button" disabled={!!off} onClick={() => send(e)} className="max-w-full truncate rounded-full bg-white/80 px-2.5 py-1 text-left text-[11.5px] font-semibold shadow-sm disabled:opacity-50">{e}</button>
            ))}
          </div>
        )}
        <form onSubmit={(e) => { e.preventDefault(); send(text); }} className="sticky bottom-0 flex gap-2 bg-transparent pt-1">
          <input value={text} onChange={(e) => setText(e.target.value)} disabled={!!off} placeholder={off ? '配置模型后才能问' : '问点什么，比如：客厅现在多少度'} className="h-10 min-w-0 flex-1 rounded-xl border border-input bg-white px-3 text-[13px] disabled:opacity-60" />
          {busy
            ? <button type="button" onClick={() => stop.current?.abort()} className="grid size-10 shrink-0 place-items-center rounded-xl bg-secondary" aria-label="停下"><LoaderCircle className="size-4 animate-spin" /></button>
            : <button disabled={!!off || !text.trim()} className="grid size-10 shrink-0 place-items-center rounded-xl bg-primary text-white disabled:opacity-50" aria-label="发送"><Send className="size-4" /></button>}
        </form>
      </div>
    </Panel>
  );
}
