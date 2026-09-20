// Icon size ladder.
//
// Every lucide icon in the app sizes itself from here instead of a raw
// rem literal: the values are CSS lengths because the root font size is
// the user's scale knob, so icons zoom with the text. Read at the 14px
// design base they are 12 / 14 / 16 / 20 / 24 / 32 design px.
//
//   xs    inline glyphs next to micro/label text (chips, tree rows)
//   sm    the default control icon (buttons, list rows, toolbar)
//   md    section headings, list-leading icons, input adornments
//   lg    dialog headers, empty-state and hero glyphs inside cards
//   xl    large accents (usage hero, big stat cards)
//   hero  the oversized empty-state / welcome marks
export const ICON = {
  xs: '0.8571rem',
  sm: '1.0000rem',
  md: '1.1429rem',
  lg: '1.4286rem',
  xl: '1.7143rem',
  hero: '2.2857rem',
} as const;
