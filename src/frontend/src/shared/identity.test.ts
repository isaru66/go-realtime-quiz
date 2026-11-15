import { describe, expect, it } from 'vitest';
import { clearGame, loadIdentity, prepareJoin, randomToken, saveIdentity, tokenPattern, uuidPattern } from './identity';

class MemoryStorage implements Storage {
  private items = new Map<string, string>();
  get length() { return this.items.size; }
  clear() { this.items.clear(); }
  getItem(key: string) { return this.items.get(key) ?? null; }
  key(index: number) { return [...this.items.keys()][index] ?? null; }
  removeItem(key: string) { this.items.delete(key); }
  setItem(key: string, value: string) { this.items.set(key, value); }
}
describe('persistent player identity', () => {
  it('creates secure canonical credentials and persists them before the first join', () => {
    const storage = new MemoryStorage();
    const identity = loadIdentity(storage);
    expect(identity.playerId).toMatch(uuidPattern);
    const joined = prepareJoin(storage, identity, '  Ada 🌐  ', '123456');
    expect(joined.name).toBe('Ada 🌐');
    expect(joined.token).toMatch(tokenPattern);
    expect(atob(joined.token!.replace(/-/g, '+').replace(/_/g, '/'))).toHaveLength(32);
    expect(loadIdentity(storage)).toEqual(joined);
    expect(randomToken()).not.toBe(joined.token);
  });
  it('preserves session credentials even when their saved expiry is in the past', () => {
    const storage = new MemoryStorage();
    const joined = prepareJoin(storage, loadIdentity(storage), 'Ada', '123456');
    const acknowledged = { ...joined, sessionId: crypto.randomUUID(), expiresAt: '2020-01-01T00:00:00.000Z' };
    saveIdentity(storage, acknowledged);
    expect(loadIdentity(storage)).toEqual(acknowledged);
    expect(() => prepareJoin(storage, acknowledged, 'Other', '654321')).toThrow('Recover');
    expect(loadIdentity(storage)).toEqual(acknowledged);
  });
  it('round-trips the exact snake_case storage representation', () => {
    const storage = new MemoryStorage();
    const identity = {
      playerId: crypto.randomUUID(), name: 'Ada', pin: '123456', token: randomToken(),
      sessionId: crypto.randomUUID(), expiresAt: '2020-01-01T00:00:00.000Z',
    };
    saveIdentity(storage, identity);
    expect(JSON.parse(storage.getItem('quiz.player.v1')!)).toEqual({
      player_id: identity.playerId, player_name: identity.name, game_pin: identity.pin,
      reconnect_token: identity.token, session_id: identity.sessionId, expires_at: identity.expiresAt,
    });
    expect(loadIdentity(storage)).toEqual(identity);
  });
  it('migrates valid legacy camelCase credentials without changing their identity or expiry', () => {
    const storage = new MemoryStorage();
    const legacy = {
      playerId: crypto.randomUUID(), name: 'Ada', pin: '123456', token: randomToken(),
      sessionId: crypto.randomUUID(), expiresAt: '2020-01-01T00:00:00.000Z',
    };
    storage.setItem('quiz.player.v1', JSON.stringify(legacy));
    expect(loadIdentity(storage)).toEqual(legacy);
    const migrated = storage.getItem('quiz.player.v1');
    expect(JSON.parse(migrated!)).toEqual({
      player_id: legacy.playerId, player_name: legacy.name, game_pin: legacy.pin,
      reconnect_token: legacy.token, session_id: legacy.sessionId, expires_at: legacy.expiresAt,
    });
    expect(loadIdentity(storage)).toEqual(legacy);
    expect(storage.getItem('quiz.player.v1')).toBe(migrated);
  });
  it('does not overwrite a corrupt legacy identity during migration', () => {
    const storage = new MemoryStorage();
    const raw = JSON.stringify({ playerId: crypto.randomUUID(), name: 'Ada', pin: '123456', token: 'invalid' });
    storage.setItem('quiz.player.v1', raw);
    expect(() => loadIdentity(storage)).toThrow('have not been erased');
    expect(storage.getItem('quiz.player.v1')).toBe(raw);
  });
  it('clears only game fields while retaining player UUID and name', () => {
    const storage = new MemoryStorage();
    const joined = prepareJoin(storage, loadIdentity(storage), 'Ada', '123456');
    const cleared = clearGame(storage, joined);
    expect(cleared).toEqual({ playerId: joined.playerId, name: 'Ada' });
    expect(loadIdentity(storage)).toEqual(cleared);
  });
  it('refuses to proceed when persistence fails or credentials are corrupt', () => {
    const storage = new MemoryStorage();
    const identity = loadIdentity(storage);
    storage.setItem('quiz.player.v1', '{"playerId":"broken"}');
    expect(() => loadIdentity(storage)).toThrow('have not been erased');
    expect(storage.getItem('quiz.player.v1')).toBe('{"playerId":"broken"}');
    storage.setItem = () => { throw new Error('Storage disabled'); };
    expect(() => prepareJoin(storage, identity, 'Ada', '123456')).toThrow('Storage disabled');
  });
});
