import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Loader2, Sparkles, X } from 'lucide-react';
import { api } from '../lib/api';
import { formatDateTime } from '../lib/datetime';
import type { SkillDTO, SkillUsageRow } from '../lib/types';
import { Markdown } from './Markdown';
import { Badge } from './ui/Badge';
import { ICON } from './ui/icon';
import { Overlay } from './ui/Overlay';
import { useFilePreview } from './viewer/FilePreviewModal';

// SkillDetailDrawer is the right-side skill detail page. It shows the
// skill's metadata and renders the full SKILL.md body as markdown, so
// clicking a skill card behaves like clicking a plugin card. References
// in that body open in a preview dialog above the drawer.
export function SkillDetailDrawer({
  skill,
  lifecycle,
  onClose,
}: {
  skill: SkillDTO;
  /** Usage and the pin/retire decisions, when a user database is open. */
  lifecycle?: SkillUsageRow;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const slash = skill.path.lastIndexOf('/');
  const skillDir = slash > 0 ? skill.path.slice(0, slash) : skill.path;
  const [body, setBody] = useState('');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  // References inside a SKILL.md open in a dialog on top of this
  // drawer: the chat's file panel belongs to the session, and a
  // settings page has no business repainting it behind the modal.
  const { openLink, modal } = useFilePreview();

  useEffect(() => {
    let alive = true;
    setBody('');
    setError('');
    setLoading(true);
    api
      .skillContent(skill.path)
      .then((text) => {
        if (alive) setBody(text ?? '');
      })
      .catch((err) => {
        if (alive) setError(String(err));
      })
      .finally(() => {
        if (alive) setLoading(false);
      });
    return () => {
      alive = false;
    };
  }, [skill.path]);

  const scopeLabel =
    skill.scope === 'builtin'
      ? t('config.skillsScopeBuiltin')
      : t('config.skillsScopeUser');

  return (
    <>
      <Overlay
        open
        onClose={onClose}
        variant="drawer-right"
        ariaLabel={skill.name}
        panelClassName="flex w-[46rem] max-w-[94vw] flex-col border-l border-edge bg-panel shadow-modal"
      >
        <div className="flex shrink-0 items-center justify-between border-b border-edge px-4 py-3">
          <div className="flex min-w-0 items-center gap-2">
            <Sparkles size={ICON.md} className="shrink-0 text-accent" />
            <h3 className="min-w-0 truncate text-title font-semibold">
              {skill.name}
            </h3>
            {skill.plugin_id ? (
              <span className="shrink-0 rounded-tight border border-accent/30 bg-accent/10 px-1.5 py-0.5 text-micro text-accent">
                {t('config.skillsPluginFrom', {
                  name: skill.plugin_name || skill.plugin_id,
                })}
              </span>
            ) : (
              <span className="shrink-0 rounded-tight border border-edge bg-panel2 px-1.5 py-0.5 text-micro text-dim">
                {scopeLabel}
              </span>
            )}
          </div>
          <button
            onClick={onClose}
            aria-label={t('tools.close')}
            className="text-dim hover:text-fg"
          >
            <X size={ICON.md} />
          </button>
        </div>

        <div className="min-h-0 min-w-0 flex-1 space-y-4 overflow-y-auto p-4">
          <div className="space-y-1.5">
            {skill.description && (
              <p className="text-xs text-dim">{skill.description}</p>
            )}
            <p
              className="break-all font-mono text-xs text-dim"
              data-tip={skill.path}
            >
              {skill.path}
            </p>
          </div>

          {lifecycle !== undefined && (
            <div className="flex flex-wrap items-center gap-2 text-xs text-dim">
              <span>
                {lifecycle.uses === 0
                  ? t('config.skillsNeverUsed')
                  : t('config.skillsUses', { uses: lifecycle.uses })}
              </span>
              {lifecycle.last_used !== undefined &&
                lifecycle.last_used !== '' && (
                  <span>
                    {t('config.skillsLastUsed', {
                      when: formatDateTime(lifecycle.last_used),
                    })}
                  </span>
                )}
              {lifecycle.pinned && (
                <Badge tone="accent">{t('config.skillsPinned')}</Badge>
              )}
              {lifecycle.retired && (
                <Badge tone="warn">{t('config.skillsRetired')}</Badge>
              )}
              {lifecycle.suggested_retire && (
                <Badge tone="warn">
                  {t('config.skillsSuggestedArchive', {
                    days: lifecycle.idle_days ?? 0,
                  })}
                </Badge>
              )}
            </div>
          )}

          <section className="min-w-0">
            <h4 className="mb-2 text-xs font-semibold uppercase tracking-wide text-dim">
              {t('config.skillsInstructions')}
            </h4>
            {loading ? (
              <div className="flex items-center gap-2 text-xs text-dim">
                <Loader2 size={ICON.sm} className="animate-spin" />
                {t('config.skillsDetailLoading')}
              </div>
            ) : error ? (
              <p className="rounded-control border border-err/40 bg-err/10 px-3 py-2 text-xs text-err break-words">
                {t('config.skillsReadError')}: {error}
              </p>
            ) : (
              <div className="prose-chat text-sm">
                <Markdown text={body} basePath={skillDir} onOpen={openLink} />
              </div>
            )}
          </section>
        </div>
      </Overlay>
      {modal}
    </>
  );
}
