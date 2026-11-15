import { isRecord } from './protocol';

const key = 'quiz.player.v1';
export const uuidPattern = /^[a-f0-9]{8}-[a-f0-9]{4}-4[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$/;
export const tokenPattern = /^[A-Za-z0-9_-]{42}[AEIMQUYcgkosw048]$/;
export interface Identity {
  playerId: string; name: string; pin?: string; token?: string; sessionId?: string; expiresAt?: string;
}
export function randomToken(): string {
  const bytes = crypto.getRandomValues(new Uint8Array(32));
  return btoa(String.fromCharCode(...bytes)).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '');
}
export function loadIdentity(storage: Storage): Identity {
  const raw = storage.getItem(key);
  if (raw) {
    const stored: unknown = JSON.parse(raw);
    const legacy = isRecord(stored) && !('player_id' in stored) && 'playerId' in stored;
    const data = legacy ? {
      player_id: stored.playerId, player_name: stored.name, game_pin: stored.pin,
      reconnect_token: stored.token, session_id: stored.sessionId, expires_at: stored.expiresAt,
    } : stored;
    if (!isRecord(data) || typeof data.player_id !== 'string' || !uuidPattern.test(data.player_id) || typeof data.player_name !== 'string'
      || (data.game_pin !== undefined && (typeof data.game_pin !== 'string' || !/^[1-9]\d{5}$/.test(data.game_pin)))
      || (data.reconnect_token !== undefined && (typeof data.reconnect_token !== 'string' || !tokenPattern.test(data.reconnect_token)))
      || (data.session_id !== undefined && (typeof data.session_id !== 'string' || !uuidPattern.test(data.session_id)))
      || (data.expires_at !== undefined && (typeof data.expires_at !== 'string' || !Number.isFinite(Date.parse(data.expires_at))))) {
      throw new Error('Saved player credentials could not be read. They have not been erased. Restore browser storage or explicitly forget this device’s saved identity.');
    }
    const identity = {
      playerId: data.player_id, name: data.player_name, pin: data.game_pin, token: data.reconnect_token,
      sessionId: data.session_id, expiresAt: data.expires_at,
    };
    if (legacy) saveIdentity(storage, identity);
    return identity;
  }
  const identity: Identity = { playerId: crypto.randomUUID(), name: '' };
  saveIdentity(storage, identity);
  return identity;
}
export function saveIdentity(storage: Storage, identity: Identity): void {
  storage.setItem(key, JSON.stringify({
    player_id: identity.playerId, player_name: identity.name, game_pin: identity.pin,
    reconnect_token: identity.token, session_id: identity.sessionId, expires_at: identity.expiresAt,
  }));
}
export function prepareJoin(storage: Storage, identity: Identity, name: string, pin: string): Identity {
  if (identity.pin || identity.token || identity.sessionId) throw new Error('Recover or explicitly leave the saved game before joining another.');
  const normalized = name.trim();
  if ([...normalized].length < 1 || [...normalized].length > 40 || !/^[1-9]\d{5}$/.test(pin)) throw new Error('Enter a name (1–40 characters) and a six-digit PIN.');
  const joined = { playerId: identity.playerId, name: normalized, pin, token: randomToken() };
  saveIdentity(storage, joined);
  return joined;
}
export function clearGame(storage: Storage, identity: Identity): Identity {
  const clean = { playerId: identity.playerId, name: identity.name };
  saveIdentity(storage, clean);
  return clean;
}
