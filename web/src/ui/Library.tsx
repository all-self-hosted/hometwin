// 家具库: say what to put where and pick a model from the catalogue (说一句), or paste a product
// link and see what was read from it (宜家链接), or type in the size of something else.
import { useState } from 'react';
import { ExternalLink, LoaderCircle, Package, Plus, Ruler, Trash } from 'lucide-react';
import { toast } from 'sonner';
import { api, type Product } from '@/api';
import { Input } from '@/components/ui/input';
import { cn } from '@/lib/utils';
import { useStore } from '@/store';
import { Panel, money, sizeText } from './bits';
import { Finder } from './Finder';

const TYPES: [string, string][] = [
  ['sofa', '沙发'], ['armchair', '扶手椅'], ['bed', '床'], ['bookcase', '书柜'], ['wardrobe', '衣柜'], ['drawers', '抽屉柜'],
  ['desk', '书桌'], ['table', '桌子'], ['chair', '椅子'], ['tvbench', '电视柜'], ['shelf', '搁架'], ['lamp', '灯'], ['rug', '地毯'], ['box', '其他'],
];

const FROM: Record<string, string> = { measurements: '商品页的尺寸表', jsonld: '商品页的结构化数据', name: '商品名里的尺寸', manual: '手动填写', catalog: '模型库' };

export function ProductCard({ p, action }: { p: Product; action?: React.ReactNode }) {
  return (
    <article className="flex w-full min-w-0 gap-3 rounded-2xl bg-white/80 p-2.5 shadow-sm">
      <div className="grid size-[72px] shrink-0 place-items-center overflow-hidden rounded-xl bg-white">
        {p.image ? <img src={p.image} alt="" className="size-full object-contain" loading="lazy" referrerPolicy="no-referrer" /> : <Package className="size-7 text-muted-foreground" />}
      </div>
      <div className="min-w-0 flex-1">
        <h4 className="truncate text-[13.5px] font-extrabold">{p.family ? `${p.family} ${p.typeLabel}` : p.name}</h4>
        <p className="truncate text-[11.5px] text-muted-foreground">{p.name}</p>
        <p className="mt-1 flex flex-wrap items-center gap-x-2 text-[12px]">
          <b className="tabular-nums">{sizeText(p)}</b>
          {p.price ? <span className="text-muted-foreground">{money(p.price, p.currency)}</span> : null}
          {p.source === 'polyfork' && <span className="text-[11px] text-muted-foreground">模型库 · CC BY 4.0</span>}
        </p>
        <div className="mt-1.5 flex items-center gap-2">
          {action}
          {p.url && <a href={p.url} target="_blank" rel="noreferrer" className="grid size-7 place-items-center rounded-lg text-muted-foreground hover:bg-secondary" aria-label="打开商品页"><ExternalLink className="size-3.5" /></a>}
        </div>
      </div>
    </article>
  );
}

function Manual({ onDone }: { onDone(p: Product): void }) {
  const [f, setF] = useState({ name: '', type: 'box', w: '', d: '', h: '', color: '#e9e7e1' });
  const [busy, setBusy] = useState(false);
  const set = (k: keyof typeof f) => (e: React.ChangeEvent<HTMLInputElement | HTMLSelectElement>) => setF({ ...f, [k]: e.target.value });
  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setBusy(true);
    try {
      const r = await api<{ product: Product; library: Product[] }>('POST', 'api/furniture/manual', { name: f.name, type: f.type, w: Number(f.w), d: Number(f.d), h: Number(f.h), colorHex: f.color });
      useStore.setState({ library: r.library });
      onDone(r.product);
    } catch (err) {
      toast.error((err as Error).message);
    } finally {
      setBusy(false);
    }
  };
  return (
    <form onSubmit={submit} className="grid gap-2 rounded-2xl bg-white/60 p-3">
      <div className="grid grid-cols-[1fr_auto] gap-2">
        <Input placeholder="名字，比如：旧书桌" value={f.name} onChange={set('name')} required className="h-9 bg-white" />
        <select value={f.type} onChange={set('type')} className="h-9 rounded-md border border-input bg-white px-2 text-[13px]">
          {TYPES.map(([v, l]) => <option key={v} value={v}>{l}</option>)}
        </select>
      </div>
      <div className="grid grid-cols-[1fr_1fr_1fr_auto] items-center gap-2">
        {(['w', 'd', 'h'] as const).map((k) => (
          <label key={k} className="relative">
            <Input type="number" min={1} max={600} step="0.1" required placeholder={{ w: '宽', d: '深', h: '高' }[k]} value={f[k]} onChange={set(k)} className="h-9 bg-white pr-7" />
            <span className="pointer-events-none absolute right-2 top-1/2 -translate-y-1/2 text-[11px] text-muted-foreground">cm</span>
          </label>
        ))}
        <input type="color" value={f.color} onChange={set('color')} className="h-9 w-9 cursor-pointer rounded-md border border-input bg-white p-1" aria-label="颜色" />
      </div>
      <button disabled={busy} className="h-9 rounded-xl bg-foreground text-[13px] font-bold text-white disabled:opacity-50">加到家具库</button>
    </form>
  );
}

