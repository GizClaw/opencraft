import { useEffect, useMemo, useRef, useState } from 'react';
import { Check, Copy, Loader2, RefreshCw } from 'lucide-react';
import { useTranslation } from 'react-i18next';

interface LogEntry {
  ts: string;
  level: string;
  message: string;
  attrs: { key: string; value: string }[];
}

function parseLog(raw: string): LogEntry[] {
  const out: LogEntry[] = [];
  for (const line of raw.split('\n')) {
    if (!line.trim()) continue;
    const m = line.match(/^(\S+)\s+(DEBUG|INFO|WARN|ERROR)\s+(.*)$/);
    if (!m) {
      out.push({ ts: '', level: '', message: line, attrs: [] });
      continue;
    }
    const [, ts, level, rest] = m;
    const tokens = rest.split(/\s+/).filter(Boolean);
    const messageParts: string[] = [];
    let i = 0;
    for (; i < tokens.length; i++) {
      if (/^[\w.-]+=/.test(tokens[i])) break;
      messageParts.push(tokens[i]);
    }
    const message = messageParts.join(' ');
    const attrs: LogEntry['attrs'] = [];
    // Values may be quoted and contain escaped quotes or spaces
    // (e.g. error.message="graph \"node\": failed").
    const re = /([\w.-]+)=("(?:[^"\\]|\\.)*"|(?:\S+))/g;
    let am: RegExpExecArray | null;
    while ((am = re.exec(tokens.slice(i).join(' '))) !== null) {
      attrs.push({
        key: am[1],
        value: am[2].replace(/^"|"$/g, '').replace(/\\(.)/g, '$1'),
      });
    }
    out.push({ ts, level, message, attrs });
  }
  return out;
}

function levelClass(level: string): string {
  switch (level) {
    case 'ERROR':
      return 'text-err';
    case 'WARN':
      return 'text-warn';
    case 'INFO':
      return 'text-fg';
    default:
      return 'text-dim';
  }
}

export function LogViewer({ fetchLogs }: { fetchLogs: () => Promise<string> }) {
  const [raw, setRaw] = useState('');
  const [level, setLevel] = useState('ALL');
  const [follow, setFollow] = useState(true);
  const [auto, setAuto] = useState(false);
  const [loading, setLoading] = useState(false);
  const [copied, setCopied] = useState(false);
  const [error, setError] = useState('');
  const scrollRef = useRef<HTMLDivElement>(null);
  const { t } = useTranslation();

  const load = async (quiet = false) => {
    if (!quiet) setLoading(true);
    try {
      setRaw(await fetchLogs());
      setError('');
    } catch (err) {
      setError(String(err));
    } finally {
      if (!quiet) setLoading(false);
    }
  };

  useEffect(() => {
    void load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    if (!auto) return;
    const timer = setInterval(() => void load(true), 3000);
    return () => clearInterval(timer);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [auto]);

  const entries = useMemo(() => parseLog(raw), [raw]);
  const counts = useMemo(() => {
    const c: Record<string, number> = { ALL: entries.length };
    for (const e of entries) c[e.level] = (c[e.level] ?? 0) + 1;
    return c;
  }, [entries]);
  const visible =
    level === 'ALL' ? entries : entries.filter((e) => e.level === level);

  useEffect(() => {
    if (follow && scrollRef.current) {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
    }
  }, [visible, follow]);

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(raw);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // clipboard unavailable
    }
  };

  const levels = ['ALL', 'ERROR', 'WARN', 'INFO', 'DEBUG'];

  return (
    <div className="flex h-full min-h-0 flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2">
        <div className="flex overflow-hidden rounded-lg border border-edge text-xs">
          {levels.map((lv) => (
            <button
              key={lv}
              onClick={() => setLevel(lv)}
              className={`px-2 py-1 ${
                level === lv
                  ? 'bg-accent text-white'
                  : 'text-dim hover:bg-panel2 hover:text-fg'
              }`}
            >
              {t(
                `config.logs${lv === 'ALL' ? 'All' : lv[0] + lv.slice(1).toLowerCase()}`,
              )}
              <span className="ml-1 opacity-60">{counts[lv] ?? 0}</span>
            </button>
          ))}
        </div>
        <span className="text-xs text-dim">
          {t('config.logsLines', { count: visible.length })}
        </span>
        <span className="flex-1" />
        <button
          onClick={() => setFollow((v) => !v)}
          className={`rounded-lg border px-2 py-1 text-xs ${
            follow
              ? 'border-accent/40 bg-accent/10 text-accent'
              : 'border-edge text-dim hover:text-fg'
          }`}
        >
          {t('config.logsFollow')}
        </button>
        <button
          onClick={() => setAuto((v) => !v)}
          className={`rounded-lg border px-2 py-1 text-xs ${
            auto
              ? 'border-accent/40 bg-accent/10 text-accent'
              : 'border-edge text-dim hover:text-fg'
          }`}
        >
          {t('config.logsAuto')}
        </button>
        <button
          onClick={() => void copy()}
          className="flex items-center gap-1 rounded-lg border border-edge px-2 py-1 text-xs text-dim hover:text-fg"
          aria-label={t('config.logsCopy')}
        >
          {copied ? <Check size={12} /> : <Copy size={12} />}
          {t('config.logsCopy')}
        </button>
        <button
          onClick={() => void load()}
          disabled={loading}
          className="flex items-center gap-1 rounded-lg border border-edge px-2 py-1 text-xs text-dim hover:text-fg disabled:opacity-50"
        >
          {loading ? (
            <Loader2 size={12} className="animate-spin" />
          ) : (
            <RefreshCw size={12} />
          )}
          {t('config.logsRefresh')}
        </button>
      </div>
      {error && <p className="text-xs text-err">{error}</p>}
      {entries.length === 0 && !error ? (
        <p className="text-sm text-dim">{t('config.logsEmpty')}</p>
      ) : (
        <div
          ref={scrollRef}
          className="flex-1 min-h-0 overflow-y-auto rounded-xl border border-edge bg-panel2"
        >
          {visible.map((e, i) => (
            <div
              key={i}
              className="flex items-start gap-2 border-b border-edge/40 px-3 py-1 hover:bg-panel"
            >
              <span className="w-28 shrink-0 truncate font-mono text-[11px] text-dim">
                {e.ts ? e.ts.replace('T', ' ').slice(5, 23) : ''}
              </span>
              <span
                className={`w-12 shrink-0 font-mono text-[11px] font-medium ${levelClass(e.level)}`}
              >
                {e.level || '—'}
              </span>
              <span className="min-w-0 flex-1 break-all font-mono text-[11px] leading-relaxed">
                <span className="text-fg">{e.message}</span>{' '}
                {e.attrs.map((a) => (
                  <span key={a.key} className="whitespace-nowrap">
                    <span className="text-dim/70">{a.key}=</span>
                    <span className="text-accent">{a.value}</span>{' '}
                  </span>
                ))}
              </span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
