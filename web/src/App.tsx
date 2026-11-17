import { useEffect } from 'react';
import { Toaster } from '@/components/ui/sonner';
import { Scene } from '@/scene/Scene';
import { useStore } from '@/store';
import { Confirm, Dock, Hints, TopBar } from '@/ui/Chrome';
import { AssistantPanel } from '@/ui/Assistant';
import { HAPanel } from '@/ui/HAPanel';
import { ImportPanel } from '@/ui/Import';
import { Library } from '@/ui/Library';
import { AutoPanel, DevicesPanel, TwinPanel } from '@/ui/Panels';
import { Selection, turn } from '@/ui/Selection';

function useKeys() {
  useEffect(() => {
    const on = (e: KeyboardEvent) => {
      if ((e.target as HTMLElement).closest('input, textarea, select, [role="dialog"]')) return;
      const s = useStore.getState();
      const it = s.sel?.kind === 'item' ? s.items.find((x) => x.id === s.sel!.id) : undefined;
      const mod = e.metaKey || e.ctrlKey;
      if (mod && e.key.toLowerCase() === 'z') { e.preventDefault(); if (e.shiftKey) s.redo(); else s.undo(); return; }
      if (mod && e.key.toLowerCase() === 'y') { e.preventDefault(); s.redo(); return; }
      if (e.key === 'Escape') s.select(null);
      else if (it && (e.key === 'q' || e.key === 'Q')) turn(it, e.shiftKey ? -15 : -90);
      else if (it && (e.key === 'e' || e.key === 'E')) turn(it, e.shiftKey ? 15 : 90);
      else if (it && (e.key === 'Delete' || e.key === 'Backspace')) s.removeItem(it.id);
      else if (e.key === 'h' || e.key === 'H') s.camera('home');
      else if (e.key === 'n' || e.key === 'N') s.setNight(!s.night);
    };
    window.addEventListener('keydown', on);
    return () => window.removeEventListener('keydown', on);
  }, []);
}

export default function App() {
  useKeys();
  const home = useStore((s) => s.home)!;
  const panel = useStore((s) => s.panel);
  return (
    <div className="fixed inset-0 overflow-hidden bg-stage">
      <Scene home={home} />
      <TopBar />
      {panel === 'library' && <Library />}
      {panel === 'devices' && <DevicesPanel />}
      {panel === 'auto' && <AutoPanel />}
      {panel === 'twin' && <TwinPanel />}
      {panel === 'assistant' && <AssistantPanel />}
      {panel === 'ha' && <HAPanel />}
      {panel === 'import' && <ImportPanel />}
      <Selection />
      <Hints />
      <Dock />
      <Confirm />
      <Toaster position="top-center" offset={{ top: 76 }} mobileOffset={{ top: 'calc(64px + env(safe-area-inset-top))' }} />
    </div>
  );
}
