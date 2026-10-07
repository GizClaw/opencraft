import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  AlertTriangle,
  Ban,
  Clock,
  FileText,
  Send,
  Square,
} from 'lucide-react';
import { api } from '../lib/api';
import { UIEventType } from '../lib/events';
import {
  friendlyFailure,
  friendlyInterruption,
  isUserStop,
} from '../lib/store';
import type {
  HistoryMessage,
  StreamPart,
  TurnMessage,
  UIEvent,
} from '../lib/types';
import { subscribeAppEvents } from '../apps/host';
import { Markdown } from './Markdown';
import { Button } from './ui/Button';
import { ICON } from './ui/icon';
import { Input } from './ui/Input';
import { Textarea } from './ui/Textarea';
import type * as genApps from '../../bindings/github.com/GizClaw/opencraft/internal/capabilities/apps/models';
import type * as gen from '../../bindings/github.com/GizClaw/opencraft/internal/adapters/desktop/bindings/models';

interface AppMessage {
  id: string;
  role: 'user' | 'assistant';
  text: string;
  /** reasoning is the model's own trace, shown collapsed. */
  reasoning?: string;
  /** streaming marks the block the model is still writing. */
  streaming?: boolean;
}

interface AppTurnEnd {
  status: string;
  error?: string;
  errorKind?: string;
  interruptCause?: string;
}

let seq = 0;
const nextID = () => `a${++seq}`;

/** partsToMessage folds one wire message into a transcript row. */
function historyRow(msg: HistoryMessage): AppMessage | null {
  const role = msg.role === 'user' ? 'user' : 'assistant';
  const parts = msg.content?.parts ?? [];
  const texts: string[] = [];
  const thoughts: string[] = [];
  for (const part of parts) {
    if (part.type === 'text' && part.text) texts.push(part.text);
    if (part.type === 'reasoning' && part.text) thoughts.push(part.text);
  }
  const text = texts.join('\n\n');
  if (!text && thoughts.length === 0) return null;
  return {
    id: nextID(),
    role,
    text,
    reasoning: thoughts.join('\n\n') || undefined,
  };
}

/**
 * AppChat is the built-in conversation every application has (the app
 * platform plan, §4.2): the same engine and the same stream as the
 * assistant's chat, but scoped to this application — its Host, its
 * session store, its private workspace.
 *
 * It is deliberately not the main chat surface with an id swapped in.
 * An application has no workspace, no files panel, no steering queue and
 * no session list in the sidebar; what it has is a conversation it can
 * start, watch and stop, with the files the turn produced underneath it.
 */
