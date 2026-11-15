// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { PlayerSnapshot } from '../../shared/protocol';
import { PlayerGameView } from './PlayerGameView';

const question = {
  id: 'question-one', type: 'single_choice' as const, text: 'Which planet is known as the Red Planet?',
  index: 0, total: 3, points: 1000, started_at: '2026-11-15T10:00:00Z', deadline: '2026-11-15T10:00:20Z',
  options: ['Earth', 'Mars', 'Venus', 'Jupiter'].map((text, i) => ({ id: `option-${i}`, text })),
};
const snapshot: PlayerSnapshot = {
  role: 'player', state: 'QUESTION_OPEN', quiz_id: 'space', quiz_version: 'v1',
  server_time: question.started_at, expires_at: question.deadline, control_revision: 1,
  roster: [], leaderboard: [], own_score: 500, question,
};
function show(overrides: Partial<PlayerSnapshot> = {}, props = {}) {
  const onAnswer = vi.fn();
  return { onAnswer, ...render(<PlayerGameView snapshot={{ ...snapshot, ...overrides }} playerId="me" playerName="Ada" seconds={15}
    connected pending={null} onAnswer={onAnswer} {...props} />) };
}
afterEach(cleanup);

describe('player game screens', () => {
  it.each([15, 0])('uses a compact seconds label at %i seconds while retaining its accessible description', seconds => {
    show({}, { seconds });
    expect(screen.getByLabelText(`${seconds} seconds remaining`).textContent).toBe(`${seconds}s`);
  });

  it.each([2, 4, 8])('renders every one of %i choices and submits its server ID', count => {
    const options = Array.from({ length: count }, (_, i) => ({ id: `server-${i}`, text: `Choice ${i}` }));
    const { onAnswer } = show({ question: { ...question, options } });
    const buttons = screen.getAllByRole('button');
    expect(buttons).toHaveLength(count);
    fireEvent.click(buttons[count - 1]);
    expect(onAnswer).toHaveBeenCalledWith(`server-${count - 1}`);
    expect(screen.queryByText('Correct answer')).toBeNull();
  });

  it.each([
    { pending: 'submit_answer', text: 'Confirming your answer...' },
    { connected: false, text: 'Connection interrupted. Answers are paused while we recover.' },
    { seconds: 0, text: 'Time is up. Waiting for the server to publish results.' },
  ])('disables answers while $text', ({ text, ...props }) => {
    const { onAnswer } = show({}, props);
    expect(screen.getByRole('status').textContent).toBe(text);
    for (const button of screen.getAllByRole('button')) {
      expect((button as HTMLButtonElement).disabled).toBe(true);
      fireEvent.click(button);
    }
    expect(onAnswer).not.toHaveBeenCalled();
  });

  it('locks the accepted answer without revealing correctness', () => {
    show({ own_answer: { accepted: true, question_id: question.id, option_id: 'option-1', accepted_at: question.started_at } });
    expect(screen.getByRole('button', { name: /Mars Locked in/ }).classList.contains('is-selected')).toBe(true);
    expect(screen.queryByText('Correct answer')).toBeNull();
    expect(screen.getAllByRole('button').every(button => (button as HTMLButtonElement).disabled)).toBe(true);
  });

  it.each([
    { answered: true, correct: true, awarded_points: 900, heading: 'You got it!' },
    { answered: true, correct: false, awarded_points: 0, heading: 'Not this time.' },
    { answered: false, correct: false, awarded_points: 0, heading: 'No answer this round.' },
  ])('shows authoritative feedback: $heading', ({ heading, ...result }) => {
    show({ state: 'QUESTION_CLOSED', closed_result: { question_id: question.id, correct_option_id: 'option-1', closed_at: question.deadline },
      own_result: { question_id: question.id, correct_option_id: 'option-1', ...result } });
    expect(screen.getByRole('heading', { level: 1 }).textContent).toBe(heading);
    expect(screen.getByText(`+${result.awarded_points} points`)).toBeTruthy();
    expect(screen.getByText('Mars')).toBeTruthy();
    expect(screen.queryByRole('button')).toBeNull();
  });

  it('does not fabricate a result when personal feedback is absent', () => {
    show({ state: 'QUESTION_CLOSED' });
    expect(screen.getByRole('heading', { level: 1 }).textContent).toBe('Question closed');
    expect(screen.queryByText(/\+\d+ points/)).toBeNull();
  });

  it('prioritizes final results over the retained question and preserves tied ranks and identity', () => {
    const leaderboard = [
      { player_id: 'other', player_name: 'Ada', score: 500, rank: 1, left: true },
      { player_id: 'me', player_name: 'Ada', score: 500, rank: 1, left: false },
    ];
    show({ state: 'FINISHED', leaderboard });
    expect(screen.queryByText(question.text)).toBeNull();
    expect(screen.queryByRole('button')).toBeNull();
    expect(screen.getByText('You finished #1')).toBeTruthy();
    const board = within(screen.getByRole('region', { name: 'Final leaderboard' }));
    expect(board.getAllByLabelText('Rank 1')).toHaveLength(2);
    expect(board.getByText('(You)').closest('li')?.className).toBe('is-you');
    expect(board.getByText('(Left game)').closest('li')?.className).not.toBe('is-you');
  });

  it('handles final results without published scores, rank, or podium', () => {
    show({ state: 'FINISHED', own_score: undefined });
    expect(screen.getByText('Your score has not been published.')).toBeTruthy();
    expect(screen.getByText('No standings have been published.')).toBeTruthy();
    expect(screen.queryByText(/You finished/)).toBeNull();
    expect(screen.queryByRole('list', { name: 'Top finishers' })).toBeNull();
  });

  it('shows only current lobby participants and their connection state', () => {
    show({ state: 'LOBBY', question: undefined, roster: [
      { player_id: 'me', player_name: 'Ada', connected: true, left: false },
      { player_id: 'away', player_name: 'Grace', connected: false, left: false },
      { player_id: 'left', player_name: 'Alan', connected: false, left: true },
    ] });
    expect(screen.getByRole('heading', { name: "You're in, Ada!" })).toBeTruthy();
    expect(screen.queryByText('Alan')).toBeNull();
    expect(screen.getByLabelText('Away')).toBeTruthy();
  });
});
