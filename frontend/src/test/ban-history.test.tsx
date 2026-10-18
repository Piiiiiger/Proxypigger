import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { setupHttp } from '@/api/http-init';
import { keys } from '@/api/queryKeys';
import { BanHistory } from '@/components/abuse/BanHistory';
import { ClientBanHistoryButton } from '@/components/abuse/ClientBanHistoryButton';
import type { AbuseHistory, BanRecord } from '@/generated/zod';
import PortalBans, { PortalBanBanner } from '@/pages/sub/portal/PortalBans';
import { HttpUtil, Msg } from '@/utils';
import { makeTestQueryClient, renderWithProviders } from './test-utils';

const NOW_MS = Date.UTC(2026, 9, 6, 4, 0, 0);
const NOW = NOW_MS / 1000;

function record(over: Partial<BanRecord>): BanRecord {
  return {
    id: 1,
    email: 'alice',
    kind: 'abuse',
    rule: 'scan',
    reason: '端口扫描：5 分钟内连接同一 IP 的 52 个端口',
    network: '',
    strike: 1,
    eventId: 7,
    bannedAt: NOW - 60,
    expiresAt: NOW + 29 * 60,
    liftedAt: 0,
    forgiven: false,
    ...over,
  };
}

const running = record({ id: 3, strike: 2 });
const banned: AbuseHistory = { status: { ban: running, strikes: 2, limit: 3 }, records: [running] };
const clean: AbuseHistory = { status: { ban: null, strikes: 0, limit: 3 }, records: [] };

describe('ban history', () => {
  it('says why a running ban happened and how long it has left', () => {
    render(<BanHistory history={banned} nowMs={NOW_MS} />);
    expect(screen.getByText('账号封禁中：端口扫描：5 分钟内连接同一 IP 的 52 个端口')).toBeTruthy();
    expect(screen.getAllByText(/^还剩 29 分钟/)).toHaveLength(2);
    expect(screen.getByText(/30 天内违规 2\/3 次/)).toBeTruthy();
  });

  it('says a lock lasts until an admin lifts it, without counting strikes', () => {
    const lock = record({ id: 4, strike: 4, expiresAt: 0 });
    render(
      <BanHistory
        history={{ status: { ban: lock, strikes: 4, limit: 3 }, records: [lock] }}
        nowMs={NOW_MS}
      />,
    );
    expect(screen.getByText('账号已停用：30 天内违规超过 3 次')).toBeTruthy();
    expect(screen.getByText('账号停用中，请联系管理员')).toBeTruthy();
    expect(screen.queryByText(/再违规一次/)).toBeNull();
  });

  it('tells IP-limit bans from strikes and says how each ended', () => {
    const records = [
      record({ id: 3, liftedAt: NOW - 30 }),
      record({
        id: 2,
        kind: 'iplimit',
        rule: 'iplimit',
        strike: 0,
        reason: '同时在线 IP 超过上限（3 个），暂停 198.51.100.9',
        bannedAt: NOW - 86400,
        expiresAt: NOW - 86400 + 1800,
      }),
      record({
        id: 1,
        forgiven: true,
        bannedAt: NOW - 3 * 86400,
        expiresAt: NOW - 3 * 86400 + 1800,
      }),
    ];
    render(<BanHistory history={{ status: clean.status, records }} nowMs={NOW_MS} />);
    expect(screen.getByText('已由管理员解除')).toBeTruthy();
    expect(screen.getByText('IP 超限')).toBeTruthy();
    expect(screen.getAllByText('封禁 30 分钟，已恢复')).toHaveLength(2);
    expect(screen.getByText('已清除')).toBeTruthy();
  });

  it('offers the admin only the actions that apply, and the user page none', () => {
    const { unmount } = render(
      <BanHistory history={banned} nowMs={NOW_MS} onLift={vi.fn()} onForgive={vi.fn()} />,
    );
    expect(screen.getByRole('button', { name: '解除封禁' })).toBeTruthy();
    expect(screen.getByRole('button', { name: '清除违规次数' })).toBeTruthy();
    unmount();

    const { unmount: unmountClean } = render(
      <BanHistory history={clean} nowMs={NOW_MS} onLift={vi.fn()} onForgive={vi.fn()} />,
    );
    expect(screen.queryByRole('button')).toBeNull();
    unmountClean();

    render(<BanHistory history={banned} nowMs={NOW_MS} />);
    expect(screen.queryByRole('button')).toBeNull();
  });
});

