import http from 'k6/http';
import ws from 'k6/ws';
import { check, fail } from 'k6';
import { Counter, Trend } from 'k6/metrics';
import crypto from 'k6/crypto';
import encoding from 'k6/encoding';

const games = Number(__ENV.GAMES || 2);
const players = Number(__ENV.PLAYERS || 5);
const base = __ENV.BASE_URL || 'http://localhost:8088';
const origin = __ENV.ORIGIN || base;
const backends = (__ENV.BACKEND_URLS || base).split(',');
const recovered = new Counter('quiz_recovery_success');
const answers = new Counter('quiz_accepted_answers');
const latency = new Trend('quiz_answer_ack_ms');
export const options = {
  scenarios: { gameplay: { executor: 'per-vu-iterations', vus: games * (players + 1), iterations: 1, maxDuration: '3m' } },
  thresholds: { checks: ['rate==1'], ws_connecting: ['p(95)<1000'], quiz_answer_ack_ms: ['p(95)<500'] },
};
function uuid() {
  const bytes = new Uint8Array(crypto.randomBytes(16));
  bytes[6] = (bytes[6] & 15) | 64; bytes[8] = (bytes[8] & 63) | 128;
  const hex = Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('');
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`;
}
export function setup() {
  if (!__ENV.ADMIN_CREDENTIAL) fail('ADMIN_CREDENTIAL is required');
  const headers = { 'Content-Type': 'application/json', Origin: origin };
  const auth = http.post(`${base}/api/v1/auth/session`, JSON.stringify({ credential: __ENV.ADMIN_CREDENTIAL }), { headers });
  if (auth.status !== 200) fail(`login failed: ${auth.status}`);
  const csrf = auth.json('csrf_token');
  const cookie = auth.cookies['__Host-quiz_admin'] || auth.cookies.quiz_admin;
  if (!cookie) fail('admin session cookie missing');
  const cookieName = auth.cookies['__Host-quiz_admin'] ? '__Host-quiz_admin' : 'quiz_admin';
  const cookieHeader = `${cookieName}=${cookie[0].value}`;
  const catalog = http.get(`${base}/api/v1/quizzes`).json();
  const rooms = [];
  for (let i = 0; i < games; i++) {
    const response = http.post(`${base}/api/v1/games`, JSON.stringify({ quiz_id: __ENV.QUIZ_ID || catalog.quizzes[0].id, quiz_version: catalog.catalog_version }), {
      headers: { ...headers, 'X-CSRF-Token': csrf, Cookie: cookieHeader, 'Idempotency-Key': uuid() },
    });
    if (response.status !== 201) fail(`create failed: ${response.status}`);
    const room = response.json();
    const metadata = catalog.quizzes.find((quiz) => quiz.id === room.quiz_id);
    if (!metadata) fail('created quiz metadata missing');
    rooms.push({ ...room, question_count: metadata.question_count });
  }
  return { rooms, csrf, cookieHeader };
}
export default function(data) {
  const host = __VU <= games;
  const room = data.rooms[host ? __VU - 1 : (__VU - games - 1) % games];
  const player = uuid();
  const token = encoding.b64encode(crypto.randomBytes(32), 'rawurl');
  const pending = new Map();
  const answered = new Set();
  const advanced = new Set();
  let completed = false;
  let accepted = 0;
  const backendIndex = host ? __VU - 1 : Math.floor((__VU - games - 1) / games);
  const endpoint = backends[backendIndex % backends.length];
  const response = ws.connect(`${endpoint.replace(/^http/, 'ws')}/ws`, {
    headers: { Origin: origin, 'Sec-WebSocket-Protocol': 'quiz.v1', ...(host ? { Cookie: data.cookieHeader } : {}) },
    tags: { role: host ? 'host' : 'player' },
  }, (socket) => {
    const send = (type, payload) => {
      const id = uuid();
      socket.send(JSON.stringify({
        event_id: id, event_type: type, protocol_version: 1,
        game_pin: room.game_pin, session_id: room.session_id,
        ...(host ? {} : { player_id: player }), timestamp: new Date().toISOString(), payload,
      }));
      return id;
    };
    socket.on('open', () => send(host ? 'host_connect' : 'join_game', host
      ? { host_capability: room.host_capability, csrf_token: data.csrf }
      : { player_name: `Load-${__VU}`, reconnect_token: token }));
    socket.on('message', (raw) => {
      const event = JSON.parse(raw);
      if (event.event_type === 'error' || event.event_type === 'reconnect_failed') {
        console.error(`VU ${__VU}: ${event.payload.code}`);
        check(event, { 'no gameplay rejection': () => false }); socket.close(); return;
      }
      if (event.event_type === 'reconnect_success') recovered.add(1);
      if (event.event_type === 'answer_received') {
        accepted++;
        answers.add(1);
        const start = pending.get(event.reply_to);
        if (start) { latency.add(Date.now() - start); pending.delete(event.reply_to); }
      }
      if (['state_snapshot', 'host_connected', 'game_joined'].includes(event.event_type)) {
        const state = event.payload;
        if (host && state.state === 'LOBBY' && !advanced.has('start')) {
          advanced.add('start');
          socket.setTimeout(() => send('start_game', { expected_control_revision: state.control_revision }), 12000);
        }
        if (!host && state.state === 'QUESTION_OPEN' && !answered.has(state.question.id)) {
          answered.add(state.question.id);
          pending.set(send('submit_answer', { question_id: state.question.id, option_id: state.question.options[0].id }), Date.now());
        }
        if (host && state.state === 'QUESTION_CLOSED' && !advanced.has(state.control_revision)) {
          advanced.add(state.control_revision);
          socket.setTimeout(() => send('next_question', { expected_control_revision: state.control_revision }), 1000);
        }
        if (state.state === 'FINISHED') { completed = true; socket.close(); }
      }
    });
    socket.on('close', (code) => {
      if (!completed) console.error(`VU ${__VU}: socket closed before final snapshot (${code})`);
    });
    socket.setTimeout(() => socket.close(), 170000);
  });
  check(response, {
    'WebSocket upgraded': (r) => r && r.status === 101,
    'game finished': () => completed,
    'every player answer acknowledged': () => host || accepted === room.question_count,
  });
}
