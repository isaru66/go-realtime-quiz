import { useCallback, useEffect, useRef, useState } from 'react';
import type { HostRoom, Auth } from './api';
import type { Identity } from './identity';
import { clearGame, saveIdentity } from './identity';
import { parseServerMessage, type Command, type Snapshot, type ServerMessage } from './protocol';
import { backoff, friendlyError, stopsRecovery } from './recovery';

export type Connection = 'offline' | 'connecting' | 'connected' | 'recovering' | 'paused';
export type Binding = { role: 'player'; identity: Identity } | { role: 'host'; room: HostRoom; auth: Auth };
export function useGame(binding: Binding | null, updateIdentity: (identity: Identity) => void, onHostExpired: () => void, onAuthRequired?: () => void) {
  const [snapshot, setSnapshot] = useState<Snapshot | null>(null);
  const [connection, setConnection] = useState<Connection>('offline');
  const [error, setError] = useState('');
  const [pending, setPending] = useState<string | null>(null);
  const [retry, setRetry] = useState(0);
  const socket = useRef<WebSocket | null>(null);
  const bound = useRef(false);
  const current = useRef(binding);
  const identityCallback = useRef(updateIdentity);
  const hostExpiredCallback = useRef(onHostExpired);
  const authRequiredCallback = useRef(onAuthRequired);
  current.current = binding;
  identityCallback.current = updateIdentity;
  hostExpiredCallback.current = onHostExpired;
  authRequiredCallback.current = onAuthRequired;
  const answerCommand = useRef<(Command & { event_id: string }) | null>(null);
  const leaveCommand = useRef<string | null>(null);
  const key = binding?.role === 'player'
    ? binding.identity.pin && binding.identity.token ? `player:${binding.identity.playerId}:${binding.identity.pin}:${binding.identity.token}` : ''
    : binding ? `host:${binding.room.session_id}:${binding.auth.csrf_token}` : '';
  const send = useCallback((command: Command, eventId: string = crypto.randomUUID()) => {
    const active = current.current;
    if (!active || socket.current?.readyState !== WebSocket.OPEN) return false;
    const identity = active.role === 'player' ? active.identity : null;
    const sessionId = identity?.sessionId ?? (active.role === 'host' ? active.room.session_id : undefined);
    const envelope = {
      event_id: eventId, event_type: command.event_type, protocol_version: 1,
      game_pin: identity?.pin ?? (active.role === 'host' ? active.room.game_pin : ''),
      timestamp: new Date().toISOString(), payload: command.payload,
      ...(sessionId ? { session_id: sessionId } : {}),
      ...(identity ? { player_id: identity.playerId } : {}),
    };
    socket.current.send(JSON.stringify(envelope));
    return true;
  }, []);
  useEffect(() => {
    setSnapshot(null); setPending(null);
    answerCommand.current = null; leaveCommand.current = null;
    if (!key) { setConnection('offline'); return; }
    setError('');
    let disposed = false;
    let stopped = false;
    let attempt = 0;
    let revision = 0;
    let delayHint = 0;
    let timer: ReturnType<typeof setTimeout>;
    let bindTimer: ReturnType<typeof setTimeout>;
    let heartbeat: ReturnType<typeof setInterval>;
    let localSocket: WebSocket;
    const finish = (expired: boolean) => {
      stopped = true;
      if (expired) setError('The server confirmed that this game has expired. Your persistent player identity is safe.');
      const active = current.current;
      if (active?.role === 'player') {
        const clean = clearGame(localStorage, active.identity);
        identityCallback.current(clean);
      } else if (expired) hostExpiredCallback.current();
      bound.current = false; setConnection('offline'); setPending(null);
      localSocket.close(1000);
    };
    function handle(message: ServerMessage) {
      const active = current.current;
      if (!active) return;
      const expectedPin = active.role === 'player' ? active.identity.pin : active.room.game_pin;
      const expectedSession = active.role === 'player' ? active.identity.sessionId : active.room.session_id;
      if (message.game_pin !== expectedPin || (expectedSession && message.session_id && expectedSession !== message.session_id)
        || (message.player_id && (active.role !== 'player' || message.player_id !== active.identity.playerId))) return;
      switch (message.event_type) {
        case 'game_joined': case 'reconnect_success': case 'host_connected': case 'state_snapshot': {
          if (message.payload.role !== active.role || !message.session_id || !message.revision) throw new Error('The server returned a mismatched session.');
          const recovery = message.event_type !== 'state_snapshot';
          if (!recovery && (!bound.current || message.revision < revision)) return;
          revision = message.revision; bound.current = true; attempt = 0; delayHint = 0;
          clearTimeout(bindTimer); setConnection('connected'); setError(''); setPending(null);
          if (active.role === 'player') {
            const next = { ...active.identity, sessionId: message.session_id, expiresAt: message.payload.expires_at };
            saveIdentity(localStorage, next); current.current = { role: 'player', identity: next }; identityCallback.current(next);
          }
          setSnapshot(message.payload);
          if (message.payload.state === 'EXPIRED') { finish(true); return; }
          if (leaveCommand.current) {
            setPending('leave_game');
            send({ event_type: 'leave_game', payload: {} }, leaveCommand.current);
          } else if (answerCommand.current && active.role === 'player' && message.payload.role === 'player') {
            const command = answerCommand.current;
            if (command.event_type === 'submit_answer' && message.payload.state === 'QUESTION_OPEN'
              && message.payload.question?.id === command.payload.question_id && !message.payload.own_answer) {
              send(command, command.event_id); setPending('submit_answer');
            } else answerCommand.current = null;
          }
          break;
        }
        case 'error': case 'reconnect_failed':
          setError(friendlyError(message.payload)); setPending(null);
          if (message.payload.code === 'session_expired') { finish(true); break; }
          if (message.payload.code === 'unauthenticated' && active.role === 'host') {
            stopped = true; bound.current = false; authRequiredCallback.current?.(); localSocket.close(1000); break;
          }
          if (message.payload.code === 'connection_replaced') {
            stopped = true; bound.current = false; setConnection('paused'); localSocket.close(1000); break;
          }
          if (message.payload.code === 'stale_control_revision' || message.payload.code === 'already_answered') {
            send({ event_type: 'request_snapshot', payload: {} }); break;
          }
          if (!bound.current) {
            stopped = stopsRecovery(message.payload); delayHint = message.payload.retry_after_ms ?? 0;
            setConnection(stopped ? 'paused' : 'recovering'); localSocket.close(1000);
          }
          break;
        case 'answer_received':
          answerCommand.current = null; setPending(null);
          setSnapshot(previous => previous?.role === 'player' && previous.state === 'QUESTION_OPEN' && previous.question?.id === message.payload.question_id
            ? { ...previous, own_answer: message.payload } : previous);
          break;
        case 'game_left': finish(false); setSnapshot(null); break;
        case 'command_accepted': setPending(null); send({ event_type: 'request_snapshot', payload: {} }); break;
        case 'server_draining': localSocket.close(4000); break;
        default: break; // Complete snapshots, not partial events, are the authoritative UI state.
      }
    }
    function connect() {
      if (disposed || stopped) return;
      bound.current = false;
      setConnection(attempt ? 'recovering' : 'connecting');
      localSocket = new WebSocket(`${location.protocol === 'https:' ? 'wss:' : 'ws:'}//${location.host}/ws`, 'quiz.v1');
      socket.current = localSocket;
      localSocket.onopen = () => {
        const active = current.current;
        if (!active || disposed) return;
        if (active.role === 'host') send({ event_type: 'host_connect', payload: { host_capability: active.room.host_capability, csrf_token: active.auth.csrf_token } });
        else if (active.identity.sessionId) send({ event_type: 'reconnect', payload: { reconnect_token: active.identity.token ?? '', ...(revision ? { last_revision: revision } : {}) } });
        else send({ event_type: 'join_game', payload: { player_name: active.identity.name, reconnect_token: active.identity.token ?? '' } });
        bindTimer = setTimeout(() => localSocket.close(4000), 10000);
        heartbeat = setInterval(() => {
          if (bound.current) send({ event_type: 'request_snapshot', payload: {} });
        }, 15000);
      };
      localSocket.onmessage = event => {
        if (disposed || stopped) return;
        try {
          if (typeof event.data !== 'string') throw new Error('Unsupported server message.');
          handle(parseServerMessage(event.data));
        } catch (failure) {
          stopped = true; setConnection('paused'); setError(failure instanceof Error ? failure.message : 'Unable to read server state.');
          localSocket.close(1000);
        }
      };
      localSocket.onclose = event => {
        clearTimeout(bindTimer); clearInterval(heartbeat); bound.current = false;
        if (disposed || stopped) return;
        if ([1003, 1008, 1009].includes(event.code)) {
          stopped = true; setConnection('paused'); setError('Connection paused by the server. Another tab may be active, or sign-in may be required. Your credentials are preserved.'); return;
        }
        setConnection(navigator.onLine ? 'recovering' : 'offline');
        timer = setTimeout(connect, backoff(attempt++, Math.random, delayHint));
      };
      localSocket.onerror = () => { /* onclose owns recovery, avoiding duplicate reconnect timers. */ };
    }
    connect();
    return () => {
      disposed = true; clearTimeout(timer); clearTimeout(bindTimer); clearInterval(heartbeat);
      bound.current = false;
      localSocket?.close(1000);
      socket.current = null;
    };
  }, [key, retry, send]);
  function command(value: Command) {
    if (!bound.current || pending) return;
    const event_id = crypto.randomUUID();
    if (value.event_type === 'submit_answer') answerCommand.current = { ...value, event_id };
    if (value.event_type === 'leave_game') leaveCommand.current = event_id;
    if (send(value, event_id)) {
      setPending(value.event_type);
      setError('');
    }
  }
  return { snapshot, connection, error, pending, command, recover: () => setRetry(value => value + 1) };
}
