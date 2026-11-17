// 说一句: say what to put where (「卧室A 放一个床头柜」), pick one of the models found in the
// catalogue, and it goes into that room at its real size.
import { useState } from 'react';
import { LoaderCircle, MapPin, Plus, Sparkles } from 'lucide-react';
import { toast } from 'sonner';
import { api, type Hit, type Product } from '@/api';
import { Input } from '@/components/ui/input';
import { cn } from '@/lib/utils';
import { useStore } from '@/store';

const EXAMPLES = ['卧室A 放一个床头柜', '客厅加一盆大绿植', '书房来一张书桌', 'upright piano'];

interface Found { query: string; room: string; hits: Hit[] }

/** The middle of a room's outline, where a new piece first stands. */
function roomCentre(id: string): [number, number] | undefined {
  const r = useStore.getState().home?.rooms.find((x) => x.id === id);
  if (!r) return undefined;
  const xs = r.poly.map((p) => p[0]), zs = r.poly.map((p) => p[1]);
  return [(Math.min(...xs) + Math.max(...xs)) / 2, (Math.min(...zs) + Math.max(...zs)) / 2];
}

export function Finder() {
  const rooms = useStore((s) => s.home?.rooms ?? []);
  const [text, setText] = useState('');
  const [busy, setBusy] = useState(false);
  const [found, setFound] = useState<Found | null>(null);
  const [room, setRoom] = useState('');
  const [adding, setAdding] = useState<string | null>(null);

  const find = async (say: string) => {
    if (!say.trim() || busy) return;
    setBusy(true);
    try {
      const r = await api<Found>('POST', '/api/catalog/find', { text: say });
      setFound(r);
      const sel = useStore.getState().sel;
      setRoom(r.room || (sel?.kind === 'room' ? sel.id : ''));
      if (r.hits.length === 0) toast.message(`模型库里没找到「${r.query}」`, { description: '换个说法试试，或者直接写英文' });
    } catch (err) {
      toast.error((err as Error).message);
    } finally {
      setBusy(false);
    }
  };

  const put = async (h: Hit) => {
    setAdding(h.id);
    try {
      const r = await api<{ product: Product; library: Product[] }>('POST', '/api/catalog/add', { id: h.id });
      useStore.setState({ library: r.library });
      useStore.getState().place(r.product, room ? roomCentre(room) : undefined);
      const where = rooms.find((x) => x.id === room)?.name;
      toast.success(`${h.title} 放进${where ?? '家里'}了`, { description: '拖动它挪位置，Q / E 转 90°' });
    } catch (err) {
      toast.error((err as Error).message);
    } finally {
      setAdding(null);
    }
  };

  return (
    <div className="grid gap-2.5">
      <form onSubmit={(e) => { e.preventDefault(); find(text); }} className="flex gap-2">
        <Input value={text} onChange={(e) => setText(e.target.value)} placeholder="说说要放什么，比如：卧室A 放一个床头柜" className="h-10 bg-white" enterKeyHint="search" />
        <button disabled={busy || !text.trim()} className="flex h-10 shrink-0 items-center gap-1.5 rounded-xl bg-primary px-3.5 text-[13px] font-bold text-white disabled:opacity-50">
          {busy ? <LoaderCircle className="size-4 animate-spin" /> : <Sparkles className="size-4" />}{busy ? '在找' : '找'}
        </button>
      </form>

      {!found && (
        <div className="flex flex-wrap gap-1.5">
          {EXAMPLES.map((e) => (
            <button key={e} type="button" onClick={() => { setText(e); find(e); }} className="rounded-full bg-white/70 px-2.5 py-1 text-[12px] font-semibold text-muted-foreground hover:bg-white hover:text-foreground">{e}</button>
          ))}
        </div>
      )}

      {found && (
        <>
          <div className="flex flex-wrap items-center gap-x-2 gap-y-1.5 px-0.5 text-[12px] text-muted-foreground">
            <span>按「<b className="text-foreground">{found.query}</b>」找到 {found.hits.length} 个</span>
            <label className="ml-auto flex items-center gap-1">
              <MapPin className="size-3.5" />
              <select value={room} onChange={(e) => setRoom(e.target.value)} className="h-7 rounded-lg border border-input bg-white px-1.5 text-[12px] font-semibold text-foreground" aria-label="放进哪个房间">
                <option value="">家里中间</option>
                {rooms.map((r) => <option key={r.id} value={r.id}>{r.name}</option>)}
              </select>
            </label>
          </div>
          <div className="grid grid-cols-2 gap-2">
            {found.hits.map((h) => (
              <article key={h.id} className="flex min-w-0 flex-col overflow-hidden rounded-2xl bg-white/85 shadow-sm">
                <div className="relative aspect-[4/3] overflow-hidden bg-[#f4f1ec]">
                  <img src={h.image} alt="" loading="lazy" referrerPolicy="no-referrer" className="absolute inset-0 size-full object-contain" />
                </div>
                <div className="flex min-w-0 flex-1 flex-col gap-1 p-2">
                  <h4 className="truncate text-[12.5px] font-extrabold" title={h.title}>{h.title}</h4>
                  <p className="text-[11.5px] tabular-nums text-muted-foreground">{Math.round(h.w)} × {Math.round(h.d)} × {Math.round(h.h)} cm</p>
                  <button type="button" disabled={!!adding} onClick={() => put(h)} className={cn('mt-auto flex h-7 items-center justify-center gap-1 rounded-lg bg-foreground text-[12px] font-bold text-white disabled:opacity-50')}>
                    {adding === h.id ? <LoaderCircle className="size-3.5 animate-spin" /> : <Plus className="size-3.5" />}放进去
                  </button>
                </div>
              </article>
            ))}
          </div>
          <p className="px-0.5 text-[11px] leading-relaxed text-muted-foreground">模型来自 <a href="https://polyfork.dev" target="_blank" rel="noreferrer" className="underline">Polyfork</a>，免费、按真实尺寸，CC BY 4.0。</p>
        </>
      )}
    </div>
  );
}
