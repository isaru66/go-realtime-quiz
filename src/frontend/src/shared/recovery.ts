import type { WireError } from './protocol';
export function backoff(attempt: number, random = Math.random, retryAfter = 0): number {
  return Math.max(Math.floor(random() * Math.min(30000, 500 * 2 ** Math.min(Math.max(0, attempt), 16))), Math.min(60000, Math.max(0, retryAfter)));
}
const messages: Record<string, string> = {
  wrong_pin: 'No game was found with that PIN. Check the host’s screen. Your saved identity is safe.',
  session_unavailable: 'This saved session is unavailable. It may be temporarily offline; your recovery credentials are preserved.',
  session_mismatch: 'That PIN now belongs to a different game. Your saved session has not been overwritten.',
  session_expired: 'The server confirmed that this session has expired. Your player identity is ready for another game.',
  connection_replaced: 'This game is active in another tab. Automatic recovery is paused to avoid interrupting it.',
  invalid_credentials: 'These recovery credentials were not accepted. They have not been deleted.',
  unauthenticated: 'Your host sign-in has expired. Sign in again to recover this room.',
  stale_control_revision: 'The game changed before your action. Refreshing its current state; review it before trying again.',
  already_answered: 'Your answer was already recorded. Refreshing your accepted answer.',
  deadline_passed: 'Time is up. Waiting for the official result.',
  game_already_started: 'This game has already started. Only previously joined players can reconnect.',
  membership_left: 'This membership was previously left. Use the explicit forget action to join a different game.',
};
export function friendlyError(error: WireError): string {
  return messages[error.code] ?? error.message;
}
export function stopsRecovery(error: WireError): boolean {
  return error.code === 'connection_replaced' || !error.retryable;
}
