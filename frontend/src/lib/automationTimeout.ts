// Helpers for the per-task automation run bound. The wire form is a Go
// duration ("15m", "2h") because the scheduler stores it that way; the
// form edits whole minutes, and an empty field means "use the default".

export const DEFAULT_AUTOMATION_TIMEOUT_MINUTES = 15;
export const MAX_AUTOMATION_TIMEOUT_MINUTES = 24 * 60;

// timeoutMinutesFromDuration maps a stored duration onto the whole
// minutes the form edits. Anything unparsable reads as the empty
// default; the backend rejects such values on save, so this only
// covers rows written outside the form.
export function timeoutMinutesFromDuration(value: string | undefined): string {
  const raw = (value ?? '').trim();
  if (raw === '') return '';
  const match = /^(?:(\d+)h)?(?:(\d+)m)?(?:(\d+)s)?$/.exec(raw);
  if (!match || (!match[1] && !match[2] && !match[3])) return '';
  const seconds =
    Number(match[1] ?? 0) * 3600 +
    Number(match[2] ?? 0) * 60 +
    Number(match[3] ?? 0);
  if (seconds <= 0) return '';
  return String(Math.ceil(seconds / 60));
}

// timeoutDurationFromMinutes validates the field: empty means the
// default, otherwise a positive whole number of minutes, capped at a
// day. Returns undefined for a value the backend would reject so the
// form can refuse to save instead of silently dropping it.
export function timeoutDurationFromMinutes(
  minutes: string,
): string | undefined {
  const raw = minutes.trim();
  if (raw === '') return '';
  if (!/^\d+$/.test(raw)) return undefined;
  const value = Number(raw);
  if (value < 1 || value > MAX_AUTOMATION_TIMEOUT_MINUTES) return undefined;
  return `${value}m`;
}
