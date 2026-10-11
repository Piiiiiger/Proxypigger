import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';

import { setupHttp } from '@/api/http-init';
import AbusePage from '@/pages/abuse/AbusePage';
import { HttpUtil, Msg } from '@/utils';
import { renderWithProviders } from './test-utils';

// A landmark stands in for the real top bar, so a page drawn without it shows.
vi.mock('@/layouts/AppNav', () => ({ default: () => <nav aria-label="面板导航" /> }));

// The handlers bind JSON only: the real serializer must run, so fetch is stubbed.
vi.mocked(HttpUtil.post).mockRestore();
const getStub = vi.mocked(HttpUtil.get);
const originalGet = getStub.getMockImplementation();
const fetchMock = vi.fn<typeof fetch>();

const nowSec = Math.floor(Date.now() / 1000);
const settings = {
  rules: {
    spamAttempts: 10,
    spamWindowMin: 10,
    btAttempts: 30,
    btWindowMin: 10,
    scanIps: 150,
    scanPortsOnIp: 50,
    scanSensitiveIps: 20,
    scanWindowMin: 5,
    floodPerDest: 1000,
    floodTotal: 3000,
    crawlerConns: 4000,
    crawlerHosts: 600,
    crawlerWindowMin: 10,
    crawlerWindows: 2,
    speedTestsPerHour: 5,
    speedTestsPerDay: 15,
    speedTestGapMin: 3,
    fullSpeedMbps: 100,
    fullSpeedWarnMin: 120,
    fullSpeedStrikeMin: 240,
    openaiAuthMinPerHour: 10,
    openaiAuthMinPerDay: 30,
    googleAuthMinPerHour: 30,
    googleAuthMinPerDay: 0,
    microsoftSignupMinPerHour: 10,
    microsoftSignupMinPerDay: 20,
  },
  actions: {
    spam: 'ban',
    bt: 'ban',
    scan: 'ban',
    flood: 'ban',
    crawler: 'ban',
    speedtest: 'ban',
    fullspeed: 'ban',
    register: 'record',
  },
  signup: { limit: 3, action: 'record' },
};
const overview = {
  servers: [
    { nodeId: 0, name: '面板本机', mode: 'off', capable: true },
    { nodeId: 3, name: 'de-fra-1', mode: 'observe', capable: false },
  ],
  settings,
  bans: [
    {
      id: 9,
      email: 'alice',
      kind: 'abuse',
      rule: 'scan',
      reason: '端口扫描：5 分钟内连接同一 IP 的 52 个端口',
      network: '',
      strike: 2,
      eventId: 4,
      bannedAt: nowSec - 60,
      expiresAt: nowSec + 1740,
      liftedAt: 0,
      forgiven: false,
    },
  ],
  events: [
    {
      id: 4,
      email: 'alice',
      nodeId: 3,
      rule: 'scan',
      level: 'strike',
      measure: 'ports',
      count: 52,
      limit: 50,
      window: 300,
      samples: '["198.51.100.7","198.51.100.8"]',
      action: 'banned',
      at: nowSec - 60,
      server: 'de-fra-1',
      label: '端口扫描',
      evidence: '5 分钟内连接同一 IP 的 52 个端口',
    },
  ],
};

