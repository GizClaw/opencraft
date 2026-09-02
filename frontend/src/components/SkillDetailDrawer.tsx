import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Loader2, Sparkles, X } from 'lucide-react';
import { api } from '../lib/api';
import type { SkillDTO } from '../lib/types';
import { Markdown } from './Markdown';

// SkillDetailDrawer is the right-side skill detail page. It shows the
// skill's metadata and renders the full SKILL.md body as markdown, so
// clicking a skill card behaves like clicking a plugin card.
export function SkillDetailDrawer({
  skill,
  onClose,
}: {
  skill: SkillDTO;
  onClose: () => void;
}) {
  const { t } = useTranslation();
  const [body, setBody] = useState('');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

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
      : skill.scope === 'user'
        ? t('config.skillsScopeUser')
        : skill.scope === 'repo'
          ? t('config.skillsScopeRepo')
          : skill.scope;

  return (
    <>
      <div className="fixed inset-0 z-40 bg-black/30" onClick={onClose} />
      <aside
        className="fixed inset-y-0 right-0 z-50 flex w-[46rem] max-w-[94vw] flex-col border-l border-edge bg-panel shadow-2xl"
        role="dialog"
        aria-modal="true"
        aria-label={skill.name}
      >
        <div className="flex shrink-0 items-center justify-between border-b border-edge px-4 py-3">
          <div className="flex min-w-0 items-center gap-2">
            <Sparkles size="1.0714rem" className="shrink-0 text-accent" />
            <h3 className="min-w-0 truncate text-sm font-semibold">
              {skill.name}
            </h3>
            {skill.plugin_id ? (
              <span className="shrink-0 rounded border border-accent/30 bg-accent/10 px-1.5 py-0.5 text-[0.7143rem] text-accent">
                {t('config.skillsPluginFrom', {
                  name: skill.plugin_name || skill.plugin_id,
                })}
              </span>
            ) : (
              <span className="shrink-0 rounded border border-edge bg-panel2 px-1.5 py-0.5 text-[0.7143rem] text-dim">
                {scopeLabel}
              </span>
            )}
          </div>
          <button
            onClick={onClose}
            aria-label={t('tools.close')}
            className="text-dim hover:text-fg"
          >
            <X size="1.1429rem" />
          </button>
        </div>

        <div className="min-h-0 min-w-0 flex-1 space-y-4 overflow-y-auto p-4">
          <div className="space-y-1.5">
            {skill.description && (
              <p className="text-xs text-dim">{skill.description}</p>
            )}
            <p
              className="break-all font-mono text-xs text-dim"
              title={skill.path}
            >
              {skill.path}
            </p>
          </div>

          <section className="min-w-0">
            <h4 className="mb-2 text-xs font-semibold uppercase tracking-wide text-dim">
              {t('config.skillsInstructions')}
            </h4>
            {loading ? (
              <div className="flex items-center gap-2 text-xs text-dim">
                <Loader2 size="0.9286rem" className="animate-spin" />
                {t('config.skillsDetailLoading')}
              </div>
            ) : error ? (
              <p className="rounded-lg border border-err/40 bg-err/10 px-3 py-2 text-xs text-err break-words">
                {t('config.skillsReadError')}: {error}
              </p>
            ) : (
              <div className="prose-chat text-sm">
                <Markdown text={body} />
              </div>
            )}
          </section>
        </div>
      </aside>
    </>
  );
}