export function AppChat({
  appID,
  manifest,
}: {
  appID: string;
  manifest: genApps.Manifest;
}) {
  const { t } = useTranslation();
  const [conversationID, setConversationID] = useState('');
  const [messages, setMessages] = useState<AppMessage[]>([]);
  const [runID, setRunID] = useState('');
  const [pending, setPending] = useState(false);
  const [end, setEnd] = useState<AppTurnEnd | null>(null);
  const [artifacts, setArtifacts] = useState<string[]>([]);
  const [model, setModel] = useState(manifest.defaults?.model ?? '');
  const [think, setThink] = useState(manifest.defaults?.think_level ?? '');
  // The agents this package declares: what its manifest names as the
  // entry (a turn that names none runs it) and the ones it lists. An
  // application with one agent has nothing to pick, so it gets no
  // picker; one with several is a package that plays several roles, and
  // the same conversation can host all of them.
  const agents = useMemo(() => {
    const entry = manifest.agent || '';
    return entry ? [entry, ...(manifest.agents ?? [])] : [];
  }, [manifest.agent, manifest.agents]);
  const [agent, setAgent] = useState('');
  const [draft, setDraft] = useState('');
  const convRef = useRef('');
  const runRef = useRef('');

  convRef.current = conversationID;
  runRef.current = runID;

  // The newest conversation is what the page opens on. An application
  // that has never been talked to has none, and one is minted on the
  // first send — the id the host would have minted anyway, taken early
  // so the turn names its own conversation.
  useEffect(() => {
    let cancelled = false;
    setMessages([]);
    setArtifacts([]);
    setEnd(null);
    void (async () => {
      try {
        const sessions = await api.appSessions(appID);
        if (cancelled) return;
        const newest = sessions[0]?.id ?? '';
        setConversationID(newest);
        if (!newest) return;
        const history = await api.appHistory(appID, newest);
        if (cancelled) return;
        setMessages(
          history.map(historyRow).filter((m): m is AppMessage => !!m),
        );
        const live = await api.appActiveRun(appID, newest);
        if (!cancelled && live) setRunID(live);
      } catch {
        // An application whose runtime cannot answer yet (disabled
        // between the card and this mount) shows an empty conversation;
        // the send path reports the refusal with its own copy.
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [appID]);

  // One subscription to this application's stream: the deltas the model
  // writes, the files the turn produced, and the terminal event. Only
  // events the host tagged with this application reach it.
  useEffect(() => {
    return subscribeAppEvents(appID, (ev: UIEvent) => {
      const data = ev.data as {
        run_id?: string;
        conversation_id?: string;
        delta?: { part?: StreamPart };
        path?: string;
        status?: string;
        error?: string;
        error_kind?: string;
        interrupt_cause?: string;
      };
      if (ev.type === UIEventType.stream) {
        const part = data.delta?.part;
        if (!part) return;
        // Deltas of another conversation of this application (a run its
        // own scripts started, one this page just left) are not this
        // transcript's. An empty id in the payload is let through: the
        // send that started this run may not have answered yet, and the
        // answer is what names the conversation.
        if (data.conversation_id && data.conversation_id !== convRef.current) {
          return;
        }
        if (part.type === 'text' && part.text) {
          setPending(false);
          appendAssistant(setMessages, data.run_id ?? '', part.text);
        } else if (part.type === 'reasoning' && part.text) {
          appendReasoning(setMessages, part.text);
        }
        return;
      }
      if (ev.type === UIEventType.artifact) {
        // The strip belongs to the turn's conversation; a run that
        // arrived while the page shows another one is not this page's.
        if (data.conversation_id && data.conversation_id !== convRef.current) {
          return;
        }
        if (data.path) {
          setArtifacts((prev) =>
            prev.includes(data.path!) ? prev : [...prev, data.path!],
          );
        }
        return;
      }
      if (ev.type === UIEventType.turnEnd) {
        const finished = data.run_id ?? '';
        // A turn can end before the start call's answer arrives, so
        // neither id is known yet when a fast turn's end shows up: an
        // *unknown* id is not a reason to ignore the event, only a
        // different known one is — that is another run's end (a turn the
        // page's own scripts started, or one of another conversation).
        const otherRun =
          !!finished && !!runRef.current && finished !== runRef.current;
        const otherConversation =
          !!data.conversation_id &&
          !!convRef.current &&
          data.conversation_id !== convRef.current;
        if (otherRun || otherConversation) return;
        setPending(false);
        setRunID('');
        setMessages((prev) =>
          prev.map((m) => (m.streaming ? { ...m, streaming: false } : m)),
        );
        const status = data.status ?? '';
        if (status && status !== 'completed') {
          setEnd({
            status,
            error: data.error,
            errorKind: data.error_kind,
            interruptCause: data.interrupt_cause,
          });
        } else {
          setEnd(null);
        }
      }
    });
  }, [appID]);

  const send = useCallback(async () => {
    const parts: StreamPart[] = [];
    const text = draft.trim();
    if (!text) return;
    parts.push({ type: 'text', text });
    setDraft('');
    setEnd(null);
    setMessages((prev) => [...prev, { id: nextID(), role: 'user', text }]);
    // The strip is the turn's: it starts empty and fills as the files the
    // turn writes arrive, the way the assistant's strip does.
    setArtifacts([]);
    setPending(true);
    try {
      // The wire message is the same TurnMessage the assistant's
      // composer builds; only the channel differs.
      const message: TurnMessage = { role: 'user', content: { parts } };
      const start = await api.appStartTurn({
        id: appID,
        conversation_id: conversationID,
        message: message as unknown as gen.AppTurnRequest['message'],
        model,
        think,
        agent_id: agent,
      });
      setRunID(start.run_id);
      if (!conversationID) setConversationID(start.conversation_id);
    } catch (err) {
      setPending(false);
      setEnd({ status: 'failed', error: String(err) });
    }
  }, [appID, conversationID, draft, model, think, agent]);

  const stop = useCallback(async () => {
    if (!runID) return;
    try {
      await api.appCancel(appID, runID);
    } catch (err) {
      setEnd({ status: 'failed', error: String(err) });
    }
  }, [appID, runID]);

  const running = pending || runID !== '';
  const notice = useMemo(() => turnNotice(end, t), [end, t]);

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="min-h-0 flex-1 overflow-y-auto px-4 py-3">
        {messages.length === 0 && !running && (
          <p className="py-6 text-center text-xs text-faint">
            {t('apps.chat.empty')}
          </p>
        )}
        <div className="mx-auto flex max-w-3xl flex-col gap-3">
          {messages.map((m) => (
            <div key={m.id} data-testid={`app-msg-${m.role}`}>
              {m.role === 'user' ? (
                <div className="ml-auto w-fit max-w-[85%] whitespace-pre-wrap rounded-card bg-panel3 px-3 py-2 text-sm">
                  {m.text}
                </div>
              ) : (
                <div className="flex flex-col gap-1">
                  {m.reasoning && (
                    <details className="rounded-card border border-edge bg-panel2 px-3 py-2 text-xs text-dim">
                      <summary className="cursor-pointer select-none">
                        {t('apps.chat.reasoning')}
                      </summary>
                      <p className="mt-1 whitespace-pre-wrap">{m.reasoning}</p>
                    </details>
                  )}
                  {m.text &&
                    (m.streaming ? (
                      <div className="prose-chat whitespace-pre-wrap text-sm">
                        {m.text}
                      </div>
                    ) : (
                      <div className="prose-chat text-sm">
                        <Markdown text={m.text} onOpen={() => undefined} />
                      </div>
                    ))}
                </div>
              )}
            </div>
          ))}
          {running && !messages.some((m) => m.streaming) && (
            <p className="animate-pulse text-xs text-dim">
              {t('apps.chat.thinking')}
            </p>
          )}
        </div>
      </div>
      {artifacts.length > 0 && (
        <div
          data-testid="app-artifacts"
          className="flex flex-wrap items-center gap-1.5 border-t border-edge px-4 py-1.5"
        >
          <span className="text-xs font-medium text-fg">
            {t('chat.turnArtifacts')}
          </span>
          {artifacts.map((path) => (
            <button
              key={path}
              data-testid="app-artifact"
              onClick={() => void api.appReveal(appID, path)}
              className="inline-flex max-w-[16rem] items-center gap-1 rounded-tight border border-edge bg-panel2 px-1.5 py-0.5 text-xs text-dim hover:text-fg"
              data-tip={t('apps.chat.reveal')}
            >
              <FileText size={ICON.xs} className="shrink-0" />
              <span className="truncate">{path}</span>
            </button>
          ))}
        </div>
      )}
      {notice && (
        <div
          role="status"
          data-testid="app-turn-notice"
          className={`flex items-start gap-2 border-t border-edge px-4 py-2 text-xs ${notice.tone}`}
        >
          <notice.Icon size={ICON.sm} className="mt-0.5 shrink-0" />
          <div className="min-w-0">
            <p className="font-medium">{notice.title}</p>
            {notice.detail && (
              <p className="mt-0.5 break-words text-dim">{notice.detail}</p>
            )}
          </div>
        </div>
      )}
      <div className="border-t border-edge px-4 py-2">
        <div className="mx-auto flex max-w-3xl items-end gap-2">
          {/* A plain text box, not the main composer: an application's
              turn takes text and nothing else in v1 — no attachments, no
              file mentions, no steering queue — and offering the main
              composer's affordances here would promise all three. */}
          <Textarea
            aria-label={t('apps.chat.placeholder')}
            data-testid="app-composer"
            size="sm"
            surface="raised"
            rows={2}
            className="min-h-[2.5rem] flex-1 text-sm"
            placeholder={t('apps.chat.placeholder')}
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && !e.shiftKey) {
                e.preventDefault();
                void send();
              }
            }}
          />
          <div className="flex flex-col items-stretch gap-1">
            {agents.length > 1 && (
              <select
                aria-label={t('apps.chat.agent')}
                // The column's other controls label themselves with a
                // placeholder; a select has none, so the hint says what
                // the list is (the names are the package's own), through
                // the skin's tooltip rather than the native one.
                data-tip={t('apps.chat.agent')}
                data-testid="app-agent"
                className="w-36 rounded-control border border-edge bg-panel2 px-2 py-1 text-xs text-fg outline-none focus:border-accent"
                value={agent}
                onChange={(e) => setAgent(e.target.value)}
              >
                {/* The empty value is the entry agent: a turn that
                    names none runs it, which is what an application
                    with one agent always does. */}
                <option value="">{agents[0]}</option>
                {agents.slice(1).map((name) => (
                  <option key={name} value={name}>
                    {name}
                  </option>
                ))}
              </select>
            )}
            <Input
              size="sm"
              surface="raised"
              aria-label={t('apps.chat.model')}
              className="w-36"
              value={model}
              onChange={(e) => setModel(e.target.value)}
              placeholder={t('apps.chat.model')}
            />
            <Input
              size="sm"
              surface="raised"
              aria-label={t('apps.chat.think')}
              className="w-36"
              value={think}
              onChange={(e) => setThink(e.target.value)}
              placeholder={t('apps.chat.think')}
            />
          </div>
          {running ? (
            <Button size="sm" variant="secondary" onClick={() => void stop()}>
              <Square size={ICON.sm} />
              {t('chat.stop')}
            </Button>
          ) : (
            <Button
              size="sm"
              onClick={() => void send()}
              disabled={!draft.trim()}
            >
              <Send size={ICON.sm} />
              {t('apps.chat.send')}
            </Button>
          )}
        </div>
      </div>
    </div>
  );
}

