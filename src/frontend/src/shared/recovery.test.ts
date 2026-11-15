import { describe, expect, it } from 'vitest';
import { backoff, friendlyError, stopsRecovery } from './recovery';
describe('recovery policy', () => {
  it('uses full jitter with exponential growth and a stable 30-second ceiling', () => {
    expect(backoff(0, () => 0)).toBe(0);
    expect(backoff(0, () => 0.5)).toBe(250);
    expect(backoff(1, () => 0.5)).toBe(500);
    expect(backoff(5, () => 0.5)).toBe(8000);
    expect(backoff(5000, () => 0.5)).toBe(15000);
    expect(backoff(8, () => 0.99999)).toBeLessThan(30000);
  });
  it('honors longer retry-after hints without unbounded waits', () => {
    expect(backoff(0, () => 0, 45000)).toBe(45000);
    expect(backoff(0, () => 0, 90000)).toBe(60000);
    expect(backoff(5, () => 0.5, 200)).toBe(8000);
  });
  it('pauses replaced tabs and preserves meaningful errors including wrong PIN', () => {
    expect(stopsRecovery({ code: 'connection_replaced', message: 'replaced', retryable: true })).toBe(true);
    expect(stopsRecovery({ code: 'temporarily_unavailable', message: 'retry', retryable: true })).toBe(false);
    expect(friendlyError({ code: 'wrong_pin', message: 'wrong', retryable: false })).toContain('Check the host');
    expect(friendlyError({ code: 'session_unavailable', message: 'unknown', retryable: false })).toContain('preserved');
    expect(friendlyError({ code: 'custom', message: 'Action required', retryable: false })).toBe('Action required');
  });
});
