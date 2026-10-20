import { fireEvent, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router';
import { afterEach, describe, expect, it, vi } from 'vitest';

import PlansPage from '@/pages/plans/PlansPage';
import { HttpUtil, Msg } from '@/utils';
import { renderWithProviders } from './test-utils';

// The top bar has its own tests and needs settings the page itself does not.
vi.mock('@/layouts/AppNav', () => ({ default: () => null }));

const plan = (
  id: number,
  name: string,
  templateId: number,
  memberCount: number,
  inboundIds: number[],
) => ({
  id,
  name,
  limitIp: 3,
  remark: '',
  templateId,
  inboundIds,
  memberCount,
  sortIndex: id,
  createdAt: 0,
  updatedAt: 0,
});

const PLANS = [
  plan(1, 'Starter 125G', 5, 1, [7, 8]),
  { ...plan(2, 'Duo 150G', 0, 4, [7]), termMonths: [3, 12] },
];
const TEMPLATES = [
  {
    id: 3,
    name: 'relay_v3',
    isDefault: true,
    kind: 'yaml',
    size: 10,
    planCount: 1,
    updatedAt: 0,
    baseId: 0,
    changes: [],
  },
  {
    id: 5,
    name: 'alpha_v3',
    isDefault: false,
    kind: 'yaml',
    size: 10,
    planCount: 1,
    updatedAt: 0,
    baseId: 0,
    changes: [],
  },
];

// The setup file stubs HttpUtil for every test; its GET stub is put back afterwards.
const getStub = vi.mocked(HttpUtil.get);
const setupGet = getStub.getMockImplementation();

afterEach(() => {
  if (setupGet) getStub.mockImplementation(setupGet);
});

function renderPage(templates = TEMPLATES) {
  getStub.mockImplementation(async (url: string) => {
    if (url === '/panel/api/plans/list') return new Msg(true, '', PLANS);
    if (url === '/panel/api/ruleTemplates/list') return new Msg(true, '', templates);
    return new Msg(true, '', []);
  });
  renderWithProviders(
    <MemoryRouter initialEntries={['/plans']}>
      <PlansPage />
    </MemoryRouter>,
  );
}

function cardOf(name: string) {
  return screen.getByText(name).closest('.plan-card') as HTMLElement;
}

describe('PlansPage', () => {
  it("shows each card's template, the default one starred, and its counts", async () => {
    renderPage();
    await screen.findByText('Starter 125G');
    await within(cardOf('Starter 125G')).findByText('alpha_v3');
    expect(within(cardOf('Starter 125G')).getByText('1 user(s)')).toBeTruthy();
    expect(within(cardOf('Starter 125G')).getByText('2')).toBeTruthy();

    // A plan naming no template gets the default one.
    const fallback = cardOf('Duo 150G');
    expect(within(fallback).getByText('relay_v3')).toBeTruthy();
    expect(within(fallback).getByRole('img', { name: 'Default template' })).toBeTruthy();
  });

  // Each user has their own quota, validity and reset, so a card shows none of them.
  it('shows what a plan holds: its template, IP limit, servers and users', async () => {
    renderPage();
    await screen.findByText('Starter 125G');
    const labels = Array.from(cardOf('Starter 125G').querySelectorAll('.plan-row-label')).map(
      (el) => el.textContent,
    );
    expect(labels).toEqual(['Rule template', 'IP Limit', '计费周期', 'Nodes', 'Users']);
    expect(
      within(cardOf('Starter 125G')).getByText('3', { selector: '.plan-row-value' }),
    ).toBeTruthy();
    expect(within(cardOf('Starter 125G')).getByText('不限')).toBeTruthy();
    expect(within(cardOf('Duo 150G')).getByText('季付 · 年付')).toBeTruthy();
  });

  it('says so when a plan names no template and none is the default', async () => {
    renderPage(TEMPLATES.map((tpl) => ({ ...tpl, isDefault: false })));
    await screen.findByText('Duo 150G');
    await within(cardOf('Duo 150G')).findByText('None');
  });

  it('switches to a list of the same plans', async () => {
    renderPage();
    await screen.findByText('Starter 125G');
    fireEvent.click(screen.getByTitle('List'));
    const table = await screen.findByRole('table');
    expect(within(table).getByText('Starter 125G')).toBeTruthy();
    expect(within(table).getByText('Duo 150G')).toBeTruthy();
  });
});
