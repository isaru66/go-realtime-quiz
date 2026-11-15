import { useEffect, useState, type FormEvent, type ReactNode } from 'react';
import { QRCodeSVG } from 'qrcode.react';
import { api, ApiError, isAuth, isCatalog, isConfig, isHostRoom, isPending, readSession, type AdminConfig, type Auth, type Catalog, type HostRoom, type PendingCreate } from '../shared/api';
import { clearGame, loadIdentity, prepareJoinWithLock, type Identity } from '../shared/identity';
import { useGame, type Binding } from '../shared/useGame';
import type { Snapshot } from '../shared/protocol';

type View = 'player' | 'host' | 'admin';
function viewFromHash(): View { return location.hash === '#host' ? 'host' : location.hash === '#admin' ? 'admin' : 'player'; }
function failureMessage(error: unknown): string { return error instanceof Error ? error.message : 'Something went wrong. Please try again.'; }
function Panel({ children, className = '' }: { children: ReactNode; className?: string }) {
  return <section className={`panel ${className}`}>{children}</section>;
}
function Badge({ children, good = false }: { children: ReactNode; good?: boolean }) {
  return <span className={`badge ${good ? 'good' : ''}`}>{children}</span>;
}
export default function App() {
  const [view, setView] = useState<View>(viewFromHash);
  const [identity, setIdentity] = useState<Identity | null>(null);
  const [auth, setAuth] = useState<Auth | null>(null);
  const [room, setRoom] = useState<HostRoom | null>(null);
  const [pendingCreate, setPendingCreate] = useState<PendingCreate | null>(null);
  const [bootError, setBootError] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [catalog, setCatalog] = useState<Catalog | null>(null);
  const [config, setConfig] = useState<AdminConfig | null>(null);
  const [name, setName] = useState('');
  const [pin, setPin] = useState(new URLSearchParams(location.search).get('pin') ?? '');
  const [credential, setCredential] = useState('');
  const [selected, setSelected] = useState('');
  const [clock, setClock] = useState(Date.now());
  useEffect(() => {
    try {
      const saved = loadIdentity(localStorage); setIdentity(saved); setName(saved.name); if (saved.pin) setPin(saved.pin);
      setAuth(readSession('quiz.auth.v1', isAuth)); setRoom(readSession('quiz.host.v1', isHostRoom));
      setPendingCreate(readSession('quiz.create.v1', isPending));
    } catch (failure) { setBootError(failureMessage(failure)); }
    const hash = () => setView(viewFromHash());
    window.addEventListener('hashchange', hash);
    return () => window.removeEventListener('hashchange', hash);
  }, []);
  useEffect(() => { const timer = setInterval(() => setClock(Date.now()), 250); return () => clearInterval(timer); }, []);
  async function fetchCatalog() {
    try { setCatalog(await api('/quizzes', isCatalog)); } catch (failure) { setError(failureMessage(failure)); }
  }
  useEffect(() => { if (view === 'host') void fetchCatalog(); }, [view]);
  async function fetchConfig() {
    setBusy(true); setError('');
    try { setConfig(await api('/admin/config', isConfig)); } catch (failure) {
      setError(failureMessage(failure)); if (failure instanceof ApiError && failure.code === 'unauthenticated') requireSignIn();
    }
    finally { setBusy(false); }
  }
  useEffect(() => { if (view === 'admin' && auth) void fetchConfig(); }, [view, auth]);
  function expireHost() { sessionStorage.removeItem('quiz.host.v1'); setRoom(null); }
  function requireSignIn() {
    sessionStorage.removeItem('quiz.auth.v1'); setAuth(null); setConfig(null);
    setError('Please sign in again. Your saved host room and pending creation request are preserved.');
  }
  const binding: Binding | null = view === 'player' && identity?.pin && identity.token
    ? { role: 'player', identity }
    : view === 'host' && room && auth ? { role: 'host', room, auth } : null;
  const game = useGame(binding, setIdentity, expireHost, requireSignIn);
  const active = game.snapshot;
  const connected = game.connection === 'connected';
  const savedPlayer = !!identity?.pin;
  const label = { offline: 'Offline', connecting: 'Connecting', connected: 'Connected', recovering: 'Recovering', paused: 'Recovery paused' }[game.connection];
  const [timeSample, setTimeSample] = useState({ offset: 0, server: '' });
  useEffect(() => {
    if (active && active.server_time !== timeSample.server) setTimeSample({ server: active.server_time, offset: Date.parse(active.server_time) - Date.now() });
  }, [active, timeSample.server]);
  const seconds = active?.question ? Math.max(0, Math.ceil((Date.parse(active.question.deadline) - (clock + timeSample.offset)) / 1000)) : 0;
  async function login(event: FormEvent) {
    event.preventDefault(); setBusy(true); setError('');
    try {
      const result = await api('/auth/session', isAuth, { method: 'POST', body: JSON.stringify({ credential }) });
      sessionStorage.setItem('quiz.auth.v1', JSON.stringify(result)); setAuth(result); setCredential('');
    } catch (failure) { setError(failureMessage(failure)); }
    finally { setBusy(false); }
  }
  async function logout() {
    if (!auth) return;
    setBusy(true); setError('');
    try {
      const response = await fetch('/api/v1/auth/session', { method: 'DELETE', credentials: 'include', headers: { 'X-CSRF-Token': auth.csrf_token } });
      if (!response.ok && response.status !== 401) throw new Error('Sign-out could not be confirmed. Please retry.');
      sessionStorage.removeItem('quiz.auth.v1'); setAuth(null); setConfig(null);
    } catch (failure) { setError(failureMessage(failure)); }
    finally { setBusy(false); }
  }
  async function createGame() {
    if (!auth || !catalog) return;
    setBusy(true); setError('');
    try {
      const request = pendingCreate ?? { key: crypto.randomUUID(), quiz_id: selected, quiz_version: catalog.catalog_version };
      if (!request.quiz_id) throw new Error('Select a quiz first.');
      sessionStorage.setItem('quiz.create.v1', JSON.stringify(request)); setPendingCreate(request);
      const created = await api('/games', isHostRoom, {
        method: 'POST', headers: { 'X-CSRF-Token': auth.csrf_token, 'Idempotency-Key': request.key },
        body: JSON.stringify({ quiz_id: request.quiz_id, quiz_version: request.quiz_version }),
      });
      sessionStorage.setItem('quiz.host.v1', JSON.stringify(created)); setRoom(created);
      sessionStorage.removeItem('quiz.create.v1'); setPendingCreate(null);
    } catch (failure) {
      setError(failureMessage(failure));
      if (failure instanceof ApiError && failure.code === 'unauthenticated') requireSignIn();
      if (failure instanceof ApiError && ['configuration_changed', 'quiz_not_found', 'session_expired'].includes(failure.code)) {
        sessionStorage.removeItem('quiz.create.v1'); setPendingCreate(null); await fetchCatalog();
      }
    } finally { setBusy(false); }
  }
  async function join(event: FormEvent) {
    event.preventDefault(); setError('');
    try { if (identity) setIdentity(await prepareJoinWithLock(localStorage, identity, name, pin)); }
    catch (failure) { setError(failureMessage(failure)); }
  }
  function forget() {
    if (identity && confirm('Forget this saved game on this device? This does not leave the server game and cannot be undone.')) {
      try { setIdentity(clearGame(localStorage, identity)); setError(''); }
      catch (failure) { setError(failureMessage(failure)); }
    }
  }
  function hostControl(event_type: 'start_game' | 'next_question' | 'end_game') {
    if (!active) return;
    if (event_type === 'end_game' && !confirm('End this game for everyone and publish final results?')) return;
    game.command({ event_type, payload: { expected_control_revision: active.control_revision } });
  }
  const joinUrl = `${location.origin}/?pin=${view === 'host' ? room?.game_pin ?? '' : identity?.pin ?? ''}`;
  const livePin = view === 'host' ? room?.game_pin : identity?.pin;
  return <div className="app-shell">
    <header className="topbar">
      <a href="#" className="brand" aria-label="Quiz Live home"><span className="brand-icon">Q</span><span>Quiz <strong>Live</strong></span></a>
      <span className="topbar-caption">Your live quiz workspace</span>
      <span className="workspace-label">LIVE EXPERIENCES</span>
    </header>
    <div className="workspace">
      <aside className="sidebar">
        <p className="nav-heading">WORKSPACE</p>
        <nav aria-label="Main navigation">
          <a href="#" aria-current={view === 'player' ? 'page' : undefined}><span aria-hidden="true">◈</span> Play a quiz</a>
          <a href="#host" aria-current={view === 'host' ? 'page' : undefined}><span aria-hidden="true">▣</span> Host workspace</a>
          <a href="#admin" aria-current={view === 'admin' ? 'page' : undefined}><span aria-hidden="true">☷</span> Configuration</a>
        </nav>
        <div className="sidebar-note"><span className="mini-logo">Q</span><strong>Made for connection.</strong><p>One room. Shared moments.<br />A little friendly competition.</p></div>
        <div className="sidebar-footer">QUIZ LIVE <span>Protocol v1</span></div>
      </aside>
      <main>
        <div className="breadcrumb">Workspace <span>/</span> {view === 'player' ? 'Play a quiz' : view === 'host' ? 'Host workspace' : 'Configuration'}</div>
        <div className="page-heading">
          <div><p className="eyebrow">{view === 'player' ? 'EVERY ANSWER COUNTS' : view === 'host' ? 'BRING YOUR ROOM TOGETHER' : 'CATALOG OPERATIONS'}</p><h1>{view === 'player' ? savedPlayer ? 'Your live game' : 'Ready to play?' : view === 'host' ? 'Host workspace' : 'Configuration overview'}</h1>
            <p className="subtitle">{view === 'player' ? 'Join the conversation. Test what you know. Enjoy the moment.' : view === 'host' ? 'Choose a quiz, invite your players, and take the lead.' : 'A read-only view of the active quiz catalog and validation health.'}</p></div>
          {binding && <div className={`connection ${connected ? 'online' : ''}`} role="status"><span />{label}</div>}
        </div>
        {bootError && <div className="alert" role="alert">{bootError}<button className="link-button" onClick={() => {
          if (confirm('Permanently forget the saved player identity and host recovery information on this device?')) {
            localStorage.removeItem('quiz.player.v1'); ['quiz.auth.v1', 'quiz.host.v1', 'quiz.create.v1'].forEach(key => sessionStorage.removeItem(key)); location.reload();
          }
        }}>Explicitly forget saved identity</button></div>}
        {(error || game.error) && <div className="alert" role="alert">{error || game.error}</div>}
        {binding && !connected && <div className="notice" role="status"><strong>{game.connection === 'paused' ? 'Recovery needs your attention.' : 'Keeping your place safe.'}</strong> Your saved credentials stay on this device. {game.connection !== 'paused' && 'We’ll recover the latest game state when the connection returns.'}
          <button className="secondary small" onClick={game.recover}>Recover in this tab</button>{view === 'host' && <button className="link-button" onClick={requireSignIn}>Sign in again</button>}</div>}
        {view === 'player' && !savedPlayer && <div className="join-layout">
          <Panel className="join-panel"><div className="section-icon">↗</div><h2>Join a live quiz</h2><p className="muted">Enter the game PIN shared by your host.</p>
            <form onSubmit={join}>
              <label htmlFor="pin">Game PIN</label><input id="pin" inputMode="numeric" autoComplete="off" placeholder="123456" pattern="[1-9][0-9]{5}" maxLength={6} required value={pin} onChange={event => setPin(event.target.value.replace(/\D/g, ''))} className="pin-input" />
              <label htmlFor="name">Your display name</label><input id="name" autoComplete="nickname" placeholder="How should we call you?" required maxLength={80} value={name} onChange={event => setName(event.target.value)} />
              <p className="field-note">Choose a name everyone in your room will recognize.</p>
              <button className="primary full" disabled={!identity || !!bootError}>Join game <span aria-hidden="true">→</span></button>
            </form><div className="secure-note">▣ &nbsp; Your place is saved automatically on this device.</div>
          </Panel>
          <div className="welcome-column"><div className="welcome-art" aria-hidden="true"><div className="art-grid" /><div className="art-card art-back"><span>LIVE QUIZ</span><i /><i /></div><div className="art-card art-front"><span className="art-check">✓</span><strong>A great answer<br />starts with you.</strong><div className="art-options"><i /><i /><i /></div></div><div className="art-bubble">Let’s play! ↗</div></div>
            <h2>A shared challenge.<br />A brighter connection.</h2><p className="muted">From team check-ins to trivia nights, make every question a moment worth sharing.</p>
            <div className="feature-row"><span>01</span><div><strong>Get your game PIN</strong><p>Your host will share a six-digit code or QR.</p></div></div><div className="feature-row"><span>02</span><div><strong>Make yourself known</strong><p>Pick your display name and join the room.</p></div></div><div className="feature-row"><span>03</span><div><strong>Play in the moment</strong><p>Answer live and see where you land.</p></div></div>
          </div>
        </div>}
        {view === 'player' && savedPlayer && <>
          <div className="room-strip"><span>GAME PIN <strong>{identity?.pin}</strong></span><span>Playing as <strong>{identity?.name}</strong></span><div className="strip-actions"><button className="link-button" disabled={!connected || !!game.pending} onClick={() => { if (confirm('Leave this game? Your answers remain in the leaderboard.')) game.command({ event_type: 'leave_game', payload: {} }); }}>Leave game</button><button className="link-button muted" onClick={forget}>Forget saved game</button></div></div>
          {active ? <GameView snapshot={active} seconds={seconds} connected={connected} pending={game.pending} onAnswer={option_id => { if (active.question) game.command({ event_type: 'submit_answer', payload: { question_id: active.question.id, option_id } }); }} /> : <Panel className="empty-state"><div className="spinner" /><h2>Recovering your place</h2><p className="muted">Your name, credentials, and session are preserved while we contact the host’s room.</p></Panel>}
        </>}
        {view !== 'player' && !auth && <div className="auth-layout"><Panel><div className="section-icon">▣</div><h2>Sign in to your workspace</h2><p className="muted">Use your deployment’s administrator credential to host games or view catalog health.</p><form onSubmit={login}><label htmlFor="credential">Administrator credential</label><input id="credential" type="password" autoComplete="current-password" required value={credential} onChange={event => setCredential(event.target.value)} /><button className="primary full" disabled={busy || !!bootError}>{busy ? 'Signing in…' : 'Sign in securely →'}</button></form><p className="field-note">Credentials never appear in join links. Your browser uses a secure, HttpOnly session cookie.</p></Panel></div>}
        {view === 'host' && auth && !room && <div className="host-setup">
          <Panel><div className="panel-title"><div><h2>Create a new game</h2><p className="muted">Choose from the published quiz catalog.</p></div><button className="link-button" onClick={() => void fetchCatalog()}>Refresh</button></div>
            {pendingCreate && <div className="notice">A creation request is pending. Recover its result before creating another game. The original request key is retained.</div>}
            <div className="quiz-grid">{catalog?.quizzes.map(quiz => <label key={quiz.id} className={`quiz-card ${selected === quiz.id ? 'selected' : ''}`}><input type="radio" name="quiz" value={quiz.id} disabled={!!pendingCreate} checked={selected === quiz.id} onChange={() => setSelected(quiz.id)} /><div><span className="quiz-glyph">▤</span><h3>{quiz.title}</h3><p>{quiz.description || 'A fresh challenge for your room.'}</p><Badge>{quiz.question_count} questions</Badge></div></label>)}</div>
            {catalog?.quizzes.length === 0 && <p>No quizzes are currently available.</p>}
            {!catalog && <p className="muted">Loading catalog… Use Refresh if the server is unavailable.</p>}
            <div className="panel-actions"><button className="primary" disabled={busy || (!selected && !pendingCreate) || !catalog} onClick={() => void createGame()}>{busy ? 'Recovering creation…' : pendingCreate ? 'Recover pending creation →' : 'Create game →'}</button><button className="link-button" disabled={busy} onClick={() => void logout()}>Sign out</button></div>
          </Panel><Panel className="info-panel"><h3>A smooth start, every time.</h3><p>Invite players using your game’s PIN or QR code. You control when the first question begins.</p><hr /><p>Catalog version</p><code>{catalog?.catalog_version ?? 'Loading…'}</code><p className="field-note">Each game is pinned to this catalog version, so live changes won’t interrupt your room.</p></Panel>
        </div>}
        {view === 'host' && auth && room && <>
          <div className="room-strip"><span>HOSTING <strong>{room.quiz_id}</strong></span><Badge good={connected}>{active?.state.replaceAll('_', ' ') ?? 'CONNECTING'}</Badge><div className="strip-actions"><button className="link-button" disabled={busy} onClick={() => void logout()}>Sign out</button><button className="link-button muted" onClick={() => { if (confirm('Forget this host capability in this tab? You will lose the ability to control this room.')) { sessionStorage.removeItem('quiz.host.v1'); setRoom(null); } }}>Forget room</button></div></div>
          <div className="live-layout"><div>
            {active ? <GameView snapshot={active} seconds={seconds} connected={connected} pending={game.pending} onAnswer={() => undefined} /> : <Panel className="empty-state"><div className="spinner" /><h2>Connecting to your room</h2><p className="muted">Your host capability is safely retained in this tab.</p></Panel>}
            <Panel className="controls"><div><h2>Game controls</h2><p className="muted">You set the pace. The server keeps the score.</p></div><div className="control-buttons">
              {active?.state === 'LOBBY' && <button className="primary" disabled={!connected || !!game.pending} onClick={() => hostControl('start_game')}>Start game →</button>}
              {active?.state === 'QUESTION_CLOSED' && <button className="primary" disabled={!connected || !!game.pending} onClick={() => hostControl('next_question')}>{active.question && active.question.index + 1 >= active.question.total ? 'Finish game' : 'Next question →'}</button>}
              {active && !['FINISHED', 'EXPIRED'].includes(active.state) && <button className="secondary" disabled={!connected || !!game.pending} onClick={() => hostControl('end_game')}>End game</button>}
              <button className="link-button" disabled={!connected} onClick={() => game.command({ event_type: 'request_snapshot', payload: {} })}>Refresh state</button>
            </div></Panel>
          </div><div><Panel className="qr-panel"><p className="eyebrow">INVITE YOUR PLAYERS</p><h2>Scan. Join. Play.</h2><div className="qr-code"><QRCodeSVG value={joinUrl} size={180} level="M" title="Scan to join this game" /></div><p className="muted">Or enter the game PIN</p><div className="big-pin">{livePin}</div><a className="join-link" href={joinUrl} target="_blank" rel="noreferrer">{joinUrl}</a><button className="secondary full" onClick={() => { void navigator.clipboard.writeText(joinUrl).catch(() => setError('Copy was unavailable. Select the join link to copy it manually.')); }}>Copy join link</button></Panel><Panel className="info-panel"><h3>Only the essentials.</h3><p>Your join link contains only the game PIN. Host credentials are never shared with players.</p></Panel></div></div>
        </>}
        {view === 'admin' && auth && <>
          <div className="admin-toolbar"><Badge good>Read-only workspace</Badge><div><button className="secondary small" disabled={busy} onClick={() => void fetchConfig()}>{busy ? 'Refreshing…' : 'Refresh status'}</button><button className="link-button" disabled={busy} onClick={() => void logout()}>Sign out</button></div></div>
          {config ? <><div className="stat-grid"><Panel><p className="eyebrow">VALIDATION</p><h2>{config.validation_status === 'valid' ? 'Healthy catalog' : 'Reload rejected'}</h2><Badge good={config.validation_status === 'valid'}>{config.validation_status}</Badge></Panel><Panel><p className="eyebrow">PUBLISHED QUIZZES</p><div className="stat-number">{config.quizzes.length}</div><p className="muted">Available for new games</p></Panel><Panel><p className="eyebrow">SERVING POD</p><h3 className="break-all">{config.pod_id}</h3><p className="muted">Status is local to this pod</p></Panel></div>
            <Panel><h2>Catalog health</h2><dl className="metadata"><dt>Active version</dt><dd><code>{config.active_version}</code></dd><dt>Last validation attempt</dt><dd>{new Date(config.last_attempt_at).toLocaleString()}</dd><dt>Validation errors</dt><dd>{config.errors.length ? <ul>{config.errors.map((item, index) => <li key={index}>{item}</li>)}</ul> : 'No validation errors reported.'}</dd></dl></Panel>
            <Panel><div className="panel-title"><h2>Published catalog</h2><Badge>{config.quizzes.length} quizzes</Badge></div><div className="table-scroll"><table><thead><tr><th>Quiz</th><th>Description</th><th>Questions</th><th>Identifier</th></tr></thead><tbody>{config.quizzes.map(quiz => <tr key={quiz.id}><td><strong>{quiz.title}</strong></td><td>{quiz.description || '—'}</td><td>{quiz.question_count}</td><td><code>{quiz.id}</code></td></tr>)}</tbody></table></div></Panel>
          </> : <Panel className="empty-state"><h2>Configuration status</h2><p className="muted">Use Refresh status to request this pod’s latest validation metadata.</p></Panel>}
        </>}
        <footer className="page-footer"><span>Quiz Live · Better together, one question at a time.</span><span>Live state is synchronized with your host.</span></footer>
      </main>
    </div>
  </div>;
}

