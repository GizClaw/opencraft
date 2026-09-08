import { authorInitials } from '../../lib/format';

// AvatarBadge is a tiny letter avatar for GitHub users. Initials avoid
// loading remote avatar images inside the desktop webview.
export function AvatarBadge({
  login,
  size = 'sm',
}: {
  login: string;
  size?: 'sm' | 'md';
}) {
  const cls =
    size === 'sm' ? 'h-3.5 w-3.5 text-[0.5rem]' : 'h-5 w-5 text-[0.6429rem]';
  return (
    <span
      className={`grid shrink-0 select-none place-items-center rounded-full bg-accent/15 font-semibold text-accent ${cls}`}
      title={login}
      aria-hidden="true"
    >
      {authorInitials(login)}
    </span>
  );
}
