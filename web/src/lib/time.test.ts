import { describe, expect, it } from 'vitest';
import { formatDate, relativeTime } from './time';

const now = new Date('2026-07-19T12:00:00Z');

describe('relativeTime', () => {
  it('reports very recent timestamps as "just now"', () => {
    expect(relativeTime('2026-07-19T11:59:40Z', now)).toBe('just now');
    expect(relativeTime('2026-07-19T11:59:59Z', now)).toBe('just now');
  });

  it('reports minutes, hours, and days within a week', () => {
    expect(relativeTime('2026-07-19T11:55:00Z', now)).toBe('5m ago');
    expect(relativeTime('2026-07-19T09:00:00Z', now)).toBe('3h ago');
    expect(relativeTime('2026-07-17T12:00:00Z', now)).toBe('2d ago');
    expect(relativeTime('2026-07-12T12:00:00Z', now)).toBe('7d ago');
  });

  it('falls back to a short date beyond a week', () => {
    expect(relativeTime('2026-06-30T12:00:00Z', now)).toBe('Jun 30');
  });

  it('adds the year when it differs from the current year', () => {
    expect(relativeTime('2025-12-30T12:00:00Z', now)).toBe('Dec 30, 2025');
  });

  it('returns already-humanized or invalid input unchanged', () => {
    expect(relativeTime('2m ago', now)).toBe('2m ago');
    expect(relativeTime('6h ago', now)).toBe('6h ago');
    expect(relativeTime('not a date', now)).toBe('not a date');
    expect(relativeTime('', now)).toBe('');
  });
});

describe('formatDate', () => {
  it('formats a valid date as a short, dated label', () => {
    expect(formatDate('2026-07-30T12:00:00Z')).toBe('Jul 30, 2026');
    expect(formatDate('Jul 30, 2026')).toBe('Jul 30, 2026');
  });

  it('returns invalid input unchanged', () => {
    expect(formatDate('unknown')).toBe('unknown');
    expect(formatDate('')).toBe('');
  });
});
