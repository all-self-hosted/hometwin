// 接入 Home Assistant: where HA is, whether HomeTwin is connected, and which HA entities stand
// for which devices in which room. The person confirms every mapping.
import { useCallback, useEffect, useMemo, useState } from 'react';
import { CircleAlert, House, Link2, LoaderCircle, RefreshCw, Unlink, Wand2 } from 'lucide-react';
import { toast } from 'sonner';
import { api, get } from '@/api';
import { Input } from '@/components/ui/input';
import { cn } from '@/lib/utils';
import { useStore } from '@/store';
import { KIND_NAME, Panel } from './bits';

export interface HAStatus {
  url: string; connected: boolean; error?: string; version?: string; locationName?: string;
  entities: number; mapped: number; lastEvent?: string; tokenFound?: boolean; needToken?: string;
}
interface Suggestion { room: string; x: number; y: number; z: number; rot: number; width?: number; door?: string; takeOver?: string }
interface Mapping { id?: string; entities: string[]; kind: string; name: string; room: string; x?: number; y?: number; z?: number; rot?: number; takeOver?: boolean }
interface Entity {
  entity_id: string; domain: string; name: string; state: string; area?: string; device?: string;
  kind?: string; supported: boolean; suggestion?: Suggestion; mapped?: Mapping | null;
}

const OWNER_STEPS = (url: string) => [
  `打开 ${url}，按提示建好主人账号（只在这台电脑上）`,
  '点左下角头像 → 安全 → 长期访问令牌 → 创建令牌，复制它',
  '在终端运行下面这行，按提示粘贴令牌（令牌不要贴到聊天或命令行里）：',
];
const KEYCHAIN_CMD = 'security add-generic-password -a hometwin -s hometwin-ha -w';