/** appendAssistant extends the streaming assistant row, opening one. */
function appendAssistant(
  setMessages: React.Dispatch<React.SetStateAction<AppMessage[]>>,
  _runID: string,
  text: string,
) {
  setMessages((prev) => {
    const last = prev[prev.length - 1];
    if (last && last.role === 'assistant' && last.streaming) {
      return [
        ...prev.slice(0, -1),
        { ...last, text: last.text + text, streaming: true },
      ];
    }
    return [
      ...prev,
      { id: nextID(), role: 'assistant', text, streaming: true },
    ];
  });
}

/** appendReasoning extends the streaming row's trace. */
function appendReasoning(
  setMessages: React.Dispatch<React.SetStateAction<AppMessage[]>>,
  text: string,
) {
  setMessages((prev) => {
    const last = prev[prev.length - 1];
    if (last && last.role === 'assistant' && last.streaming) {
      return [
        ...prev.slice(0, -1),
        { ...last, reasoning: (last.reasoning ?? '') + text },
      ];
    }
    return [
      ...prev,
      {
        id: nextID(),
        role: 'assistant',
        text: '',
        reasoning: text,
        streaming: true,
      },
    ];
  });
}

/**
 * turnNotice renders why a turn ended without finishing. The distinction
 * the main chat makes is made here too, and for the same reason: a run
 * that hit its own deadline and a run the user stopped arrive as the same
 * `canceled` status, and only the structured error kind tells them apart
 * — reading the status alone would report a timeout as the user's stop.
 */
