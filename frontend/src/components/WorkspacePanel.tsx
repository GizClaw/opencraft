// WorkspacePanel is the right rail of the chat page. It hosts the
// Files | Git segmented modes: the Git segment only appears when the
// active workspace is inside a git repository, otherwise the panel
// stays a plain file browser.
import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from 'react';
import { Files, GitBranch } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { api } from '../lib/api';
import { useStore } from '../lib/store';
import { useConversationState } from '../state/react';
import { EventsOn } from '../../wailsjs/runtime/runtime';
import { FileViewer } from './FileViewer';
import { GitPanel } from './GitPanel';

export function WorkspacePanel({ sessionID }: { sessionID: string }) {
  const { t } = useTranslation();
  const workspace = useStore((s) => s.workspace);
  const mode = useStore((s) => s.viewers[sessionID]?.panelMode ?? 'files');
  const setPanelMode = useStore((s) => s.setPanelMode);
  const [inRepo, setInRepo] = useState<boolean | null>(null);
  const conversationState = useConversationState(sessionID);
  const turnState = conversationState?.turn;
  const busy = turnState?.name === 'starting' || turnState?.name === 'running';
  const prevBusy = useRef(busy);

  const probeRepo = useCallback(async () => {
    try {
      const repo = await api.gitRepo();
      setInRepo(repo.in_repo);
    } catch {
      setInRepo(false);
    }
  }, []);

  // Repo membership is a workspace property; re-probe on workspace
  // changes and whenever this session's rail opens.
  useEffect(() => {
    void probeRepo();
  }, [probeRepo, workspace, sessionID]);

  // Agent turns can initialize a repository or change it on disk;
  // re-probe when a turn finishes and after UI git writes.
  useEffect(() => {
    if (prevBusy.current && !busy) void probeRepo();
    prevBusy.current = busy;
  }, [busy, probeRepo]);

  useEffect(() => {
    const off = EventsOn('opencraft:ui', (ev: unknown) => {
      const msg = ev as { type?: string };
      if (msg?.type === 'git_changed' || msg?.type === 'turn_end') {
        void probeRepo();
      }
    });
    return () => off();
  }, [probeRepo]);

  // Repo membership can also change from outside the app (for example
  // `git init` in an editor terminal); re-probe on a light interval.
  useEffect(() => {
    const timer = window.setInterval(() => void probeRepo(), 5000);
    return () => window.clearInterval(timer);
  }, [probeRepo]);

  const showGit = inRepo === true;
  const modeSafe: 'files' | 'git' = showGit ? mode : 'files';

  return (
    <div
      className="relative flex h-full shrink-0 flex-col border-l border-edge bg-panel"
      style={{ width: 'min(49.6vw, 896px)', minWidth: 448 }}
    >
      {showGit && (
        <div className="flex h-9 shrink-0 items-center gap-1 border-b border-edge px-2">
          <Segment
            active={modeSafe === 'files'}
            icon={<Files size="0.8571rem" />}
            label={t('git.files')}
            onClick={() => setPanelMode('files')}
          />
          <Segment
            active={modeSafe === 'git'}
            icon={<GitBranch size="0.8571rem" />}
            label={t('git.git')}
            onClick={() => setPanelMode('git')}
          />
        </div>
      )}
      <div className="flex min-h-0 min-w-0 flex-1 flex-col">
        {modeSafe === 'git' ? (
          <GitPanel sessionID={sessionID} />
        ) : (
          <FileViewer sessionID={sessionID} embedded />
        )}
      </div>
    </div>
  );
}

function Segment({
  active,
  icon,
  label,
  onClick,
}: {
  active: boolean;
  icon: ReactNode;
  label: string;
  onClick: () => void;
}) {
  return (
    <button
      onClick={onClick}
      className={`flex items-center gap-1.5 rounded-lg px-2.5 py-1 text-xs transition-colors ${
        active
          ? 'bg-accent/15 text-accent'
          : 'text-dim hover:bg-panel2 hover:text-fg'
      }`}
    >
      {icon}
      {label}
    </button>
  );
}
