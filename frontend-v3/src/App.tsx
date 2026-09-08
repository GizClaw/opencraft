import { useEffect, useState } from 'react';
import { Events } from '@wailsio/runtime';
import { Shell } from '../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop';

function windowLabel(): string {
  return window.location.hash.includes('second') ? 'second' : 'main';
}

export default function App() {
  const [lines, setLines] = useState<string[]>([`window=${windowLabel()}`]);
  const [result, setResult] = useState<string>(' ');

  useEffect(() => {
    const auto = window.location.hash.includes('auto');
    const off = Events.On('v3:log', (evt: any) => {
      const text = String(evt?.data ?? evt);
      setLines((prev) => [...prev.slice(-60), text]);
      if (auto && text.includes('auto app ready')) {
        void (async () => {
          await Shell.ReportProbe('frontend-alive', windowLabel());
          await Shell.OpenSecondWindow();
          await new Promise((r) => setTimeout(r, 2500));
          await Shell.ReportProbe('second-window', await Shell.WindowInfo());
          await Shell.CloseSecondWindow();
        })();
      }
    });
    return () => {
      if (typeof off === 'function') off();
    };
  }, []);

  const act = (label: string, p: Promise<string>) => {
    setResult(`${label}: ...`);
    p.then((v) => setResult(`${label}: ${v}`)).catch((e) =>
      setResult(`${label}: ERROR ${e}`),
    );
  };

  return (
    <div style={{ fontFamily: 'inherit' }}>
      <h1 style={{ marginTop: 0 }}>OpenCraft v3 skeleton — {windowLabel()}</h1>
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8 }}>
        <button onClick={() => act('version', Shell.Version())}>Version</button>
        <button onClick={() => act('bcast', Shell.Broadcast('hello from UI'))}>
          Broadcast (Go)
        </button>
        <button onClick={() => act('open2', Shell.OpenSecondWindow())}>
          Open 2nd window
        </button>
        <button onClick={() => act('close2', Shell.CloseSecondWindow())}>
          Close 2nd window
        </button>
        <button onClick={() => act('winfo', Shell.WindowInfo())}>Windows</button>
        <button
          onClick={() => {
            Events.Emit('v3:log', `js emit from ${windowLabel()}`);
            act('jsemit', Promise.resolve('emitted'));
          }}
        >
          Emit from JS
        </button>
        <button onClick={() => act('quit', Shell.Quit())}>Quit</button>
      </div>
      <p style={{ color: '#9fd0ff', minHeight: '1.2em' }}>{result}</p>
      <pre
        style={{
          background: '#171b24',
          border: '1px solid #2a3040',
          borderRadius: 8,
          padding: 10,
          minHeight: 160,
          whiteSpace: 'pre-wrap',
        }}
      >
        {lines.join('\n')}
      </pre>
    </div>
  );
}