beforeEach(() => {
  document.head.innerHTML = '<meta name="csrf-token" content="test-token">';
  setupHttp();
  fetchMock.mockReset();
  fetchMock.mockImplementation(
    async () =>
      new Response(JSON.stringify({ success: true, msg: '', obj: overview }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
  );
  vi.stubGlobal('fetch', fetchMock);
  getStub.mockImplementation(async (url: string) =>
    url === '/panel/api/abuse/overview' ? new Msg(true, '', overview) : new Msg(true, '', []),
  );
});

afterEach(() => {
  vi.unstubAllGlobals();
  if (originalGet) getStub.mockImplementation(originalGet);
});

async function renderPage() {
  renderWithProviders(<AbusePage />);
  await screen.findByLabelText('de-fra-1 的检测模式');
}

function lastPost() {
  const [url, init] = fetchMock.mock.calls.at(-1)!;
  expect(new Headers(init?.headers).get('Content-Type')).toBe('application/json');
  return { url, body: init?.body ? JSON.parse(String(init.body)) : undefined };
}

// antd's generated ids are all "test-id" here, so a select is opened from its own control.
function chooseIn(control: HTMLElement, optionText: string) {
  fireEvent.mouseDown(control.closest('.ant-select') as HTMLElement);
  const option = Array.from(
    document.querySelectorAll(
      '.ant-select-dropdown:not(.ant-select-dropdown-hidden) .ant-select-item-option',
    ),
  ).find((o) => (o.getAttribute('title') ?? o.textContent ?? '').trim() === optionText);
  if (!option) throw new Error(`no option ${optionText}`);
  fireEvent.click(option);
}

function rule(title: string): HTMLElement {
  return screen.getByRole('group', { name: title });
}

function saveButton(): HTMLButtonElement {
  return screen.getByRole('button', { name: '保存设置' }) as HTMLButtonElement;
}

it('sits in the panel shell under its own header, like every other page', async () => {
  await renderPage();
  expect(screen.getByRole('navigation', { name: '面板导航' })).toBeTruthy();
  expect(screen.getByRole('heading', { level: 1, name: '防滥用' })).toBeTruthy();
});

it('warns that a detecting server whose agent cannot detect has no effect yet', async () => {
  await renderPage();
  const row = screen.getByLabelText('de-fra-1 的检测模式').closest('tr') as HTMLElement;
  expect(within(row).getByText('节点 agent 未连接或需要更新')).toBeTruthy();
});

it('switches one server with a JSON request naming it', async () => {
  await renderPage();
  chooseIn(screen.getByLabelText('de-fra-1 的检测模式'), '封禁');
  await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
  expect(lastPost()).toEqual({
    url: '/panel/api/abuse/mode',
    body: { nodeId: 3, mode: 'enforce' },
  });
});

it('lifts a running ban', async () => {
  await renderPage();
  fireEvent.click(screen.getByRole('button', { name: /解\s*除/ }));
  await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
  expect(lastPost().url).toBe('/panel/api/abuse/lift/alice');
});

it('shows the destinations a hit stored as evidence', async () => {
  await renderPage();
  const row = screen.getByText('端口扫描', { selector: 'td' }).closest('tr') as HTMLElement;
  expect(within(row).getByText('198.51.100.7、198.51.100.8')).toBeTruthy();
});

it('saves what a rule does together with its thresholds', async () => {
  await renderPage();
  chooseIn(within(rule('爬虫')).getByLabelText('处理'), '提醒用户');
  fireEvent.change(within(rule('爬虫')).getByLabelText('不同网站数'), { target: { value: '700' } });
  chooseIn(within(rule('注册保护（用户页面）')).getByLabelText('处理'), '阻止注册 24 小时');
  fireEvent.click(saveButton());

  await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
  expect(lastPost()).toEqual({
    url: '/panel/api/abuse/settings',
    body: {
      rules: { ...settings.rules, crawlerHosts: 700 },
      actions: { ...settings.actions, crawler: 'warn' },
      signup: { limit: 3, action: 'ban' },
    },
  });
});

it('saves the bulk sign-up limit of each platform', async () => {
  await renderPage();
  const register = rule('批量注册（AI / Google / 微软账号）');
  fireEvent.change(within(register).getByLabelText('Google 每小时分钟数'), {
    target: { value: '40' },
  });
  chooseIn(within(register).getByLabelText('处理'), '提醒用户');
  fireEvent.click(saveButton());

  await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
  expect(lastPost()).toEqual({
    url: '/panel/api/abuse/settings',
    body: {
      rules: { ...settings.rules, googleAuthMinPerHour: 40 },
      actions: { ...settings.actions, register: 'warn' },
      signup: { limit: 3, action: 'record' },
    },
  });
});

it('offers saving only while a rule differs from what was saved, as the settings page does', async () => {
  await renderPage();
  expect(saveButton().disabled).toBe(true);
  fireEvent.change(within(rule('爬虫')).getByLabelText('不同网站数'), { target: { value: '700' } });
  expect(saveButton().disabled).toBe(false);
  fireEvent.click(saveButton());

  await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
  await waitFor(() => expect(saveButton().disabled).toBe(true));
  expect((within(rule('爬虫')).getByLabelText('不同网站数') as HTMLInputElement).value).toBe('700');
});

it('keeps a rule change to save again when saving fails', async () => {
  await renderPage();
  fetchMock.mockImplementation(
    async () => new Response(JSON.stringify({ success: false, msg: 'save unavailable' })),
  );
  fireEvent.change(within(rule('爬虫')).getByLabelText('不同网站数'), { target: { value: '700' } });
  fireEvent.click(saveButton());

  await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(1));
  await waitFor(() => expect(saveButton().querySelector('.ant-btn-loading-icon')).toBeNull());
  expect(saveButton().disabled).toBe(false);
  expect((within(rule('爬虫')).getByLabelText('不同网站数') as HTMLInputElement).value).toBe('700');
});
