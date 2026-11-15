import { useEffect, useRef, type FormEvent, type ReactNode } from 'react';
import type { Identity } from '../../shared/identity';
import type { PlayerSnapshot } from '../../shared/protocol';
import type { Connection } from '../../shared/useGame';
import { PlayerGameView } from './PlayerGameView';
import './player.css';

interface Props {
  identity: Identity | null;
  name: string;
  pin: string;
  onNameChange: (name: string) => void;
  onPinChange: (pin: string) => void;
  onJoin: (event: FormEvent) => void;
  joinDisabled: boolean;
  onForget: () => void;
  onLeave: () => void;
  snapshot: PlayerSnapshot | null;
  seconds: number;
  connection: Connection;
  pending: string | null;
  onAnswer: (optionId: string) => void;
  children: ReactNode;
}

export function PlayerExperience({
  identity, name, pin, onNameChange, onPinChange, onJoin, joinDisabled,
  onForget, onLeave, snapshot, seconds, connection, pending, onAnswer, children,
}: Props) {
  const saved = !!identity?.pin;
  const connected = connection === 'connected';
  const label = { offline: 'Offline', connecting: 'Connecting', connected: 'Connected', recovering: 'Reconnecting', paused: 'Recovery paused' }[connection];
  const main = useRef<HTMLElement>(null);
  const phase = saved ? `${snapshot?.state ?? 'connecting'}:${snapshot?.question?.id ?? ''}` : 'join';
  useEffect(() => {
    main.current?.querySelector<HTMLElement>('h1')?.focus({ preventScroll: true });
  }, [phase]);

  return <div className="player-shell">
    <header className="play-header">
      <a className="play-brand" href="#" aria-label="Quiz Live home"><span aria-hidden="true">Q</span> Quiz <strong>Live</strong></a>
      {saved ? <span className="play-connection" role="status"><i className={connected ? 'is-connected' : ''} aria-hidden="true" />{label}</span>
        : <a className="play-workspace-link" href="#host">Host a quiz</a>}
    </header>
    <main className="play-main" ref={main}>
      <div className="play-notices">{children}</div>
      {!saved ? <section className="play-join">
        <div className="play-mosaic" aria-hidden="true"><i /><i /><i /><i /></div>
        <p className="play-eyebrow">A little friendly competition</p>
        <h1 tabIndex={-1}>Big questions.<br />Your moment.</h1>
        <p className="play-subtitle">Grab your game PIN. Bring your best guess.</p>
        <form className="play-join-form" onSubmit={onJoin}>
          <label htmlFor="pin">Game PIN</label>
          <input id="pin" className="play-pin-input" inputMode="numeric" autoComplete="off" placeholder="123456"
            pattern="[1-9][0-9]{5}" maxLength={6} required value={pin}
            onChange={event => onPinChange(event.target.value.replace(/\D/g, ''))} />
          <label htmlFor="name">Your display name</label>
          <input id="name" autoComplete="nickname" placeholder="What should we call you?" required maxLength={80}
            aria-describedby="play-name-hint" value={name} onChange={event => onNameChange(event.target.value)} />
          <p id="play-name-hint">Use 1-40 characters your room will recognize.</p>
          <button className="play-join-button" disabled={joinDisabled}>Join game <span aria-hidden="true">&rarr;</span></button>
        </form>
        <p className="play-save-note">Your place is saved on this device.</p>
      </section> : snapshot ? <PlayerGameView snapshot={snapshot} playerId={identity?.playerId ?? ''} playerName={identity?.name ?? ''}
        seconds={seconds} connected={connected} pending={pending} onAnswer={onAnswer} />
        : <section className="play-center">
          <div className="play-loader" aria-hidden="true" />
          <p className="play-eyebrow">Hold your place</p>
          <h1 tabIndex={-1}>{identity?.sessionId ? 'Recovering your game' : 'Joining your game'}</h1>
          <p>We are contacting the room. Your saved identity stays safe.</p>
        </section>}
    </main>
    {saved && <footer className="play-session">
      <span className="play-player-name">{identity?.name}</span>
      <span>PIN <strong>{identity?.pin}</strong></span>
      {snapshot?.own_score !== undefined && <span><strong>{snapshot.own_score.toLocaleString()}</strong> pts</span>}
      <details className="play-session-actions">
        <summary>Game options</summary>
        <div>
          <button disabled={!connected || !!pending} onClick={onLeave}>Leave game</button>
          <button onClick={onForget}>Forget saved game</button>
          <p>Forgetting removes recovery on this device. It does not leave the server game.</p>
        </div>
      </details>
    </footer>}
  </div>;
}
