import { useTranslation } from 'react-i18next';
import { Check } from 'lucide-react';
import { Button, type ButtonSize } from './Button';
import { ICON } from './icon';

// SaveBar — the one place a panel's save action lives: status on the
// left (saved check / error), primary button on the right, always at
// the bottom edge of its container. Dialogs, inline sections and list
// editors share it so "where do I submit this?" has one answer.
export function SaveBar({
  saved = false,
  error = '',
  saving = false,
  onSave,
  saveLabel,
  size = 'md',
  disabled = false,
  bare = false,
  className = '',
  children,
}: {
  saved?: boolean;
  error?: string;
  saving?: boolean;
  onSave: () => void;
  saveLabel?: string;
  size?: ButtonSize;
  disabled?: boolean;
  // bare: the caller owns the frame (a dialog footer that already
  // draws the top border and padding). Default mode draws them here.
  bare?: boolean;
  className?: string;
  // Extra status content (e.g. a hint next to the saved check).
  children?: React.ReactNode;
}) {
  const { t } = useTranslation();
  return (
    <div
      className={`flex w-full shrink-0 items-center justify-between gap-3 ${
        bare ? '' : 'border-t border-edge px-4 py-3'
      } ${className}`}
    >
      <span className="flex min-w-0 items-center gap-2 text-xs">
        {saved && (
          <span className="inline-flex shrink-0 items-center gap-1 text-ok">
            <Check size={ICON.xs} />
            {t('config.toolsSaved')}
          </span>
        )}
        {children}
        {error !== '' && (
          <span className="min-w-0 truncate text-err" data-tip={error}>
            {error}
          </span>
        )}
      </span>
      <Button
        variant="primary"
        size={size}
        loading={saving}
        disabled={disabled}
        onClick={onSave}
      >
        {saveLabel ?? t('setup.saveApply')}
      </Button>
    </div>
  );
}
