import { describe, expect, it } from 'vitest';
import { readFileSync } from 'node:fs';
import { parseServerMessage } from './protocol';
import schema from './protocol-v1.schema.json';
const sessionId = 'c0d29bf6-941e-4e1f-bd0e-b18456676ab6';
const time = '2026-11-15T08:30:00.000Z';
export function snapshotMessage(event_type = 'game_joined', revision = 2) {
  return {
    event_id: crypto.randomUUID(), event_type, protocol_version: 1, game_pin: '123456',
    session_id: sessionId, player_id: 'fc6d1de4-64d1-4997-9eab-e02016e12c4d',
    timestamp: time, revision, reply_to: crypto.randomUUID(),
    payload: {
      state: 'LOBBY', role: 'player', quiz_id: 'quiz', quiz_version: 'a'.repeat(64),
      server_time: time, expires_at: '2026-11-15T10:30:00.000Z',
      control_revision: 0, roster: [], leaderboard: [], own_score: 0,
    },
  };
}
describe('closed v1 protocol validation', () => {
  it('keeps the frontend schema identical to the repository protocol contract', () => {
    const contract: unknown = JSON.parse(readFileSync(new URL('../../../../docs/protocol-v1.schema.json', import.meta.url), 'utf8'));
    expect(schema).toEqual(contract);
  });
  it('accepts complete player snapshots and rejects unknown private fields', () => {
    const message = snapshotMessage();
    expect(parseServerMessage(JSON.stringify(message)).event_type).toBe('game_joined');
    expect(() => parseServerMessage(JSON.stringify({ ...message, payload: { ...message.payload, reconnect_token: 'a'.repeat(43) } }))).toThrow('incompatible');
    expect(() => parseServerMessage(JSON.stringify({ ...message, undocumented: true }))).toThrow('incompatible');
  });
  it('does not accept correctness or host statistics on open player snapshots', () => {
    const message = snapshotMessage();
    const payload = { ...message.payload, state: 'QUESTION_OPEN', question: {
      id: 'q1', type: 'single_choice', text: 'Which?', options: [{ id: 'a', text: 'A' }, { id: 'b', text: 'B' }],
      index: 0, total: 1, points: 100, started_at: time, deadline: '2026-11-15T08:30:20.000Z',
    } };
    expect(() => parseServerMessage(JSON.stringify({ ...message, payload }))).not.toThrow();
    expect(() => parseServerMessage(JSON.stringify({ ...message, payload: { ...payload, own_result: {
      question_id: 'q1', correct_option_id: 'a', answered: true, correct: true, awarded_points: 100,
    } } }))).toThrow();
    expect(() => parseServerMessage(JSON.stringify({ ...message, payload: { ...payload, statistics: { question_id: 'q1', counts: { a: 1, b: 0 }, answer_count: 1 } } }))).toThrow();
  });
  it('rejects invalid UUIDs, timestamps, and unsupported protocol versions', () => {
    const message = snapshotMessage();
    expect(() => parseServerMessage(JSON.stringify({ ...message, event_id: 'not-a-uuid' }))).toThrow();
    expect(() => parseServerMessage(JSON.stringify({ ...message, timestamp: '2026-11-15T08:30:00Z' }))).toThrow();
    expect(() => parseServerMessage(JSON.stringify({ ...message, protocol_version: 2 }))).toThrow();
  });
});
