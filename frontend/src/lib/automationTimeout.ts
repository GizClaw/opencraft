// Helpers for the per-task automation run bound. The wire form is a Go
// duration ("15m", "2h") because the scheduler stores it that way; the
// form edits whole minutes, and an empty field means "use the default".

export const DEFAULT_AUTOMATION_TIMEOUT_MINUTES = 15;
export const MAX_AUTOMATION_TIMEOUT_MINUTES = 24 * 60;

// timeoutMinutesFromDuration maps a stored duration onto the whole
// minutes the form edits, and returns '' for a duration with no such
// form — unset, or a Go duration the field cannot state ("1.5h",
// "40ms"; the backend stores whatever the model wrote). '' is not a
// statement that the task has no bound: the form shows the stored text
// verbatim in that case, and only writes minutes back once the user has
// actually edited the field (see timeoutFromForm).
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

// TimeoutFormFields is the run-limit half of an automation form.
export interface TimeoutFormFields {
  // The bound exactly as the task stores it (a Go duration). It is what
  // a save writes back while the field is untouched, so renaming a task
  // cannot cut its run short: the minutes field has no way to state
  // "1.5h" or "40ms", and writing its empty default back would turn a
  // 90-minute bound into the 15-minute one.
  timeoutDuration: string;
  // What the minutes field holds; '' is its empty state.
  timeoutMinutes: string;
  // Set on the first edit of the field. Until then the stored duration is
  // authoritative.
  timeoutEdited: boolean;
}

// timeoutFromForm is the one rule for the bound a save sends: the minutes
// the user typed once they have edited the field, the stored duration
// verbatim before that. Returns undefined for minutes the backend would
// reject, so the form refuses to save instead of quietly substituting
// them.
export function timeoutFromForm(f: TimeoutFormFields): string | undefined {
  if (!f.timeoutEdited) return f.timeoutDuration.trim();
  return timeoutDurationFromMinutes(f.timeoutMinutes);
}
