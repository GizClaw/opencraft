import { BookOpen, Loader2, Wrench } from 'lucide-react';
import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { api } from '../../lib/api';
import type { SkillDTO } from '../../lib/types';
import type { PluginToolDTO } from '../types';

// PluginCapabilitiesSection renders the agent-facing tools and skills a
// plugin contributes. It is embedded in the plugin detail drawer and
// loads both lists in parallel when mounted.
export function PluginCapabilitiesSection({
  pluginId,
  hasTools,
  hasSkills,
}: {
  pluginId: string;
  hasTools: boolean;
  hasSkills: boolean;
}) {
  const { t } = useTranslation();
  const [tools, setTools] = useState<PluginToolDTO[] | null>(null);
  const [skills, setSkills] = useState<SkillDTO[] | null>(null);
  const [error, setError] = useState('');

  useEffect(() => {
    let cancelled = false;
    setError('');
    void Promise.all([
      hasTools ? api.pluginTools(pluginId) : Promise.resolve([]),
      hasSkills ? api.pluginSkills(pluginId) : Promise.resolve([]),
    ])
      .then(([toolRes, skillRes]) => {
        if (cancelled) return;
        setTools(toolRes);
        setSkills(skillRes);
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(String(err));
      });
    return () => {
      cancelled = true;
    };
  }, [hasSkills, hasTools, pluginId]);

  const loaded = tools !== null && skills !== null;

  return (
    <div className="space-y-3">
      {error && <p className="text-xs text-err">{error}</p>}
      {!loaded && !error && (
        <div className="flex items-center gap-2 text-xs text-dim">
          <Loader2 size="0.8571rem" className="animate-spin" />
          {t('config.pluginsCapabilitiesLoading')}
        </div>
      )}
      {loaded && hasTools && (
        <section>
          <h4 className="mb-1.5 flex items-center gap-1.5 text-[0.7rem] font-semibold uppercase tracking-wide text-dim">
            <Wrench size="0.75rem" />
            {t('config.pluginsCapabilitiesTools')}
          </h4>
          {tools!.length === 0 ? (
            <p className="text-xs text-dim">{t('config.pluginsToolsEmpty')}</p>
          ) : (
            <div className="space-y-1.5">
              {tools!.map((tool) => (
                <div
                  key={tool.name}
                  className="rounded-md border border-edge bg-panel px-2.5 py-2"
                >
                  <div className="flex items-center gap-2">
                    <span className="min-w-0 truncate font-mono text-xs font-medium text-fg">
                      {tool.name}
                    </span>
                    {tool.mutates_state && (
                      <span className="shrink-0 rounded bg-warn/10 px-1.5 py-0.5 text-[0.65rem] text-warn">
                        {t('config.pluginsToolMutates')}
                      </span>
                    )}
                  </div>
                  {tool.description && (
                    <p className="mt-1 text-xs leading-relaxed text-dim">
                      {tool.description}
                    </p>
                  )}
                  <p className="mt-1 font-mono text-[0.7rem] text-dim/70">
                    {tool.method}
                  </p>
                </div>
              ))}
            </div>
          )}
        </section>
      )}
      {loaded && hasSkills && (
        <section>
          <h4 className="mb-1.5 flex items-center gap-1.5 text-[0.7rem] font-semibold uppercase tracking-wide text-dim">
            <BookOpen size="0.75rem" />
            {t('config.pluginsCapabilitiesSkills')}
          </h4>
          {skills!.length === 0 ? (
            <p className="text-xs text-dim">{t('config.pluginsSkillsEmpty')}</p>
          ) : (
            <div className="space-y-1.5">
              {skills!.map((skill) => (
                <div
                  key={skill.path}
                  className="rounded-md border border-edge bg-panel px-2.5 py-2"
                >
                  <div className="flex items-center gap-2">
                    <span className="min-w-0 truncate font-mono text-xs font-medium text-fg">
                      {skill.name}
                    </span>
                    {skill.plugin_id ? (
                      <span className="shrink-0 rounded border border-accent/30 bg-accent/10 px-1.5 py-0.5 text-[0.65rem] text-accent">
                        {t('config.skillsPluginFrom', {
                          name: skill.plugin_name || skill.plugin_id,
                        })}
                      </span>
                    ) : skill.scope ? (
                      <span className="shrink-0 rounded bg-panel px-1.5 py-0.5 text-[0.65rem] text-dim">
                        {skill.scope}
                      </span>
                    ) : null}
                  </div>
                  {skill.description && (
                    <p className="mt-1 text-xs leading-relaxed text-dim">
                      {skill.description}
                    </p>
                  )}
                  <p className="mt-1 break-all font-mono text-[0.7rem] text-dim/60">
                    {skill.path}
                  </p>
                </div>
              ))}
            </div>
          )}
        </section>
      )}
    </div>
  );
}
