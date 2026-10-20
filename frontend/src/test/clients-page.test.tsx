import { fireEvent, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import ClientsPage from '@/pages/clients/ClientsPage';
import { ClipboardManager, HttpUtil, IntlUtil, Msg } from '@/utils';
import { renderWithProviders } from './test-utils';

// The top bar and the live socket have their own tests and need a running panel.
vi.mock('@/layouts/AppNav', () => ({ default: () => null }));
vi.mock('@/hooks/useWebSocket', () => ({ useWebSocket: () => {} }));

const GB = 1024 ** 3;
const DAY = 86_400_000;
const NEXT_RESET = Date.UTC(2030, 0, 22);

function clients(now: number) {
  return [
    {
      email: 'a@x',
      planId: 7,
      enable: true,
      totalGB: 10 * GB,
      expiryTime: 0,
      subId: 'sub-a',
      comment: 'vip',
      nextReset: NEXT_RESET,
      traffic: { up: GB, down: GB },
    },
    {
      email: 'b@x',
      planId: 7,
      enable: false,
      totalGB: 10 * GB,
      expiryTime: 0,
      subId: 'sub-b',
      traffic: { up: 4 * GB, down: 6 * GB },
    },
    { email: 'c@x', enable: true, totalGB: 0, expiryTime: now - 2 * DAY, subId: '' },
    {
      email: 'd@x',
      planId: 7,
      enable: false,
      totalGB: 10 * GB,
      expiryTime: now + 3 * DAY - 3_600_000,
      subId: 'sub-d',
    },
  ];
}

const SUMMARY = {
  total: 4,
  active: 1,
  onlineCount: 0,
  depletedCount: 2,
  expiringCount: 1,
  deactiveCount: 1,
  exhaustedCount: 1,
  expiredCount: 1,
  online: [],
  depleted: [],
  expiring: [],
  deactive: [],
};

const PLAN = {
  id: 7,
  name: 'Gold',
  totalGB: 10 * GB,
  durationDays: 30,
  trafficReset: 'monthly',
  trafficResetDay: 22,
  limitIp: 0,
  remark: '',
  templateId: 0,
  inboundIds: [],
  memberCount: 3,
  sortIndex: 0,
  createdAt: 0,
  updatedAt: 0,
};

// The setup file stubs HttpUtil for every test; both stubs are put back afterwards.
const getStub = vi.mocked(HttpUtil.get);
const postStub = vi.mocked(HttpUtil.post);
const setupGet = getStub.getMockImplementation();
const setupPost = postStub.getMockImplementation();

afterEach(() => {
  if (setupGet) getStub.mockImplementation(setupGet);
  if (setupPost) postStub.mockImplementation(setupPost);
  localStorage.clear();
});

function renderPage() {
  const now = Date.now();
  const listUrls: string[] = [];
  getStub.mockImplementation(async (url: string) => {
    if (url.startsWith('/panel/api/clients/list/paged')) {
      listUrls.push(url);
      return new Msg(true, '', {
        items: clients(now),
        total: 4,
        filtered: 4,
        page: 1,
        pageSize: 25,
        summary: SUMMARY,
      });
    }
    if (url === '/panel/api/plans/list') return new Msg(true, '', [PLAN]);
    return new Msg(true, '', []);
  });
  postStub.mockImplementation(async (url: string) => {
    if (url === '/panel/api/setting/defaultSettings') {
      return new Msg(true, '', { pageSize: 25, subEnable: true, subURI: 'https://sub.example/s/' });
    }
    return new Msg(true, '', []);
  });
  renderWithProviders(
    <MemoryRouter initialEntries={['/clients']}>
      <ClientsPage />
    </MemoryRouter>,
  );
  return listUrls;
}

function rowOf(email: string) {
  return screen.getByText(email).closest('tr') as HTMLElement;
}

describe('ClientsPage (用户管理)', () => {
  it('filters by a plan chip and by a status chip, each showing its count', async () => {
    const listUrls = renderPage();
    await screen.findByText('a@x');

    fireEvent.click(screen.getByRole('button', { name: 'Gold (3)' }));
    await waitFor(() => expect(listUrls.at(-1)).toContain('plan=7'));

    fireEvent.click(screen.getByRole('button', { name: /Out of traffic \(1\)/ }));
    await waitFor(() => expect(listUrls.at(-1)).toContain('filter=exhausted'));
    expect(screen.getByRole('button', { name: 'No plan (1)' })).toBeTruthy();
  });

  it('says why a client is off rather than only that it is', async () => {
    renderPage();
    await screen.findByText('a@x');
    expect(within(rowOf('a@x')).getByText('Enabled')).toBeTruthy();
    expect(within(rowOf('b@x')).getByText('Used up')).toBeTruthy();
    expect(within(rowOf('c@x')).getByText('Expired')).toBeTruthy();
    expect(within(rowOf('d@x')).getByText('Disabled')).toBeTruthy();
    expect(within(rowOf('a@x')).getByText('20%')).toBeTruthy();
  });

  it('saves a remark in place and copies the subscription link', async () => {
    const copy = vi.spyOn(ClipboardManager, 'copyText').mockResolvedValue(true);
    renderPage();
    await screen.findByText('a@x');

    fireEvent.click(within(rowOf('a@x')).getByRole('button', { name: 'Edit comment' }));
    const input = within(rowOf('a@x')).getByRole('textbox', { name: 'Edit comment' });
    fireEvent.change(input, { target: { value: 'pays monthly' } });
    fireEvent.keyDown(input, { key: 'Enter', code: 'Enter', keyCode: 13 });
    await waitFor(() =>
      expect(postStub).toHaveBeenCalledWith(
        '/panel/api/clients/a%40x/comment',
        { comment: 'pays monthly' },
        expect.anything(),
      ),
    );

    fireEvent.click(within(rowOf('a@x')).getByRole('button', { name: /Copy link/ }));
    await waitFor(() => expect(copy).toHaveBeenCalledWith('https://sub.example/s/sub-a'));
    copy.mockRestore();
    expect(within(rowOf('c@x')).queryByRole('button', { name: /Copy link/ })).toBeNull();
  });

  it('lists expiry, days left and the next reset in the renewal view, and renews one client', async () => {
    renderPage();
    await screen.findByText('a@x');
    fireEvent.click(screen.getByText('Renewal view'));

    await within(rowOf('d@x')).findByText('3 d');
    expect(within(rowOf('a@x')).getByText('no expiry')).toBeTruthy();
    expect(within(rowOf('a@x')).getByText(IntlUtil.formatDate(NEXT_RESET))).toBeTruthy();
    expect(within(rowOf('c@x')).getByText('Expired 2 d ago')).toBeTruthy();
    // Renewing is the user's own now, so one without a plan can be renewed too.
    const noPlanRenew = within(rowOf('c@x')).getByRole('button', { name: /Renew/ });
    expect(noPlanRenew.hasAttribute('disabled')).toBe(false);

    fireEvent.click(within(rowOf('a@x')).getByRole('button', { name: /Renew/ }));
    // Generated ids are all "test-id" here, so the dialog is found by its title.
    const dialog = (await screen.findByText('Renew a@x')).closest('.ant-modal') as HTMLElement;
    fireEvent.click(within(dialog).getByRole('button', { name: 'Renew' }));
    await waitFor(() =>
      expect(postStub).toHaveBeenCalledWith(
        '/panel/api/clients/renew',
        { emails: ['a@x'], months: 1, days: 0, resetUsage: true },
        expect.anything(),
      ),
    );
  });

  // 妙妙屋X keeps the table free of checkboxes until you ask for bulk actions.
  it('shows the selection column only in bulk mode', async () => {
    renderPage();
    await screen.findByText('a@x');
    expect(screen.queryAllByRole('checkbox')).toHaveLength(0);

    fireEvent.click(screen.getByRole('button', { name: 'Bulk actions' }));
    expect(screen.getAllByRole('checkbox').length).toBe(5);
    expect(screen.getByText('0 selected')).toBeTruthy();
  });
});
