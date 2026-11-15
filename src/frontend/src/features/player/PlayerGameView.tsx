import type { PlayerSnapshot, Rank } from '../../shared/protocol';

interface Props {
  snapshot: PlayerSnapshot;
  playerId: string;
  playerName: string;
  seconds: number;
  connected: boolean;
  pending: string | null;
  onAnswer: (optionId: string) => void;
}

function Standings({ ranks, playerId, final = false }: { ranks: Rank[]; playerId: string; final?: boolean }) {
  return <section className="play-standings" aria-label={final ? 'Final leaderboard' : 'Leaderboard'}>
    <h2>{final ? 'Final leaderboard' : 'Leaderboard'}</h2>
    {ranks.length ? <ol>{ranks.map(rank => <li key={rank.player_id} className={rank.player_id === playerId ? 'is-you' : ''}>
      <span className="play-rank" aria-label={`Rank ${rank.rank}`}>{rank.rank}</span>
      <span className="play-standing-name">{rank.player_name}{rank.player_id === playerId && <small> (You)</small>}{rank.left && <small> (Left game)</small>}</span>
      <strong>{rank.score.toLocaleString()} <small>pts</small></strong>
    </li>)}</ol> : <p>No standings have been published.</p>}
  </section>;
}

export function PlayerGameView({ snapshot, playerId, playerName, seconds, connected, pending, onAnswer }: Props) {
  const question = snapshot.question;
  const ownRank = snapshot.leaderboard.find(rank => rank.player_id === playerId);

  if (snapshot.state === 'FINISHED') return <div className="play-results">
    <section className="play-center play-finale">
      <p className="play-eyebrow">That's a wrap</p>
      <h1 tabIndex={-1}>Great game, {playerName || 'everyone'}!</h1>
      <p>You brought the answers. Here is how the room finished.</p>
      <div className="play-final-score">{snapshot.own_score !== undefined ? <><strong>{snapshot.own_score.toLocaleString()}</strong><span>points</span></> : <span>Your score has not been published.</span>}</div>
      {ownRank && <p className="play-personal-rank">You finished #{ownRank.rank}</p>}
      {snapshot.leaderboard.some(rank => rank.rank <= 3) && <ol className="play-podium" aria-label="Top finishers">
        {snapshot.leaderboard.filter(rank => rank.rank <= 3).map(rank => <li key={rank.player_id} className={`play-place-${rank.rank}`}>
          <span className="play-medal">#{rank.rank}</span>
          <strong>{rank.player_name}</strong>
          <span>{rank.score.toLocaleString()} pts{rank.left && ' (Left game)'}</span>
        </li>)}
      </ol>}
    </section>
    <Standings ranks={snapshot.leaderboard} playerId={playerId} final />
    <p className="play-next-note">Ready for another quiz? Use Game options to leave this game first.</p>
  </div>;

  if (snapshot.state === 'EXPIRED') return <section className="play-center">
    <h1 tabIndex={-1}>This game has expired</h1><p>The server confirmed this room is no longer available.</p>
  </section>;

  if (snapshot.state === 'LOBBY') {
    const players = snapshot.roster.filter(player => !player.left);
    return <section className="play-center play-lobby">
      <div className="play-lobby-mark" aria-hidden="true">&#10003;</div>
      <p className="play-eyebrow">You're in the right place</p>
      <h1 tabIndex={-1}>You're in, {playerName}!</h1>
      <p>Get ready. Your host will start the quiz.</p>
      <p className="play-player-count"><strong>{players.length}</strong> {players.length === 1 ? 'player' : 'players'} in the room</p>
      <ul className="play-roster" aria-label="Players in the room">{players.map(player => <li key={player.player_id}>
        <span>{player.player_name}</span><span className={player.connected ? 'play-present' : 'play-away'} aria-label={player.connected ? 'Connected' : 'Away'} />
      </li>)}</ul>
      <p className="play-next-note">Keep this page open. The next question will appear here.</p>
    </section>;
  }

  if (snapshot.state === 'QUESTION_CLOSED') {
    const result = snapshot.own_result?.question_id === question?.id ? snapshot.own_result : undefined;
    const correctAnswer = question?.options.find(option => option.id === snapshot.closed_result?.correct_option_id);
    return <div className="play-results">
      <section className="play-center play-round-result">
        <p className="play-eyebrow">Question {question ? question.index + 1 : ''} complete</p>
        <div className={`play-result-symbol ${result?.correct ? 'is-correct' : ''}`} aria-hidden="true">{result ? result.correct ? '\u2713' : result.answered ? '\u00d7' : '\u2014' : '?'}</div>
        <h1 tabIndex={-1}>{result ? result.correct ? 'You got it!' : result.answered ? 'Not this time.' : 'No answer this round.' : 'Question closed'}</h1>
        {result && <p className="play-awarded">+{result.awarded_points.toLocaleString()} points</p>}
        {correctAnswer && <div className="play-correct-answer"><span>Correct answer</span><strong>{correctAnswer.text}</strong></div>}
        {snapshot.own_score !== undefined && <p>Total score: <strong>{snapshot.own_score.toLocaleString()} pts</strong>{ownRank && <> &middot; Rank <strong>#{ownRank.rank}</strong></>}</p>}
        <p className="play-next-note">Waiting for the host to continue.</p>
      </section>
      <Standings ranks={snapshot.leaderboard} playerId={playerId} />
    </div>;
  }

  if (!question) return <section className="play-center"><h1 tabIndex={-1}>Waiting for the question</h1><p>The latest game state will appear here.</p></section>;

  const accepted = snapshot.own_answer?.question_id === question.id ? snapshot.own_answer : undefined;
  const duration = (Date.parse(question.deadline) - Date.parse(question.started_at)) / 1000;
  const progress = duration > 0 ? Math.min(100, Math.max(0, seconds / duration * 100)) : 0;
  const status = accepted ? 'Answer locked in. Your result appears when the question closes.'
    : pending === 'submit_answer' ? 'Confirming your answer...'
    : !connected ? 'Connection interrupted. Answers are paused while we recover.'
    : seconds === 0 ? 'Time is up. Waiting for the server to publish results.'
    : 'Choose one answer.';
  const contentHeavy = question.options.some(option => option.text.length > 100);

  return <section className="play-question">
    <div className="play-progress">
      <div className="play-question-meta">
        <span>Question <strong>{question.index + 1}</strong> / {question.total}</span>
        <span>{question.points.toLocaleString()} points</span>
        <span className="play-timer" aria-live="off" aria-label={`${seconds} seconds remaining`}>{seconds}s</span>
      </div>
      <div className="play-time-track" aria-hidden="true"><div style={{ width: `${progress}%` }} /></div>
    </div>
    <div className="play-prompt">
      <h1 tabIndex={-1}>{question.text}</h1>
      <p className="play-answer-status" role="status">{status}</p>
    </div>
    <div className="play-choices">
      <div className={`play-answer-grid ${contentHeavy ? 'is-content-heavy' : ''}`}>
        {question.options.map((option, index) => <button key={option.id}
          className={`play-answer play-color-${index % 4} ${accepted?.option_id === option.id ? 'is-selected' : ''}`}
          disabled={!connected || seconds === 0 || !!pending || !!accepted}
          onClick={() => onAnswer(option.id)}>
          <span className="play-answer-marker"><span className={`play-shape play-shape-${index % 4}`} aria-hidden="true" /><span>{String.fromCharCode(65 + index)}</span></span>
          <span className="play-answer-text">{option.text}</span>
          {accepted?.option_id === option.id && <span className="play-answer-lock">Locked in</span>}
        </button>)}
      </div>
    </div>
  </section>;
}
