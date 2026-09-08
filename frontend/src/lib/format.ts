// Small shared formatters used across the Git panel surfaces.

export function dateLabel(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso;
  return d.toLocaleString();
}

export function authorInitials(login: string): string {
  return login.slice(0, 2).toUpperCase();
}
