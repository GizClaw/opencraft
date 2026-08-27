import { useState } from 'react';
import { Loader2, Search } from 'lucide-react';
import { useTranslation } from 'react-i18next';

export interface GitHubRepo {
  full_name: string;
  description: string | null;
  html_url: string;
  clone_url: string;
  stargazers_count: number;
}

export interface MCPLaunchProbe {
  command: string;
  args: string[];
}

// probeMCPServerLaunch tries to infer an MCP server's run command from
// the repo's metadata: package.json bin → npx -y <pkg>; pyproject.toml
// name → uvx <name>; README line mentioning mcp with npx/uvx/pipx as a
// last resort. Returns empty values when nothing looks like a launcher.
export async function probeMCPServerLaunch(
  fullName: string,
): Promise<MCPLaunchProbe> {
  const raw = `https://raw.githubusercontent.com/${fullName}/HEAD/`;
  try {
    const res = await fetch(raw + 'package.json');
    if (res.ok) {
      const pkg = (await res.json()) as {
        name?: string;
        bin?: string | Record<string, string>;
      };
      if (pkg.bin && typeof pkg.name === 'string') {
        const binName =
          typeof pkg.bin === 'string' ? pkg.name : Object.keys(pkg.bin)[0];
        return {
          command: 'npx',
          args: ['-y', pkg.name, binName !== pkg.name ? binName : ''].filter(
            Boolean,
          ),
        };
      }
    }
  } catch {
    // fall through to pyproject / README
  }
  try {
    const res = await fetch(raw + 'pyproject.toml');
    if (res.ok) {
      const text = await res.text();
      const m = text.match(/^name\s*=\s*["']([^"']+)["']/m);
      if (m) return { command: 'uvx', args: [m[1]] };
    }
  } catch {
    // fall through to README
  }
  for (const readme of ['README.md', 'README.MD', 'readme.md']) {
    try {
      const res = await fetch(raw + readme);
      if (!res.ok) continue;
      const text = await res.text();
      const hit = text
        .split('\n')
        .map((l) => l.replace(/^[#>*`\-\s]+/, '').trim())
        .find(
          (l) =>
            /(^|\s)(npx|uvx|pipx|python\s+-m)\s/.test(l) &&
            /mcp/i.test(l) &&
            l.length < 120,
        );
      if (hit) {
        const tokens = hit.split(/\s+/);
        return { command: tokens[0], args: tokens.slice(1) };
      }
    } catch {
      // try next readme name
    }
  }
  return { command: '', args: [] };
}

// GitHubSearch queries the GitHub repository search API straight from
// the renderer (api.github.com sends Access-Control-Allow-Origin: *),
// optionally narrowed by a topic. Unauthenticated rate limits apply
// (~10 search req/min), fine for a manual discover action.
export function GitHubSearch({
  topic,
  placeholder,
  actionLabel,
  onPick,
  busy,
}: {
  topic: string;
  placeholder: string;
  actionLabel: string;
  onPick: (repo: GitHubRepo) => void;
  busy?: boolean;
}) {
  const { t } = useTranslation();
  const [query, setQuery] = useState('');
  const [results, setResults] = useState<GitHubRepo[]>([]);
  const [searching, setSearching] = useState(false);
  const [error, setError] = useState('');
  const [searched, setSearched] = useState(false);

  const runSearch = async (q: string) => {
    if (!q.trim()) return;
    setSearching(true);
    setError('');
    try {
      const full = topic ? `${q.trim()} topic:${topic}` : q.trim();
      const res = await fetch(
        `https://api.github.com/search/repositories?q=${encodeURIComponent(
          full,
        )}&per_page=10&sort=stars`,
        { headers: { Accept: 'application/vnd.github+json' } },
      );
      if (!res.ok) {
        throw new Error(`${res.status} ${res.statusText}`);
      }
      const data = (await res.json()) as { items?: GitHubRepo[] };
      setResults(data.items ?? []);
      setSearched(true);
    } catch (err) {
      setError(String(err));
      setResults([]);
    } finally {
      setSearching(false);
    }
  };

  return (
    <div className="space-y-1.5">
      <form
        className="flex gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          void runSearch(query);
        }}
      >
        <input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder={placeholder}
          className="flex-1 min-w-0 rounded-lg border border-edge bg-panel px-3 py-1.5 text-sm outline-none focus:border-accent"
        />
        <button
          type="submit"
          disabled={searching || busy}
          className="flex shrink-0 items-center gap-1.5 rounded-lg border border-edge px-3 py-1.5 text-sm text-dim hover:text-fg disabled:opacity-40"
        >
          {searching ? (
            <Loader2 size={13} className="animate-spin" />
          ) : (
            <Search size={13} />
          )}
          {t('config.githubSearch')}
        </button>
      </form>
      {error && <p className="text-xs text-err">{error}</p>}
      {searched && !searching && results.length === 0 && !error && (
        <p className="text-xs text-dim">{t('config.githubNoResults')}</p>
      )}
      {results.map((repo) => (
        <div
          key={repo.full_name}
          className="flex items-center gap-2 rounded-lg px-2 py-1 hover:bg-panel"
        >
          <span className="text-sm min-w-0 truncate">{repo.full_name}</span>
          <span className="flex-1 text-xs text-dim min-w-0 truncate">
            {repo.description ?? ''}
          </span>
          {repo.stargazers_count > 0 && (
            <span className="shrink-0 text-[10px] text-dim">
              ★ {repo.stargazers_count}
            </span>
          )}
          <button
            onClick={() => onPick(repo)}
            disabled={busy}
            className="shrink-0 rounded-md border border-accent/40 px-2 py-0.5 text-xs text-accent hover:bg-accent/10 disabled:opacity-40"
          >
            {actionLabel}
          </button>
        </div>
      ))}
    </div>
  );
}
