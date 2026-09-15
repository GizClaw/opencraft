import {
  ClipboardList,
  FileText,
  Globe,
  HelpCircle,
  Puzzle,
  Sparkles,
  Terminal,
  Users,
  Wrench,
  type LucideIcon,
} from 'lucide-react';

/**
 * The tool categories the pet feed reports. The vocabulary lives on the
 * Go side (PetToolCategory in internal/adapters/desktop/pet/pet.go);
 * this list is the renderer's copy of it, and toolCategory.test.ts pins
 * the Go constants, this list and the CSS tints in pet.css against each
 * other so a new category cannot land in only one of the three.
 */
export const PET_TOOL_CATEGORIES = [
  'exec',
  'file',
  'web',
  'generate',
  'skill',
  'plan',
  'ask',
  'delegate',
  'other',
] as const;

export type PetToolCategory = (typeof PET_TOOL_CATEGORIES)[number];

/** Glyph per category; `other` is also the mark for unknown values. */
const ICONS: Record<PetToolCategory, LucideIcon> = {
  exec: Terminal,
  file: FileText,
  web: Globe,
  generate: Sparkles,
  skill: Puzzle,
  plan: ClipboardList,
  ask: HelpCircle,
  delegate: Users,
  other: Wrench,
};

/** Narrows a wire category to the vocabulary the renderer styles. */
export function petToolCategory(category?: string): PetToolCategory {
  return (PET_TOOL_CATEGORIES as readonly string[]).includes(category ?? '')
    ? (category as PetToolCategory)
    : 'other';
}

/** The glyph the tool pill shows for a wire category. */
export function petToolIcon(category?: string): LucideIcon {
  return ICONS[petToolCategory(category)];
}
