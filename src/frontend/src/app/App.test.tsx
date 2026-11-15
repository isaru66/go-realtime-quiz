// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import App from './App';
import { useGame } from '../shared/useGame';
import { loadIdentity, prepareJoin, randomToken } from '../shared/identity';
import type { HostSnapshot } from '../shared/protocol';

vi.mock('../shared/useGame', () => ({ useGame: vi.fn() }));
const mockedGame = vi.mocked(useGame);
const command = vi.fn();
const recover = vi.fn();
const time = '2026-11-15T10:00:00Z';
const host: HostSnapshot = {
  role: 'host', state: 'LOBBY', quiz_id: 'quiz', quiz_version: 'version', server_time: time, expires_at: time,
  control_revision: 2, roster: [], leaderboard: [],
};
const catalog = { catalog_version: 'version', quizzes: [{ id: 'quiz', title: 'Space quiz', description: 'Explore space', question_count: 3 }] };
const room = { session_id: 'c0d29bf6-941e-4e1f-bd0e-b18456676ab6', game_pin: '123456', quiz_id: 'quiz',
  quiz_version: 'version', host_capability: 'a'.repeat(42) + 'A', expires_at: time, control_revision: 0 };
function route(hash: string) {
  act(() => { location.hash = hash; window.dispatchEvent(new HashChangeEvent('hashchange')); });
}
beforeEach(() => {
  for (const name of ['localStorage', 'sessionStorage']) {
    const items = new Map<string, string>();
    vi.stubGlobal(name, {
      getItem: (key: string) => items.get(key) ?? null,
      setItem: (key: string, value: string) => { items.set(key, value); },
      removeItem: (key: string) => { items.delete(key); },
      clear: () => items.clear(),
      key: (index: number) => [...items.keys()][index] ?? null,
      get length() { return items.size; },
    } satisfies Storage);
  }
  history.replaceState(null, '', '/?pin=123456');
  command.mockReset(); recover.mockReset();
  mockedGame.mockReturnValue({ snapshot: null, connection: 'offline', error: '', pending: null, command, recover });
  vi.stubGlobal('fetch', vi.fn(async (url: string) => {
    const data = url.endsWith('/quizzes') ? catalog : url.endsWith('/auth/session') ? { csrf_token: randomToken(), expires_at: time }
      : url.endsWith('/games') ? room : { pod_id: 'pod', active_version: 'version', validation_status: 'valid',
        last_attempt_at: time, errors: [], quizzes: catalog.quizzes };
    return { ok: true, json: async () => data };
  }));
  Object.defineProperty(navigator, 'locks', { configurable: true, value: { request: (_: string, callback: () => unknown) => Promise.resolve(callback()) } });
});
afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks(); });

describe('separate player and workspace shells', () => {
  it('joins using the prefilled PIN and stored identity without workspace chrome', async () => {
    render(<App />);
    expect(screen.queryByRole('navigation')).toBeNull();
    expect((screen.getByLabelText('Game PIN') as HTMLInputElement).value).toBe('123456');
    fireEvent.change(screen.getByLabelText('Your display name'), { target: { value: 'Ada' } });
    fireEvent.click(screen.getByRole('button', { name: /Join game/ }));
    await screen.findByRole('heading', { name: 'Joining your game' });
    expect(loadIdentity(localStorage)).toMatchObject({ name: 'Ada', pin: '123456' });
    expect(screen.queryByRole('heading', { name: /You're in/ })).toBeNull();
  });

  it('retains recovery controls and never renders a previous host snapshot to a player', () => {
    prepareJoin(localStorage, loadIdentity(localStorage), 'Ada', '123456');
    mockedGame.mockReturnValue({ snapshot: host, connection: 'paused', error: '', pending: null, command, recover });
    render(<App />);
    expect(screen.queryByText('Game controls')).toBeNull();
    expect(screen.getByRole('heading', { name: 'Joining your game' })).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Recover in this tab' }));
    expect(recover).toHaveBeenCalledOnce();
    expect(loadIdentity(localStorage).pin).toBe('123456');
  });

  it('keeps expiry and corrupt-storage errors visible on the join screen', () => {
    localStorage.setItem('quiz.player.v1', 'not json');
    mockedGame.mockReturnValue({ snapshot: null, connection: 'offline', error: 'The server confirmed that this game has expired.', pending: null, command, recover });
    render(<App />);
    expect(screen.getAllByRole('alert')).toHaveLength(2);
    expect((screen.getByRole('button', { name: /Join game/ }) as HTMLButtonElement).disabled).toBe(true);
    expect(localStorage.getItem('quiz.player.v1')).toBe('not json');
  });

  it('preserves host sign-in, game creation, invitations, controls, admin refresh, and sign-out', async () => {
    location.hash = '#host';
    const rendered = render(<App />);
    expect(screen.getByRole('navigation', { name: 'Main navigation' })).toBeTruthy();
    fireEvent.change(screen.getByLabelText('Administrator credential'), { target: { value: 'test-only' } });
    fireEvent.click(screen.getByRole('button', { name: /Sign in securely/ }));
    fireEvent.click(await screen.findByRole('radio'));
    fireEvent.click(screen.getByRole('button', { name: /Create game/ }));
    await screen.findByRole('heading', { name: 'Scan. Join. Play.' });
    expect(screen.getByRole('link', { name: /\\?pin=123456/ }).getAttribute('href')).toContain('?pin=123456');
    mockedGame.mockReturnValue({ snapshot: host, connection: 'connected', error: '', pending: null, command, recover });
    rendered.rerender(<App />);
    fireEvent.click(screen.getByRole('button', { name: /Start game/ }));
    expect(command).toHaveBeenCalledWith({ event_type: 'start_game', payload: { expected_control_revision: 2 } });
    mockedGame.mockReturnValue({ snapshot: { ...host, state: 'QUESTION_CLOSED' }, connection: 'connected', error: '', pending: null, command, recover });
    rendered.rerender(<App />);
    fireEvent.click(screen.getByRole('button', { name: /Next question/ }));
    expect(command).toHaveBeenCalledWith({ event_type: 'next_question', payload: { expected_control_revision: 2 } });
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    fireEvent.click(screen.getByRole('button', { name: 'End game' }));
    expect(command).toHaveBeenCalledWith({ event_type: 'end_game', payload: { expected_control_revision: 2 } });
    route('#admin');
    await screen.findByText('Healthy catalog');
    fireEvent.click(screen.getByRole('button', { name: 'Refresh status' }));
    await waitFor(() => expect((screen.getByRole('button', { name: 'Refresh status' }) as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(screen.getByRole('button', { name: 'Sign out' }));
    await screen.findByLabelText('Administrator credential');
    route('');
    expect(screen.queryByRole('navigation')).toBeNull();
    expect(screen.getByRole('heading', { name: /Big questions/ })).toBeTruthy();
  });
});
