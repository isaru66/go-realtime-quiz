// @vitest-environment jsdom
import { act, cleanup, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useGame, type Binding } from './useGame';
import { loadIdentity, prepareJoin, saveIdentity, type Identity } from './identity';
import { isRecord } from './protocol';

class MockSocket {
  static OPEN = 1;
  static instances: MockSocket[] = [];
  readyState = 0;
  sent: string[] = [];
  onopen: (() => void) | null = null;
  onmessage: ((event: { data: string }) => void) | null = null;
  onclose: ((event: { code: number }) => void) | null = null;
  onerror: (() => void) | null = null;
  constructor(public url: string, public protocol: string) { MockSocket.instances.push(this); }
  open() { this.readyState = 1; this.onopen?.(); }
  send(data: string) { this.sent.push(data); }
  close(code = 1000) {
    if (code !== 1000 && (code < 3000 || code > 4999)) throw new DOMException('Invalid browser WebSocket close code', 'InvalidAccessError');
    this.serverClose(code);
  }
  serverClose(code: number) { this.readyState = 3; this.onclose?.({ code }); }
  receive(value: unknown) { this.onmessage?.({ data: JSON.stringify(value) }); }
}
const time = '2026-11-15T08:30:00.000Z';
const sessionId = 'c0d29bf6-941e-4e1f-bd0e-b18456676ab6';
function message(identity: Identity, event_type = 'game_joined', revision = 2) {
  return {
    event_id: crypto.randomUUID(), event_type, protocol_version: 1, game_pin: identity.pin,
    session_id: sessionId, player_id: identity.playerId, timestamp: time,
    revision, reply_to: crypto.randomUUID(), payload: {
      state: 'LOBBY', role: 'player', quiz_id: 'quiz', quiz_version: 'a'.repeat(64),
      server_time: time, expires_at: '2026-11-15T10:30:00.000Z', control_revision: 0,
      roster: [], leaderboard: [], own_score: 0,
    },
  };
}
function latest() { return MockSocket.instances[MockSocket.instances.length - 1]; }
describe('WebSocket recovery lifecycle', () => {
  beforeEach(() => {
    vi.useFakeTimers(); vi.spyOn(Math, 'random').mockReturnValue(0.5);
    MockSocket.instances = []; vi.stubGlobal('WebSocket', MockSocket);
    const items = new Map<string, string>();
    vi.stubGlobal('localStorage', {
      getItem: (key: string) => items.get(key) ?? null,
      setItem: (key: string, value: string) => { items.set(key, value); },
      removeItem: (key: string) => { items.delete(key); },
      clear: () => items.clear(),
      key: (index: number) => [...items.keys()][index] ?? null,
      get length() { return items.size; },
    } satisfies Storage);
  });
  afterEach(() => { cleanup(); vi.useRealTimers(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });
  it('uses persisted join identity, restores by session, permits rollback only at a recovery bind, and resets backoff', () => {
    const identity = prepareJoin(localStorage, loadIdentity(localStorage), 'Ada', '123456');
    let updated = identity;
    const { result } = renderHook(() => useGame({ role: 'player', identity: updated }, next => { updated = next; }, vi.fn()));
    act(() => latest().open());
    expect(latest().protocol).toBe('quiz.v1');
    expect(JSON.parse(latest().sent[0])).toMatchObject({ event_type: 'join_game', player_id: identity.playerId, payload: { reconnect_token: identity.token, player_name: 'Ada' } });
    expect(loadIdentity(localStorage)).toEqual(identity);
    act(() => latest().receive(message(identity, 'game_joined', 8)));
    expect(loadIdentity(localStorage).sessionId).toBe(sessionId);
    act(() => latest().receive({ ...message(identity, 'state_snapshot', 7), payload: { ...message(identity).payload, own_score: 99 } }));
    expect(result.current.snapshot?.role === 'player' && result.current.snapshot.own_score).toBe(0);
    act(() => latest().serverClose(1006));
    act(() => vi.advanceTimersByTime(250));
    expect(MockSocket.instances).toHaveLength(2);
    act(() => latest().open());
    expect(JSON.parse(latest().sent[0])).toMatchObject({ event_type: 'reconnect', session_id: sessionId, payload: { reconnect_token: identity.token, last_revision: 8 } });
    act(() => latest().receive({ ...message(identity, 'reconnect_success', 3), payload: { ...message(identity).payload, own_score: 1 } }));
    expect(result.current.snapshot?.role === 'player' && result.current.snapshot.own_score).toBe(1);
    act(() => latest().serverClose(1012));
    act(() => vi.advanceTimersByTime(249));
    expect(MockSocket.instances).toHaveLength(2);
    act(() => vi.advanceTimersByTime(1));
    expect(MockSocket.instances).toHaveLength(3);
  });
  it('does not reconnect a superseded tab until deliberate user recovery', () => {
    const identity = prepareJoin(localStorage, loadIdentity(localStorage), 'Ada', '123456');
    const { result } = renderHook(() => useGame({ role: 'player', identity }, vi.fn(), vi.fn()));
    act(() => latest().open());
    act(() => latest().receive(message(identity)));
    act(() => latest().receive({
      ...message(identity, 'error'), payload: { code: 'connection_replaced', message: 'Replaced', retryable: false },
    }));
    act(() => vi.advanceTimersByTime(120000));
    expect(MockSocket.instances).toHaveLength(1);
    expect(result.current.connection).toBe('paused');
    expect(loadIdentity(localStorage).token).toBe(identity.token);
    act(() => result.current.recover());
    expect(MockSocket.instances).toHaveLength(2);
  });
  it('keeps unknown sessions but clears authenticated expiry without deleting the UUID', () => {
    const joined = prepareJoin(localStorage, loadIdentity(localStorage), 'Ada', '123456');
    const identity = { ...joined, sessionId }; saveIdentity(localStorage, identity);
    let binding: Binding | null = { role: 'player', identity };
    const update = vi.fn((next: Identity) => { if (!next.pin) binding = null; });
    const { result, rerender } = renderHook(() => useGame(binding, update, vi.fn()));
    act(() => latest().open());
    act(() => latest().receive({ ...message(identity, 'reconnect_failed'), payload: { code: 'session_unavailable', message: 'Unavailable', retryable: false } }));
    expect(loadIdentity(localStorage)).toEqual(identity);
    expect(result.current.error).toContain('preserved');
    act(() => result.current.recover());
    act(() => latest().open());
    act(() => latest().receive({ ...message(identity, 'reconnect_failed'), payload: { code: 'session_expired', message: 'Expired', retryable: false } }));
    rerender();
    expect(loadIdentity(localStorage)).toEqual({ playerId: identity.playerId, name: identity.name });
    expect(update).toHaveBeenCalledWith({ playerId: identity.playerId, name: identity.name });
  });
  it('sends host commands and snapshot requests without a player ID', () => {
    const identity = prepareJoin(localStorage, loadIdentity(localStorage), 'Ada', '123456');
    const binding: Binding = { role: 'host', auth: { csrf_token: identity.token!, expires_at: time }, room: {
      session_id: sessionId, game_pin: '123456', quiz_id: 'quiz', quiz_version: 'a'.repeat(64), host_capability: identity.token!, expires_at: time, control_revision: 0,
    } };
    const requireSignIn = vi.fn();
    const { result } = renderHook(() => useGame(binding, vi.fn(), vi.fn(), requireSignIn));
    act(() => latest().open());
    expect(JSON.parse(latest().sent[0])).toMatchObject({ event_type: 'host_connect', payload: { csrf_token: identity.token, host_capability: identity.token } });
    expect(JSON.parse(latest().sent[0])).not.toHaveProperty('player_id');
    const { player_id: _playerId, ...envelope } = message(identity, 'host_connected');
    const { own_score: _ownScore, ...payload } = envelope.payload;
    act(() => latest().receive({ ...envelope, payload: { ...payload, role: 'host' } }));
    act(() => result.current.command({ event_type: 'request_snapshot', payload: {} }));
    expect(JSON.parse(latest().sent[1])).toMatchObject({ event_type: 'request_snapshot', payload: {} });
    expect(JSON.parse(latest().sent[1])).not.toHaveProperty('player_id');
    act(() => latest().receive({ ...envelope, event_type: 'error', payload: { code: 'unauthenticated', message: 'Expired login', retryable: false } }));
    expect(requireSignIn).toHaveBeenCalledOnce();
    act(() => vi.advanceTimersByTime(120000));
    expect(MockSocket.instances).toHaveLength(1);
  });
  it.each([false, true])('recovers state before deciding whether an uncertain answer needs replay (already accepted: %s)', alreadyAccepted => {
    let identity = prepareJoin(localStorage, loadIdentity(localStorage), 'Ada', '123456');
    const { result } = renderHook(() => useGame({ role: 'player', identity }, next => { identity = next; }, vi.fn()));
    const open = {
      ...message(identity).payload, state: 'QUESTION_OPEN', question: {
        id: 'q1', type: 'single_choice', text: 'Which?', options: [{ id: 'a', text: 'A' }, { id: 'b', text: 'B' }],
        index: 0, total: 1, points: 100, started_at: time, deadline: '2026-11-15T08:30:20.000Z',
      },
    };
    act(() => latest().open());
    act(() => latest().receive({ ...message(identity), payload: open }));
    act(() => result.current.command({ event_type: 'submit_answer', payload: { question_id: 'q1', option_id: 'a' } }));
    const original: unknown = JSON.parse(latest().sent[1]);
    act(() => latest().serverClose(1006));
    act(() => vi.advanceTimersByTime(250));
    act(() => latest().open());
    expect(latest().sent).toHaveLength(1);
    act(() => latest().receive({ ...message(identity, 'reconnect_success', 3), payload: {
      ...open, ...(alreadyAccepted ? { own_answer: { question_id: 'q1', accepted: true, option_id: 'a', accepted_at: time } } : {}),
    } }));
    expect(latest().sent).toHaveLength(alreadyAccepted ? 1 : 2);
    if (!isRecord(original)) throw new Error('Expected answer command');
    if (!alreadyAccepted) expect(JSON.parse(latest().sent[1])).toMatchObject({
      event_id: original.event_id, event_type: original.event_type, payload: original.payload,
      session_id: original.session_id, player_id: original.player_id,
    });
  });
  it('clears a departed membership when recovery confirms an earlier leave', () => {
    let identity = prepareJoin(localStorage, loadIdentity(localStorage), 'Ada', '123456');
    const { result } = renderHook(() => useGame({ role: 'player', identity }, next => { identity = next; }, vi.fn()));
    act(() => latest().open());
    act(() => latest().receive(message(identity)));
    act(() => result.current.command({ event_type: 'leave_game', payload: {} }));
    act(() => latest().serverClose(1006));
    expect(loadIdentity(localStorage).token).toBe(identity.token);
    act(() => vi.advanceTimersByTime(250));
    act(() => latest().open());
    act(() => latest().receive({ ...message(identity, 'game_left'), payload: {} }));
    expect(loadIdentity(localStorage)).toEqual({ playerId: identity.playerId, name: 'Ada' });
    expect(result.current.connection).toBe('offline');
  });
  it('uses a legal browser close code and automatically recovers after a server drain notice', () => {
    let identity = prepareJoin(localStorage, loadIdentity(localStorage), 'Ada', '123456');
    const { result } = renderHook(() => useGame({ role: 'player', identity }, next => { identity = next; }, vi.fn()));
    act(() => latest().open());
    act(() => latest().receive(message(identity)));
    act(() => latest().receive({ ...message(identity, 'server_draining'), payload: {} }));
    expect(result.current.connection).toBe('recovering');
    expect(result.current.error).toBe('');
    expect(loadIdentity(localStorage).sessionId).toBe(sessionId);
    act(() => vi.advanceTimersByTime(250));
    expect(MockSocket.instances).toHaveLength(2);
    act(() => latest().open());
    expect(JSON.parse(latest().sent[0])).toMatchObject({ event_type: 'reconnect', session_id: sessionId });
    act(() => latest().receive(message(identity, 'reconnect_success', 3)));
    expect(result.current.connection).toBe('connected');
  });
  it('uses a legal browser close code when a bind times out and retries without pausing', () => {
    const identity = prepareJoin(localStorage, loadIdentity(localStorage), 'Ada', '123456');
    const { result } = renderHook(() => useGame({ role: 'player', identity }, vi.fn(), vi.fn()));
    act(() => latest().open());
    act(() => vi.advanceTimersByTime(10000));
    expect(result.current.connection).toBe('recovering');
    expect(result.current.error).toBe('');
    act(() => vi.advanceTimersByTime(250));
    expect(MockSocket.instances).toHaveLength(2);
    act(() => latest().open());
    expect(JSON.parse(latest().sent[0])).toMatchObject({ event_type: 'join_game', payload: { reconnect_token: identity.token } });
  });
});