function turnNotice(
  end: AppTurnEnd | null,
  t: (key: string, options?: Record<string, unknown>) => string,
): {
  title: string;
  detail: string;
  tone: string;
  Icon: typeof AlertTriangle;
} | null {
  if (!end) return null;
  const deadline = end.status === 'canceled' && end.errorKind === 'timeout';
  const userStop = isUserStop(
    end.status as never,
    end.interruptCause,
    end.errorKind,
  );
  const failure = end.status === 'failed' || end.status === 'aborted';
  const friendly = friendlyFailure(end.errorKind);
  if (deadline) {
    return {
      title: t('chat.lastTimedOut'),
      detail: friendly || end.error || t('chat.turnTimeout'),
      tone: 'text-warn',
      Icon: Clock,
    };
  }
  if (userStop) {
    return {
      title: t('chat.lastCancelled'),
      detail: t('chat.lastCancelledDetail'),
      tone: 'text-dim',
      Icon: Ban,
    };
  }
  if (end.status === 'interrupted') {
    return {
      title: t('chat.lastInterrupted'),
      detail:
        friendlyInterruption(end.interruptCause) ??
        t('chat.lastInterruptedDetail'),
      tone: 'text-warn',
      Icon: AlertTriangle,
    };
  }
  return {
    title: failure ? t('chat.lastFailed') : t('chat.lastAborted'),
    detail: friendly || end.error || t('chat.lastFailedDetail'),
    tone: 'text-err',
    Icon: AlertTriangle,
  };
}