describe("the admin's ban history", () => {
  // The handlers bind JSON only: the real serializer must run, so fetch is stubbed.
  const getStub = vi.mocked(HttpUtil.get);
  const originalGet = getStub.getMockImplementation();
  const originalPost = vi.mocked(HttpUtil.post).getMockImplementation();
  const fetchMock = vi.fn<typeof fetch>();

  beforeEach(() => {
    vi.mocked(HttpUtil.post).mockRestore();
    document.head.innerHTML = '<meta name="csrf-token" content="test-token">';
    setupHttp();
    fetchMock.mockReset();
    fetchMock.mockImplementation(
      async () =>
        new Response(JSON.stringify({ success: true, msg: '', obj: clean }), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        }),
    );
    vi.stubGlobal('fetch', fetchMock);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    if (originalGet) getStub.mockImplementation(originalGet);
    if (originalPost) vi.spyOn(HttpUtil, 'post').mockImplementation(originalPost);
  });

  it('reads the history when opened and shows the ban gone once lifted', async () => {
    let lifted = false;
    getStub.mockImplementation(async (url: string) =>
      url === '/panel/api/abuse/history/alice'
        ? new Msg(true, '', lifted ? clean : banned)
        : new Msg(true, '', []),
    );
    renderWithProviders(<ClientBanHistoryButton email="alice" />);
    expect(getStub).not.toHaveBeenCalledWith('/panel/api/abuse/history/alice', undefined, {
      silent: true,
    });

    fireEvent.click(screen.getByRole('button', { name: /查看/ }));
    await screen.findByText(/^账号封禁中/);
    lifted = true;
    fireEvent.click(screen.getByRole('button', { name: '解除封禁' }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
    expect(fetchMock.mock.calls[0][0]).toBe('/panel/api/abuse/lift/alice');
    await screen.findByText('最近 90 天没有封禁');
    expect(screen.queryByText(/^账号封禁中/)).toBeNull();
  });
});

describe("the user page's ban history", () => {
  afterEach(() => vi.unstubAllGlobals());

  it('shows the persisted first violation and acknowledges only after the user clicks', async () => {
    let acknowledged = false;
    const request = vi.fn(async (url: string, options?: RequestInit) => {
      if (url.endsWith('/ai-patrol/seen')) {
        expect(options?.method).toBe('POST');
        expect(JSON.parse(String(options?.body))).toEqual({ ids: [7] });
        acknowledged = true;
        return new Response(null, { status: 204 });
      }
      return new Response(
        JSON.stringify({
          ...clean,
          aiPatrolNotices: [
            {
              id: 7,
              at: NOW,
              action: 'ai_violation',
              reason: 'confirmed scan',
              portalSeenAt: acknowledged ? NOW : 0,
              notifiedAt: 0,
              forgiven: false,
            },
          ],
        }),
        { status: 200 },
      );
    });
    vi.stubGlobal('fetch', request);
    renderWithProviders(<PortalBanBanner base="/x/portal" email="alice" />);
    await screen.findByText('Codex 巡查违规通知');
    expect(screen.getByText(/本次不封禁.*请联系管理员了解详情/)).toBeTruthy();
    expect(request).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole('button', { name: '我已知晓' }));
    await waitFor(() => expect(screen.queryByText('Codex 巡查违规通知')).toBeNull());
    expect(acknowledged).toBe(true);
  });

  it("lists the signed-in person's own bans, leaving the running one to the banner", async () => {
    const request = vi
      .fn()
      .mockResolvedValue(new Response(JSON.stringify(banned), { status: 200 }));
    vi.stubGlobal('fetch', request);
    renderWithProviders(<PortalBans base="/x/portal" email="alice" onSessionEnded={vi.fn()} />);
    await screen.findByText('封禁记录');
    expect(request.mock.calls[0][0]).toBe('/x/portal/bans');
    expect(screen.getByText('端口扫描：5 分钟内连接同一 IP 的 52 个端口')).toBeTruthy();
    expect(screen.queryByText(/^账号封禁中/)).toBeNull();
  });

  it('puts a running ban at the top of the page, and nothing when there is none', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(new Response(JSON.stringify(banned), { status: 200 })),
    );
    const { unmount } = renderWithProviders(<PortalBanBanner base="/x/portal" email="alice" />);
    await screen.findByText('账号封禁中：端口扫描：5 分钟内连接同一 IP 的 52 个端口');
    unmount();

    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(new Response(JSON.stringify(clean), { status: 200 })),
    );
    const queryClient = makeTestQueryClient();
    const { container } = renderWithProviders(<PortalBanBanner base="/x/portal" email="bob" />, {
      queryClient,
    });
    await waitFor(() =>
      expect(queryClient.getQueryState(keys.portal.bans('/x/portal', 'bob'))?.status).toBe(
        'success',
      ),
    );
    expect(container.textContent).toBe('');
  });

  it('hands an ended session back to sign-in', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response('{}', { status: 401 })));
    const ended = vi.fn();
    renderWithProviders(<PortalBans base="/x/portal" email="alice" onSessionEnded={ended} />);
    await waitFor(() => expect(ended).toHaveBeenCalledOnce());
  });
});
