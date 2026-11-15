import Ajv from 'ajv/dist/2020';
import addFormats from 'ajv-formats';
import schema from './protocol-v1.schema.json';

export interface Question {
  id: string; type: 'single_choice'; text: string;
  options: { id: string; text: string }[];
  index: number; total: number; points: number; started_at: string; deadline: string;
}
export interface Member { player_id: string; player_name: string; connected: boolean; left: boolean }
export interface Rank { player_id: string; player_name: string; score: number; rank: number; left: boolean }
export interface AcceptedAnswer { question_id: string; accepted: true; option_id: string; accepted_at: string }
export interface Result { question_id: string; correct_option_id: string; answered: boolean; correct: boolean; awarded_points: number }
interface BaseSnapshot {
  state: 'LOBBY' | 'QUESTION_OPEN' | 'QUESTION_CLOSED' | 'FINISHED' | 'EXPIRED';
  quiz_id: string; quiz_version: string; server_time: string; expires_at: string;
  control_revision: number; roster: Member[]; leaderboard: Rank[]; question?: Question;
  closed_result?: { question_id: string; correct_option_id: string; closed_at: string };
}
export interface PlayerSnapshot extends BaseSnapshot {
  role: 'player'; own_score?: number; own_answer?: AcceptedAnswer; own_result?: Result;
}
export interface HostSnapshot extends BaseSnapshot {
  role: 'host';
  statistics?: { question_id: string; counts: Record<string, number>; answer_count: number };
}
export type Snapshot = PlayerSnapshot | HostSnapshot;
export interface WireError { code: string; message: string; retryable: boolean; retry_after_ms?: number }
interface Envelope { event_id: string; game_pin: string; session_id?: string; player_id?: string; revision?: number; reply_to?: string; timestamp: string; protocol_version: 1 }
export type ServerMessage = Envelope & (
  { event_type: 'game_joined' | 'reconnect_success' | 'host_connected' | 'state_snapshot'; payload: Snapshot } |
  { event_type: 'error' | 'reconnect_failed'; payload: WireError } |
  { event_type: 'answer_received'; payload: AcceptedAnswer } |
  { event_type: 'game_left' | 'command_accepted' | 'server_draining' | 'game_started' | 'game_finished'; payload: Record<string, never> } |
  { event_type: 'question_started'; payload: Question } |
  { event_type: 'player_joined' | 'player_left'; payload: Member } |
  { event_type: 'question_timer'; payload: { question_id: string; server_time: string; deadline: string } } |
  { event_type: 'pong'; payload: { server_time: string } } |
  { event_type: 'question_ended'; payload: NonNullable<BaseSnapshot['closed_result']> } |
  { event_type: 'answer_result'; payload: Result } |
  { event_type: 'answer_statistics'; payload: NonNullable<HostSnapshot['statistics']> } |
  { event_type: 'leaderboard_updated'; payload: { leaderboard: Rank[] } }
);
export type Command =
  { event_type: 'join_game'; payload: { player_name: string; reconnect_token: string } } |
  { event_type: 'reconnect'; payload: { reconnect_token: string; last_revision?: number } } |
  { event_type: 'host_connect'; payload: { host_capability: string; csrf_token: string } } |
  { event_type: 'submit_answer'; payload: { question_id: string; option_id: string } } |
  { event_type: 'start_game' | 'next_question' | 'end_game'; payload: { expected_control_revision: number } } |
  { event_type: 'leave_game' | 'request_snapshot' | 'ping'; payload: Record<string, never> };

const ajv = new Ajv({ strict: false, allErrors: false });
addFormats(ajv);
ajv.addSchema(schema, 'protocol');
const validate = ajv.compile<ServerMessage>({ $ref: 'protocol#/$defs/server' });
export function parseServerMessage(raw: string): ServerMessage {
  const value: unknown = JSON.parse(raw);
  if (!validate(value)) throw new Error('The server sent an incompatible message. Please refresh after checking your connection.');
  return value;
}
export function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}
