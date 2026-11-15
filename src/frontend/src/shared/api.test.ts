import { afterEach, describe, expect, it, vi } from 'vitest';
import { api, ApiError, isAuth, isCatalog } from './api';
afterEach(() => vi.unstubAllGlobals());
describe('same-origin API contracts', () => {
  it('sends cookies and supplied CSRF/idempotency headers without adding wire fields', async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ csrf_token: 'A'.repeat(43), expires_at: '2026-11-15T10:00:00.000Z' })));
    vi.stubGlobal('fetch', fetch);
    await api('/auth/session', isAuth, { method: 'POST', headers: { 'X-CSRF-Token': 'csrf' }, body: JSON.stringify({ credential: 'test-only' }) });
    expect(fetch).toHaveBeenCalledWith('/api/v1/auth/session', {
      method: 'POST', credentials: 'include', headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': 'csrf' },
      body: '{"credential":"test-only"}',
    });
  });
  it('keeps actionable server error codes for creation recovery and sign-in', async () => {
    vi.stubGlobal('fetch', vi.fn().mockImplementation(async () => new Response(JSON.stringify({
      request_id: crypto.randomUUID(), error: { code: 'configuration_changed', message: 'Refresh catalog', retryable: false },
    }), { status: 409 })));
    await expect(api('/quizzes', isCatalog)).rejects.toMatchObject({ code: 'configuration_changed', retryable: false });
    await expect(api('/quizzes', isCatalog)).rejects.toBeInstanceOf(ApiError);
  });
  it('uses friendly service errors for empty proxy failures and network loss', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('', { status: 502 })));
    await expect(api('/quizzes', isCatalog)).rejects.toThrow('temporarily unavailable (502)');
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Failed to fetch')));
    await expect(api('/quizzes', isCatalog)).rejects.toThrow('Saved recovery information is preserved');
  });
  it('rejects a malformed public catalog rather than rendering untyped data', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ quizzes: [{ id: 'quiz', answer: 'secret' }] }))));
    await expect(api('/quizzes', isCatalog)).rejects.toThrow('incompatible response');
  });
});
