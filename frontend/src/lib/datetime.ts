// Cached Intl formatters.
//
// Building an Intl.DateTimeFormat is expensive: it resolves locale data
// through ICU and allocates a formatter graph. The transcript renders a
// timestamp for rows on every stream flush, and a long turn flushes at
// animation-frame rate, so `toLocaleString` per row per frame showed up
// as ICU initialization in the main-thread profile of a running turn.
// Formatting through one cached formatter per locale+options keeps that
// work off the render path.

const formatters = new Map<string, Intl.DateTimeFormat>();

function formatter(
  locale: string | undefined,
  options: Intl.DateTimeFormatOptions,
): Intl.DateTimeFormat {
  const key = `${locale ?? ''}|${options.month ?? ''}|${options.day ?? ''}|${
    options.year ?? ''
  }|${options.hour ?? ''}|${options.minute ?? ''}|${
    options.hour12 === undefined ? '' : String(options.hour12)
  }`;
  const cached = formatters.get(key);
  if (cached) return cached;
  const made = new Intl.DateTimeFormat(locale, options);
  formatters.set(key, made);
  return made;
}

// formatClockTime renders the time of day, with the date included once
// the timestamp is not from today — the same shape the chat transcript
// uses for message rows.
export function formatClockTime(
  iso: string | undefined,
  now: Date = new Date(),
): string {
  if (!iso) return '';
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return '';
  const sameDay = date.toDateString() === now.toDateString();
  return formatter(undefined, {
    month: sameDay ? undefined : 'numeric',
    day: sameDay ? undefined : 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  }).format(date);
}

// formatDate renders a date without the time of day.
export function formatDate(iso: string, locale?: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return '';
  return formatter(locale, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).format(date);
}

// formatDateTime renders a full date and time, used by the session hover
// card and other "when did this happen" surfaces.
export function formatDateTime(iso: string, locale?: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return '';
  return formatter(locale, {
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
  }).format(date);
}