export function HAPanel() {
  const home = useStore((s) => s.home)!;
  const [status, setStatus] = useState<HAStatus | null>(null);
  const [entities, setEntities] = useState<Entity[]>([]);
  const [url, setUrl] = useState('');
  const [busy, setBusy] = useState<string | null>(null);
  const [rooms, setRooms] = useState<Record<string, string>>({});
  const rawStatus = useCallback(async () => {
    try {
      const s = await get<HAStatus>('/api/ha/status');
      setStatus(s);
      setUrl((u) => u || s.url);
      if (s.connected) setEntities((await get<Entity[]>('/api/ha/entities')) ?? []);
    } catch (e) {
      setStatus({ url: '', connected: false, error: (e as Error).message, entities: 0, mapped: 0 });
    }
  }, []);
  useEffect(() => {
    rawStatus();
    const t = setInterval(rawStatus, 5000);
    return () => clearInterval(t);
  }, [rawStatus]);

  const roomName = (id?: string) => (id === 'outdoors' ? '室外' : home.rooms.find((r) => r.id === id)?.name ?? '没认出房间');
  const saveURL = async () => {
    try {
      await api('PUT', '/api/ha/config', { url });
      toast.success('已保存，正在连接');
      rawStatus();
    } catch (e) { toast.error((e as Error).message); }
  };
  const link = async (e: Entity) => {
    const room = rooms[e.entity_id] ?? e.suggestion?.room ?? '';
    if (!room) { toast.error('先选一个房间'); return; }
    setBusy(e.entity_id);
    const sameRoom = room === e.suggestion?.room;
    const body: Mapping = { entities: [e.entity_id], kind: e.kind!, name: e.name, room, ...(sameRoom && e.suggestion ? { x: e.suggestion.x, y: e.suggestion.y, z: e.suggestion.z, rot: e.suggestion.rot, takeOver: !!e.suggestion.takeOver } : {}) };
    try {
      await api('PUT', '/api/ha/mapping', body);
      toast.success(`${e.name} 接到了${roomName(room)}`);
      rawStatus();
    } catch (err) { toast.error((err as Error).message); } finally { setBusy(null); }
  };
  const unlink = async (e: Entity) => {
    setBusy(e.entity_id);
    try {
      await api('DELETE', `/api/ha/mapping/${encodeURIComponent(e.entity_id)}`);
      rawStatus();
    } catch (err) { toast.error((err as Error).message); } finally { setBusy(null); }
  };
  const auto = async () => {
    setBusy('auto');
    try {
      const r = await api<{ applied?: number }>('POST', '/api/ha/mapping/auto');
      toast.success(`按建议接入了 ${r?.applied ?? ''} 个设备`);
      rawStatus();
    } catch (err) { toast.error((err as Error).message); } finally { setBusy(null); }
  };

  const groups = useMemo(() => {
    const supported = entities.filter((e) => e.supported);
    return {
      mapped: supported.filter((e) => e.mapped),
      open: supported.filter((e) => !e.mapped),
      other: entities.filter((e) => !e.supported),
    };
  }, [entities]);
  const noToken = status && status.tokenFound === false;

  return (
    <Panel wide title="接入 Home Assistant" sub={status?.connected ? `已连接 · ${status.locationName ?? ''} ${status.version ? `· HA ${status.version}` : ''}` : '把家里真实的设备接进来'}
      icon={<span className="grid size-9 place-items-center rounded-xl bg-[#e6f1fb] text-[#1f78c8]"><House className="size-5" /></span>}
      onClose={() => useStore.getState().setPanel(null)}>
      <div className="grid gap-3">
        <section className="rounded-2xl bg-white/70 p-3">
          <div className="flex items-center gap-2 text-[12.5px] font-bold">
            <i className={cn('size-2.5 rounded-full', status?.connected ? 'bg-success' : 'bg-warning')} />
            {status ? (status.connected ? `已连接，${status.entities} 个实体，接入了 ${status.mapped} 个` : '没连上') : '查看中…'}
            <button type="button" onClick={() => api('POST', '/api/ha/reconnect').then(rawStatus).catch((e) => toast.error(e.message))} className="ml-auto flex items-center gap-1 rounded-lg px-2 py-1 text-muted-foreground hover:bg-secondary"><RefreshCw className="size-3.5" />重连</button>
          </div>
          <div className="mt-2 flex gap-2">
            <Input value={url} onChange={(e) => setUrl(e.target.value)} placeholder="http://127.0.0.1:38123" className="h-9 bg-white" />
            <button type="button" onClick={saveURL} className="h-9 shrink-0 rounded-xl bg-foreground px-3 text-[12.5px] font-bold text-white">保存</button>
          </div>
          {status?.error && !noToken && <p className="mt-2 flex gap-1.5 text-[12px] text-destructive"><CircleAlert className="mt-px size-3.5 shrink-0" />{status.error}</p>}
          {noToken && (
            <div className="mt-2 rounded-xl bg-warning-soft p-2.5 text-[12px] text-foreground">
              <b className="text-warning">还差一步：放一个访问令牌</b>
              <ol className="mt-1 list-decimal pl-4">
                {OWNER_STEPS(status!.url || url).map((s) => <li key={s} className="mt-0.5">{s}</li>)}
              </ol>
              <code className="mt-1 block select-all rounded-lg bg-white px-2 py-1.5 font-mono text-[11.5px]">{KEYCHAIN_CMD}</code>
              <p className="mt-1 text-muted-foreground">放好后点「重连」。令牌只存在钥匙串里，HomeTwin 不会显示它。</p>
            </div>
          )}
        </section>

        {status?.connected && (
          <>
            {groups.open.length > 0 && (
              <section>
                <div className="mb-1.5 flex items-center px-1">
                  <h4 className="text-[12px] font-bold text-muted-foreground">没接入 {groups.open.length}</h4>
                  <button type="button" disabled={busy === 'auto'} onClick={auto} className="ml-auto flex h-7 items-center gap-1 rounded-lg bg-primary px-2.5 text-[12px] font-bold text-white disabled:opacity-50">
                    {busy === 'auto' ? <LoaderCircle className="size-3.5 animate-spin" /> : <Wand2 className="size-3.5" />}按建议全部接入
                  </button>
                </div>
                <ul className="grid gap-1.5">
                  {groups.open.map((e) => (
                    <li key={e.entity_id} className="rounded-2xl bg-white/70 p-2.5">
                      <div className="flex items-start gap-2">
                        <div className="min-w-0 flex-1">
                          <b className="block truncate text-[13px]">{e.name}</b>
                          <small className="block truncate text-[11px] text-muted-foreground">{e.entity_id} · {KIND_NAME[e.kind as keyof typeof KIND_NAME] ?? e.kind} · {e.state}{e.area ? ` · HA 区域：${e.area}` : ''}</small>
                        </div>
                      </div>
                      <div className="mt-2 flex items-center gap-2">
                        <select value={rooms[e.entity_id] ?? e.suggestion?.room ?? ''} onChange={(ev) => setRooms({ ...rooms, [e.entity_id]: ev.target.value })} className="h-8 min-w-0 flex-1 rounded-lg border border-input bg-white px-2 text-[12.5px]">
                          <option value="">选房间…</option>
                          {home.rooms.map((r) => <option key={r.id} value={r.id}>{r.name}</option>)}
                          <option value="outdoors">室外</option>
                        </select>
                        {e.suggestion?.takeOver && (rooms[e.entity_id] ?? e.suggestion.room) === e.suggestion.room && <span className="text-[11px] text-muted-foreground">替换模拟的 {e.suggestion.takeOver}</span>}
                        <button type="button" disabled={busy === e.entity_id} onClick={() => link(e)} className="flex h-8 items-center gap-1 rounded-lg bg-foreground px-2.5 text-[12px] font-bold text-white disabled:opacity-50"><Link2 className="size-3.5" />接入</button>
                      </div>
                    </li>
                  ))}
                </ul>
              </section>
            )}
            {groups.mapped.length > 0 && (
              <section>
                <h4 className="mb-1.5 px-1 text-[12px] font-bold text-muted-foreground">已接入 {groups.mapped.length}</h4>
                <ul className="grid gap-1.5">
                  {groups.mapped.map((e) => (
                    <li key={e.entity_id} className="flex items-center gap-2 rounded-2xl bg-white/70 p-2.5">
                      <button type="button" className="min-w-0 flex-1 text-left" onClick={() => e.mapped?.id && useStore.getState().select({ kind: 'device', id: e.mapped.id })}>
                        <b className="block truncate text-[13px]">{e.mapped?.name ?? e.name}</b>
                        <small className="block truncate text-[11px] text-muted-foreground">{roomName(e.mapped?.room)} · {e.entity_id} · {e.state}</small>
                      </button>
                      <button type="button" disabled={busy === e.entity_id} onClick={() => unlink(e)} className="flex h-8 items-center gap-1 rounded-lg bg-white px-2.5 text-[12px] font-bold shadow-sm disabled:opacity-50"><Unlink className="size-3.5" />取消</button>
                    </li>
                  ))}
                </ul>
              </section>
            )}
            {groups.other.length > 0 && (
              <details className="rounded-2xl bg-white/50 p-2.5 text-[12px]">
                <summary className="cursor-pointer font-bold text-muted-foreground">HomeTwin 还不认识的 {groups.other.length} 个实体</summary>
                <ul className="mt-1.5 grid gap-0.5 text-muted-foreground">
                  {groups.other.slice(0, 60).map((e) => <li key={e.entity_id} className="truncate">{e.name} · {e.entity_id}</li>)}
                </ul>
              </details>
            )}
          </>
        )}
      </div>
    </Panel>
  );
}
