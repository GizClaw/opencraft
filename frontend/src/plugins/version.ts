// Version ordering for plugin versions. Mirrors the host's semver-
// shaped comparison (dotted numeric core, optional -prerelease, where
// releases sort above prereleases and numeric prerelease identifiers
// sort below alphanumeric ones). Used only for display warnings; the
// host remains the authority for update/install enforcement.

interface ParsedVersion {
  core: number[];
  pre: string[];
}

function parseVersion(v: string): ParsedVersion | null {
  const trimmed = String(v ?? '').trim();
  let s = trimmed.startsWith('v') ? trimmed.slice(1) : trimmed;
  if (!s) return null;
  const plus = s.indexOf('+');
  if (plus >= 0) s = s.slice(0, plus);
  let pre: string[] = [];
  const dash = s.indexOf('-');
  if (dash >= 0) {
    pre = s
      .slice(dash + 1)
      .split('.')
      .filter(Boolean);
    s = s.slice(0, dash);
  }
  if (!s) return null;
  const core: number[] = [];
  for (const p of s.split('.')) {
    if (!/^\d+$/.test(p) || (p.length > 1 && p.startsWith('0'))) return null;
    core.push(Number(p));
  }
  return { core, pre };
}

/** Returns -1, 0 or 1 when both versions parse; 0 on parse failure. */
export function compareVersions(a: string, b: string): number {
  const pa = parseVersion(a);
  const pb = parseVersion(b);
  if (!pa || !pb) return 0;
  const len = Math.max(pa.core.length, pb.core.length);
  for (let i = 0; i < len; i++) {
    const x = pa.core[i] ?? 0;
    const y = pb.core[i] ?? 0;
    if (x < y) return -1;
    if (x > y) return 1;
  }
  if (pa.pre.length === 0 && pb.pre.length === 0) return 0;
  if (pa.pre.length === 0) return 1;
  if (pb.pre.length === 0) return -1;
  const max = Math.max(pa.pre.length, pb.pre.length);
  for (let i = 0; i < max; i++) {
    if (i >= pa.pre.length) return -1;
    if (i >= pb.pre.length) return 1;
    const x = pa.pre[i];
    const y = pb.pre[i];
    const xn = /^\d+$/.test(x) ? Number(x) : NaN;
    const yn = /^\d+$/.test(y) ? Number(y) : NaN;
    if (!Number.isNaN(xn) && !Number.isNaN(yn)) {
      if (xn < yn) return -1;
      if (xn > yn) return 1;
    } else if (!Number.isNaN(xn)) {
      return -1; // numeric identifiers sort below alphanumeric
    } else if (!Number.isNaN(yn)) {
      return 1;
    } else if (x < y) {
      return -1;
    } else if (x > y) {
      return 1;
    }
  }
  return 0;
}