function GameView({ snapshot, seconds, connected, pending, onAnswer }: { snapshot: Snapshot; seconds: number; connected: boolean; pending: string | null; onAnswer: (option: string) => void }) {
  const players = snapshot.roster.filter(player => !player.left);
  const question = snapshot.question;
  const open = snapshot.state === 'QUESTION_OPEN';
  const player = snapshot.role === 'player' ? snapshot : null;
  return <div className="game-view">
    <div className="stat-grid compact"><Panel><p className="eyebrow">IN THE ROOM</p><div className="stat-number">{players.length}<small>players</small></div></Panel><Panel><p className="eyebrow">{player ? 'YOUR SCORE' : 'ROOM STATUS'}</p><div className={player ? 'stat-number' : 'status-title'}>{player ? <>{player.own_score ?? 0}<small>points</small></> : snapshot.state.replaceAll('_', ' ')}</div></Panel><Panel><p className="eyebrow">PROGRESS</p><div className="stat-number">{question ? `${question.index + 1}/${question.total}` : '—'}<small>questions</small></div></Panel></div>
    {snapshot.state === 'LOBBY' && <Panel className="lobby"><div className="lobby-orbit" aria-hidden="true">◈</div><Badge good>YOU’RE IN THE RIGHT PLACE</Badge><h2>{snapshot.role === 'host' ? 'Your room is ready.' : 'You’re in. Get comfortable.'}</h2><p className="muted">{snapshot.role === 'host' ? 'Share your PIN or QR code. Start the game when everyone is here.' : 'The host will start the quiz when everyone is ready. Keep this page open.'}</p><div className="roster">{players.map(member => <span className="player-chip" key={member.player_id}><span className="avatar">{[...member.player_name][0]?.toLocaleUpperCase()}</span>{member.player_name}<i className={member.connected ? 'present' : ''} aria-label={member.connected ? 'Connected' : 'Away'} /></span>)}</div>{players.length === 0 && <p className="muted">Waiting for the first player…</p>}</Panel>}
    {question && <Panel className="question-panel"><div className="question-topline"><Badge>Question {question.index + 1} of {question.total}</Badge><span className={open ? 'timer' : 'muted'} aria-live="off">{open ? `${seconds}s remaining` : 'Question closed'}</span></div><h2>{question.text}</h2><p className="muted">{question.points} points · Select one answer{player?.own_answer && open ? ' · Answer locked in' : ''}</p>
      {open && <div className="time-track"><div style={{ width: `${Math.min(100, Math.max(0, seconds / ((Date.parse(question.deadline) - Date.parse(question.started_at)) / 1000) * 100))}%` }} /></div>}
      <div className="answer-grid">{question.options.map((option, index) => {
        const selected = player?.own_answer?.option_id === option.id;
        const correct = !open && snapshot.closed_result?.correct_option_id === option.id;
        const count = snapshot.role === 'host' && !open ? snapshot.statistics?.counts[option.id] : undefined;
        return <button key={option.id} className={`answer ${selected ? 'chosen' : ''} ${correct ? 'correct' : ''}`} disabled={snapshot.role !== 'player' || !open || !connected || seconds === 0 || !!player?.own_answer || !!pending} onClick={() => onAnswer(option.id)}><span className="answer-letter">{String.fromCharCode(65 + index)}</span><span>{option.text}</span>{correct && <span className="option-note">✓ Correct</span>}{selected && <span className="option-note">{open ? '✓ Locked in' : 'Your answer'}</span>}{count !== undefined && <span className="option-note">{count} responses</span>}</button>;
      })}</div>
      {player?.own_answer && open && <div className="success-message" role="status">✓ Answer received. Your result will be revealed when the question closes.</div>}
      {pending === 'submit_answer' && <p className="muted" role="status">Confirming your answer…</p>}
      {player?.own_result && !open && <div className="result-banner"><strong>{player.own_result.correct ? 'Nicely done!' : player.own_result.answered ? 'A good try. Keep going!' : 'Time got away this round.'}</strong><span>+{player.own_result.awarded_points} points</span></div>}
      {open && seconds === 0 && <p className="muted" role="status">Time is up. Waiting for the server to publish results.</p>}
    </Panel>}
    {snapshot.state === 'FINISHED' && <Panel className="finished-banner"><span aria-hidden="true">☆</span><div><p className="eyebrow">THAT’S A WRAP</p><h2>Great game, everyone.</h2><p>Thanks for bringing your curiosity. Here’s how the room finished.</p></div></Panel>}
    {snapshot.state === 'EXPIRED' && <Panel><h2>This game has expired.</h2><p>The server has confirmed the session is no longer available.</p></Panel>}
    {snapshot.leaderboard.length > 0 && <Panel><div className="panel-title"><h2>{snapshot.state === 'FINISHED' ? 'Final leaderboard' : 'Leaderboard'}</h2><Badge>Published scores</Badge></div><ol className="leaderboard">{snapshot.leaderboard.map(rank => <li key={rank.player_id}><span className={`rank rank-${rank.rank}`}>{rank.rank}</span><span className="leader-name">{rank.player_name}{rank.left && <small> · Left game</small>}</span><strong>{rank.score.toLocaleString()} <small>pts</small></strong></li>)}</ol></Panel>}
  </div>;
}
