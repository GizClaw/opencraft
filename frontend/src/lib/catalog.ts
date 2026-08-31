// Curated catalog of well-known MCP servers and skills (option A).
// Discovery stays a UI concern: entries prefill the existing MCP rows
// or install through the existing installSkill pipeline. Commands are
// the officially documented run lines (uvx for the Python reference
// servers, npx for the TypeScript ones).

export interface MCPCatalogEntry {
  name: string;
  description: string;
  transport: 'stdio' | 'http';
  command: string;
  args: string[];
  url?: string;
  homepage: string;
}

export interface SkillCatalogEntry {
  name: string;
  description: string;
  repo: string;
  subpath: string;
  scope: 'user' | 'repo';
}

const mcpHome = (name: string) =>
  `https://github.com/modelcontextprotocol/servers/tree/main/src/${name}`;

export const MCP_CATALOG: MCPCatalogEntry[] = [
  {
    name: 'git',
    description: 'Git operations and history inspection',
    transport: 'stdio',
    command: 'uvx',
    args: ['mcp-server-git'],
    homepage: mcpHome('git'),
  },
  {
    name: 'time',
    description: 'Time and timezone conversion tools',
    transport: 'stdio',
    command: 'uvx',
    args: ['mcp-server-time'],
    homepage: mcpHome('time'),
  },
  {
    name: 'fetch',
    description: 'Web content fetching with HTML-to-markdown conversion',
    transport: 'stdio',
    command: 'uvx',
    args: ['mcp-server-fetch'],
    homepage: mcpHome('fetch'),
  },
  {
    name: 'filesystem',
    description: 'File system operations',
    transport: 'stdio',
    command: 'npx',
    args: ['-y', '@modelcontextprotocol/server-filesystem'],
    homepage: mcpHome('filesystem'),
  },
  {
    name: 'memory',
    description: 'Knowledge-graph based persistent memory',
    transport: 'stdio',
    command: 'npx',
    args: ['-y', '@modelcontextprotocol/server-memory'],
    homepage: mcpHome('memory'),
  },
  {
    name: 'everything',
    description: 'Reference server exercising the full MCP feature set',
    transport: 'stdio',
    command: 'npx',
    args: ['-y', '@modelcontextprotocol/server-everything'],
    homepage: mcpHome('everything'),
  },
  {
    name: 'sequentialthinking',
    description: 'Structured, step-by-step reasoning',
    transport: 'stdio',
    command: 'npx',
    args: ['-y', '@modelcontextprotocol/server-sequential-thinking'],
    homepage: mcpHome('sequentialthinking'),
  },
];

const codexSkill = (name: string, description: string): SkillCatalogEntry => ({
  name,
  description,
  repo: 'https://github.com/openai/codex.git',
  subpath: `.codex/skills/${name}`,
  scope: 'user',
});

const flowcraftSkill = (
  name: string,
  description: string,
): SkillCatalogEntry => ({
  name,
  description,
  repo: 'https://github.com/GizClaw/flowcraft.git',
  subpath: `skills/${name}`,
  scope: 'user',
});

export const SKILL_CATALOG: SkillCatalogEntry[] = [
  // Official FlowCraft skills — most relevant for opencraft, since the
  // app is built on flowcraft's config model.
  flowcraftSkill(
    'flowcraft-config',
    'Author, validate, and troubleshoot complete FlowCraft deployment configuration',
  ),
  codexSkill(
    'babysit-pr',
    'Watch a GitHub PR until merged/closed: review comments, CI runs, flaky retries',
  ),
  codexSkill('code-review', 'Run a final code review on a pull request'),
  codexSkill(
    'code-review-breaking-changes',
    'Breaking changes review guidance',
  ),
  codexSkill('code-review-change-size', 'Change size guidance (800 lines)'),
  codexSkill('code-review-context', 'Model-visible context review guidance'),
  codexSkill('code-review-testing', 'Test authoring guidance'),
  codexSkill(
    'codex-pr-body',
    'Update the title and body of one or more pull requests',
  ),
  codexSkill(
    'path-types',
    'Choose Rust types for OS paths (Codex repo conventions)',
  ),
  codexSkill(
    'remote-tests',
    'Testing against remote executors in integration tests',
  ),
  codexSkill('test-tui', 'Guide for testing the Codex TUI interactively'),
  codexSkill(
    'update-v8-version',
    "Update Codex's pinned v8 / rusty_v8 versions and validate release candidates",
  ),
];