export function Library() {
  const library = useStore((s) => s.library);
  const place = useStore((s) => s.place);
  const close = () => useStore.getState().setPanel(null);
  const [url, setUrl] = useState('');
  const [busy, setBusy] = useState(false);
  const [last, setLast] = useState<Product | null>(null);
  const [manual, setManual] = useState(false);
  const [tab, setTab] = useState<'say' | 'link'>('say');

  const load = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!url.trim()) return;
    setBusy(true);
    setLast(null);
    try {
      const r = await api<{ product: Product; library: Product[] }>('POST', 'api/furniture/import', { url });
      setLast(r.product);
      useStore.setState({ library: r.library });
      setUrl('');
    } catch (err) {
      toast.error((err as Error).message);
    } finally {
      setBusy(false);
    }
  };
  const drop = async (i: number) => {
    const lib = await api<Product[]>('DELETE', `api/library/${i}`);
    useStore.setState({ library: lib });
  };
  const putIn = (p: Product) => {
    place(p);
    toast.success(`${p.family ? `${p.family} ${p.typeLabel}` : p.name} 放进去了`, { description: '拖动它挪位置，Q / E 转 90°' });
  };

  return (
    <Panel wide title="家具库" sub="说一句或贴宜家链接，按真实尺寸放进家里" icon={<span className="grid size-9 place-items-center rounded-xl bg-[#fff1df] text-primary"><Package className="size-5" /></span>} onClose={close}>
      <div className="mb-3 grid grid-cols-2 gap-1 rounded-xl bg-secondary/70 p-1" role="tablist">
        {([['say', '说一句'], ['link', '宜家链接']] as const).map(([k, l]) => (
          <button key={k} type="button" role="tab" aria-selected={tab === k} onClick={() => setTab(k)} className={cn('h-8 rounded-lg text-[13px] font-bold', tab === k ? 'bg-white text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground')}>{l}</button>
        ))}
      </div>
      {tab === 'say' && <Finder />}
      {tab === 'link' && <>
      <form onSubmit={load} className="flex gap-2">
        <Input value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://www.ikea.com/…/p/…" className="h-10 bg-white" inputMode="url" />
        <button disabled={busy} className="flex h-10 shrink-0 items-center gap-1.5 rounded-xl bg-primary px-3.5 text-[13px] font-bold text-white disabled:opacity-60">
          {busy ? <LoaderCircle className="size-4 animate-spin" /> : null}{busy ? '读取中' : '读取'}
        </button>
      </form>
      {last && (
        <div className="mt-3 grid gap-1.5">
          <ProductCard p={last} action={<button type="button" onClick={() => putIn(last)} className="h-7 rounded-lg bg-primary px-3 text-[12px] font-bold text-white">放进房间</button>} />
          <p className="px-1 text-[11.5px] text-muted-foreground">尺寸来自{FROM[last.dimsFrom] ?? last.dimsFrom}{last.packages?.length ? `，${last.packages.length} 个包装` : ''}{last.measures?.find((m) => m.key === 'seatHeight') ? `，座高 ${Math.round(last.measures.find((m) => m.key === 'seatHeight')!.cm)} cm` : ''}</p>
        </div>
      )}
      <button type="button" onClick={() => setManual(!manual)} className={cn('mt-3 flex w-full items-center gap-2 rounded-xl px-1 py-1.5 text-[12.5px] font-bold text-muted-foreground hover:text-foreground')}>
        <Ruler className="size-4" />不是宜家的？手动填尺寸
      </button>
      {manual && <Manual onDone={(p) => { setManual(false); setLast(p); }} />}
      </>}
      <h3 className="mb-2 mt-4 text-[12px] font-bold text-muted-foreground">库里有 {library.length} 件</h3>
      <div className="grid gap-2">
        {library.map((p, i) => (
          <ProductCard key={`${p.url ?? p.name}-${i}`} p={p} action={
            <>
              <button type="button" onClick={() => putIn(p)} className="flex h-7 items-center gap-1 rounded-lg bg-foreground px-2.5 text-[12px] font-bold text-white"><Plus className="size-3.5" />放进房间</button>
              <button type="button" onClick={() => drop(i)} className="grid size-7 place-items-center rounded-lg text-muted-foreground hover:bg-secondary" aria-label="从库里删掉"><Trash className="size-3.5" /></button>
            </>
          } />
        ))}
        {library.length === 0 && <p className="rounded-2xl bg-white/50 p-3 text-[12.5px] text-muted-foreground">还没有家具。试试说一句「客厅放一个沙发」，或者贴一个宜家商品页的链接。</p>}
      </div>
    </Panel>
  );
}
