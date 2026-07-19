// Humanizes ISO timestamps for display. Inputs that are not valid dates
// (including strings that are already humanized, such as "2m ago") are
// returned unchanged so the same helper is safe on demo and API data.

function parse(iso: string): Date | null {
  if (typeof iso !== 'string' || !iso.trim()) return null;
  const date = new Date(iso);
  return Number.isNaN(date.getTime()) ? null : date;
}

function shortDate(date: Date, now: Date): string {
  const sameYear = date.getFullYear() === now.getFullYear();
  return date.toLocaleDateString('en-US', {
    month: 'short',
    day: 'numeric',
    ...(sameYear ? {} : { year: 'numeric' })
  });
}

export function relativeTime(iso: string, now: Date = new Date()): string {
  const date = parse(iso);
  if (!date) return iso;

  const seconds = Math.round((now.getTime() - date.getTime()) / 1000);
  if (seconds < 45) return 'just now';

  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes}m ago`;

  const hours = Math.round(minutes / 60);
  if (hours < 24) return `${hours}h ago`;

  const days = Math.round(hours / 24);
  if (days <= 7) return `${days}d ago`;

  return shortDate(date, now);
}

export function formatDate(iso: string): string {
  const date = parse(iso);
  if (!date) return iso;
  return date.toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: 'numeric' });
}
