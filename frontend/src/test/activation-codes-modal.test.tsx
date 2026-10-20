import { fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import ActivationCodesModal, { activationCodeExpiry } from '@/pages/plans/ActivationCodesModal';
import { HttpUtil, Msg } from '@/utils';
import { chooseSelectOption, listSelectOptions, renderWithProviders } from './test-utils';

const plan = {
  id: 2,
  name: 'Standard',
  limitIp: 0,
  remark: '',
  templateId: 0,
  inboundIds: [1],
  memberCount: 0,
  sortIndex: 0,
  createdAt: 0,
  updatedAt: 0,
};
const code = {
  id: 3,
  code: 'ABCD-EFGH-JKLM-NPQR',
  planId: 2,
  totalGB: 10 * 1024 ** 3,
  months: 1,
  days: 0,
  resetDay: 22,
  note: '',
  usedAt: 0,
  usedBy: '',
  createdAt: 0,
  expiresAt: 0,
};
const get = vi.mocked(HttpUtil.get);
const post = vi.mocked(HttpUtil.post);
const originalGet = get.getMockImplementation();
const originalPost = post.getMockImplementation();
afterEach(() => {
  if (originalGet) get.mockImplementation(originalGet);
  if (originalPost) post.mockImplementation(originalPost);
});

describe('activation codes', () => {
  // The server works out when a code lapses, calendar months included; the page counts down to it.
  it('counts down unused validity and expires exactly at its deadline', () => {
    const day = 86_400_000;
    const createdAt = Date.UTC(2026, 9, 4, 8);
    const grant = { expiresAt: createdAt + 12 * day };
    expect(activationCodeExpiry(grant, createdAt).remainingDays).toBe(12);
    expect(activationCodeExpiry(grant, createdAt + day).remainingDays).toBe(11);
    expect(activationCodeExpiry(grant, createdAt + 12 * day - 1).remainingDays).toBe(1);
    expect(activationCodeExpiry(grant, createdAt + 12 * day)).toMatchObject({
      remainingDays: 0,
      expired: true,
    });
    expect(activationCodeExpiry({ expiresAt: 0 }, createdAt + 365 * day).expired).toBe(false);
  });

  it('shows remaining days for an unused code', async () => {
    get.mockResolvedValue(
      new Msg(true, '', [{ ...code, expiresAt: Date.now() + 11 * 86_400_000 - 1000 }]),
    );
    renderWithProviders(<ActivationCodesModal plan={plan} onClose={() => {}} />);
    await waitFor(() => expect(screen.getByText(/11 days remaining/)).toBeTruthy());
    expect(screen.getByText(/Expires:/)).toBeTruthy();
  });

  it('marks an expired code and disables copying it as unused', async () => {
    get.mockResolvedValue(new Msg(true, '', [{ ...code, expiresAt: Date.now() - 86_400_000 }]));
    renderWithProviders(<ActivationCodesModal plan={plan} onClose={() => {}} />);
    await waitFor(() => expect(screen.getByText('Expired')).toBeTruthy());
    expect(screen.getByRole('button', { name: 'Copy unused codes' }).hasAttribute('disabled')).toBe(
      true,
    );
  });

  it('creates codes with a per-user quota and refreshes the list', async () => {
    get.mockResolvedValue(new Msg(true, '', []));
    post.mockImplementation(async () => {
      get.mockResolvedValue(new Msg(true, '', [code]));
      return new Msg(true, '', [code]);
    });
    renderWithProviders(<ActivationCodesModal plan={plan} onClose={() => {}} />);
    expect(screen.getByRole('dialog', { name: 'Activation codes · Standard' })).toBeTruthy();
    fireEvent.change(screen.getByLabelText('Traffic'), { target: { value: '10' } });
    fireEvent.change(screen.getByLabelText('Monthly reset day'), { target: { value: '22' } });
    fireEvent.click(screen.getByRole('button', { name: 'Create codes' }));
    await waitFor(() => expect(screen.getByText(code.code)).toBeTruthy());
    expect(post).toHaveBeenCalledWith(
      '/panel/api/plans/codes/add',
      { planId: 2, count: 1, totalGB: 10 * 1024 ** 3, months: 1, days: 0, resetDay: 22, note: '' },
      expect.anything(),
    );
  });

  // A plan sold by terms gets codes for one of its months only, the shortest offered first.
  it('offers only the terms of a plan sold by them', async () => {
    get.mockResolvedValue(new Msg(true, '', []));
    post.mockResolvedValue(new Msg(true, '', [code]));
    renderWithProviders(
      <ActivationCodesModal plan={{ ...plan, termMonths: [3, 12] }} onClose={() => {}} />,
    );
    const term = screen.getByLabelText('Validity').id;
    const shown = document.getElementById(term)?.closest('.ant-select');
    expect(shown?.querySelector('.ant-select-content')?.textContent).toBe('季付（3 个月）');
    expect(listSelectOptions(term)).toEqual(['季付（3 个月）', '年付（12 个月）']);
    chooseSelectOption(term, '年付（12 个月）');
    fireEvent.click(screen.getByRole('button', { name: 'Create codes' }));
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith(
        '/panel/api/plans/codes/add',
        expect.objectContaining({ planId: 2, months: 12, days: 0 }),
        expect.anything(),
      ),
    );
  });

  // Months stop at 1,200, so days typed first are brought within it when months are picked.
  it('keeps the validity within the months limit when switching to months', async () => {
    get.mockResolvedValue(new Msg(true, '', []));
    post.mockResolvedValue(new Msg(true, '', [code]));
    renderWithProviders(<ActivationCodesModal plan={plan} onClose={() => {}} />);
    chooseSelectOption('activation-code-unit', 'days');
    fireEvent.change(screen.getByLabelText('Validity'), { target: { value: '3650' } });
    chooseSelectOption('activation-code-unit', 'months');
    fireEvent.click(screen.getByRole('button', { name: 'Create codes' }));
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith(
        '/panel/api/plans/codes/add',
        expect.objectContaining({ months: 1200, days: 0 }),
        expect.anything(),
      ),
    );
  });

  // Without terms a code can still last plain days, as the odd-length ones always have.
  it('makes a code for days when days are picked', async () => {
    get.mockResolvedValue(new Msg(true, '', []));
    post.mockResolvedValue(new Msg(true, '', [code]));
    renderWithProviders(<ActivationCodesModal plan={plan} onClose={() => {}} />);
    fireEvent.change(screen.getByLabelText('Validity'), { target: { value: '20' } });
    chooseSelectOption('activation-code-unit', 'days');
    fireEvent.click(screen.getByRole('button', { name: 'Create codes' }));
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith(
        '/panel/api/plans/codes/add',
        expect.objectContaining({ planId: 2, months: 0, days: 20 }),
        expect.anything(),
      ),
    );
  });
});
