import { isRecord } from './protocol';
import { uuidPattern, tokenPattern } from './identity';

export interface Quiz { id: string; title: string; description: string; question_count: number }
export interface Catalog { catalog_version: string; quizzes: Quiz[] }
export interface AdminConfig { pod_id: string; active_version: string; validation_status: 'valid' | 'reload_rejected'; last_attempt_at: string; errors: string[]; quizzes: Quiz[] }
export interface Auth { csrf_token: string; expires_at: string }
export interface HostRoom { session_id: string; game_pin: string; quiz_id: string; quiz_version: string; host_capability: string; expires_at: string; control_revision: number }
export interface PendingCreate { key: string; quiz_id: string; quiz_version: string }
export class ApiError extends Error {
  constructor(public code: string, message: string, public retryable: boolean) { super(message); }
}
function isQuiz(value: unknown): value is Quiz {
  return isRecord(value) && typeof value.id === 'string' && typeof value.title === 'string'
    && typeof value.description === 'string' && typeof value.question_count === 'number';
}
export function isCatalog(value: unknown): value is Catalog {
  return isRecord(value) && typeof value.catalog_version === 'string' && Array.isArray(value.quizzes) && value.quizzes.every(isQuiz);
}
export function isConfig(value: unknown): value is AdminConfig {
  return isRecord(value) && typeof value.pod_id === 'string' && typeof value.active_version === 'string'
    && (value.validation_status === 'valid' || value.validation_status === 'reload_rejected')
    && typeof value.last_attempt_at === 'string' && Array.isArray(value.errors) && value.errors.every((item: unknown) => typeof item === 'string')
    && Array.isArray(value.quizzes) && value.quizzes.every(isQuiz);
}
export function isAuth(value: unknown): value is Auth {
  return isRecord(value) && typeof value.csrf_token === 'string' && tokenPattern.test(value.csrf_token) && typeof value.expires_at === 'string';
}
export function isHostRoom(value: unknown): value is HostRoom {
  return isRecord(value) && typeof value.session_id === 'string' && uuidPattern.test(value.session_id)
    && typeof value.game_pin === 'string' && /^[1-9]\d{5}$/.test(value.game_pin) && typeof value.quiz_id === 'string'
    && typeof value.quiz_version === 'string' && typeof value.host_capability === 'string' && tokenPattern.test(value.host_capability)
    && typeof value.expires_at === 'string' && typeof value.control_revision === 'number';
}
export function isPending(value: unknown): value is PendingCreate {
  return isRecord(value) && typeof value.key === 'string' && uuidPattern.test(value.key)
    && typeof value.quiz_id === 'string' && typeof value.quiz_version === 'string';
}
export function readSession<T>(key: string, guard: (value: unknown) => value is T): T | null {
  const raw = sessionStorage.getItem(key);
  if (!raw) return null;
  const value: unknown = JSON.parse(raw);
  if (!guard(value)) throw new Error('Saved host data is invalid and has been preserved. Restore this tab’s session storage before continuing.');
  return value;
}
export async function api<T>(path: string, guard: (value: unknown) => value is T, init?: RequestInit): Promise<T> {
  let response: Response;
  try {
    response = await fetch(`/api/v1${path}`, {
      ...init, credentials: 'include',
      headers: { 'Content-Type': 'application/json', ...init?.headers },
    });
  } catch {
    throw new Error('We couldn’t reach the quiz service. Check your connection and try again. Saved recovery information is preserved.');
  }
  let value: unknown;
  try { value = await response.json(); }
  catch { throw new Error(`The quiz service is temporarily unavailable (${response.status}). Please try again; your saved data is safe.`); }
  if (!response.ok) {
    if (isRecord(value) && isRecord(value.error) && typeof value.error.code === 'string' && typeof value.error.message === 'string') {
      throw new ApiError(value.error.code, value.error.message, value.error.retryable === true);
    }
    throw new Error(`Request failed (${response.status}). Please try again.`);
  }
  if (!guard(value)) throw new Error('The server returned an incompatible response.');
  return value;
}
