// Compact decimal labels for large quantities - token counts above all, but
// the usage charts and the sidebar rows read the same numbers.
//
// The unit ladder keeps climbing (k -> M -> B -> T -> P -> E) instead of
// stopping at millions: a workspace that has burned 4.74 billion prompt
// tokens used to print "4739.14M", which is a correct number nobody can
// read, sitting next to rows that said "12.3M".
//
// Precision is three significant digits per unit - 1234 -> "1.23k",
// 12345 -> "12.3k", 123456 -> "123k" - because a fourth digit of a thousand
// is noise, and it keeps every cell about the same width.

const UNITS = ['k', 'M', 'B', 'T', 'P', 'E'] as const;
const STEP = 1000;

export interface CompactOptions {
  /** trim drops a trailing ".0"/".00", so a row reads "12k", not "12.0k". */
  trim?: boolean;
}

// decimalsFor keeps a scaled value at three significant digits.
function decimalsFor(value: number): number {
  if (value >= 100) return 0;
  if (value >= 10) return 1;
  return 2;
}

// trimZeros strips the fractional zeros `trim` asks to drop.
function trimZeros(text: string): string {
  return text.replace(/(\.\d*?)0+$/, '$1').replace(/\.$/, '');
}

/**
 * formatCompact renders a quantity in three significant digits plus the
 * largest unit that keeps it below 1000: 4739140000 -> "4.74B".
 */
export function formatCompact(
  value: number,
  options: CompactOptions = {},
): string {
  // NaN/Infinity are diagnostics, not quantities: print them rather than
  // dressing them up as a unit.
  if (!Number.isFinite(value)) return String(value);
  const sign = value < 0 ? '-' : '';
  let scaled = Math.abs(value);
  let unit = -1;
  while (scaled >= STEP && unit < UNITS.length - 1) {
    scaled /= STEP;
    unit += 1;
  }
  if (unit < 0) {
    // Under the first unit the count reads as it stands; fractions (averages,
    // rates) keep at most two decimals.
    const exact = Number.isInteger(scaled)
      ? scaled
      : Math.round(scaled * 100) / 100;
    return sign + String(exact);
  }
  let text = scaled.toFixed(decimalsFor(scaled));
  // Rounding can push the label across a unit boundary ("999999" -> 1000k);
  // climb one more unit instead of printing four digits.
  while (Number(text) >= STEP && unit < UNITS.length - 1) {
    scaled /= STEP;
    unit += 1;
    text = scaled.toFixed(decimalsFor(scaled));
  }
  if (options.trim) text = trimZeros(text);
  return sign + text + UNITS[unit];
}
