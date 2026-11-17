import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import './index.css';
import { boot, useStore } from './store';

// scripted tours and checks reach the page's state here
(window as unknown as { twin: unknown }).twin = useStore;

const root = createRoot(document.getElementById('root')!);

boot()
  .then(async () => {
    const { default: App } = await import('./App');
    root.render(<StrictMode><App /></StrictMode>);
  })
  .catch((e) => {
    console.error(e);
    root.render(
      <div className="grid h-full place-items-center p-6">
        <div className="max-w-md rounded-2xl bg-white p-6 shadow-float">
          <h1 className="text-lg font-extrabold">连不上 HomeTwin 的服务</h1>
          <p className="mt-1 text-sm text-muted-foreground">{String(e?.message ?? e)}</p>
        </div>
      </div>,
    );
  });
