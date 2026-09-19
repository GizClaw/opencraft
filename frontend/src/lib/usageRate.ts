// Cache-hit rate for a usage row (or an aggregate of rows).
//
// The rate is cache-read tokens over prompt tokens. The denominator is the
// part that used to be wrong: the usage tables store `input_tokens`, and
// providers disagree about what that means. OpenAI's prompt_tokens already
// includes the cached tokens, while Anthropic's input_tokens counts only the
// tokens that were neither read from nor written to the cache. The backend
// normalizes to the inclusive number before storing (see
// sessions.PromptTokens in the Go side), so a ratio is well-defined for every
// row written since.
//
// Rows written before that normalization (or by a provider whose counters
// overrun the total) cannot state a ratio at all: `input_tokens` holds the
// uncached share, so cache_read / input_tokens can come out far above 100%.
// Those rows report `undefined` here and the UI shows a dash — the previous
// code clamped them to 100%, which turned "we cannot say" into a confident
// lie and hid exactly the bug this file documents.
export interface CacheCounters {
  /** Prompt tokens: the inclusive input total whenever the backend can say so. */
  input: number;
  /** Tokens served from the provider cache. */
  cacheRead: number;
}

/**
 * cacheHitPercent returns the cache-hit percentage in [0, 100], or
 * `undefined` when the counters cannot state one:
 *
 *   - no prompt tokens recorded (nothing measured yet),
 *   - cache reads larger than the prompt total (a row whose input column
 *     predates the inclusive normalization).
 *
 * A prompt with no cache reads is a real measurement and reports 0.
 */
export function cacheHitPercent(c: CacheCounters): number | undefined {
  if (c.input <= 0) return undefined;
  if (c.cacheRead <= 0) return 0;
  if (c.cacheRead > c.input) return undefined;
  return (c.cacheRead / c.input) * 100;
}

/** Formats a hit rate for display: one decimal, no trailing ".0". */
export function formatHitPercent(
  percent: number | undefined,
): string | undefined {
  if (percent === undefined) return undefined;
  return percent.toFixed(percent >= 99.95 ? 0 : 1);
}
