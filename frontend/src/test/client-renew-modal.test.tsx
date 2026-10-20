import { useState } from 'react';
import { fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';

import ClientRenewModal from '@/pages/clients/ClientRenewModal';
import { HttpUtil, Msg } from '@/utils';
import { chooseSelectOption, renderWithProviders } from './test-utils';

const postStub = vi.mocked(HttpUtil.post);
const setupPost = postStub.getMockImplementation();

afterEach(() => {
  if (setupPost) postStub.mockImplementation(setupPost);
});

function renderModal(emails: string[]) {
  postStub.mockResolvedValue(new Msg(true, '', { affected: emails.length }));
  const onClose = vi.fn();
  renderWithProviders(<ClientRenewModal open emails={emails} onClose={onClose} />);
  return onClose;
}

function unitText(): string | null | undefined {
  const select = document.getElementById('client-renew-unit')?.closest('.ant-select');
  return select?.querySelector('.ant-select-content')?.textContent;
}

describe('ClientRenewModal', () => {
  // Renewing is per user: a calendar month on top by default, with the usage cleared.
  it('renews by one calendar month and clears the usage unless told otherwise', async () => {
    const onClose = renderModal(['alice', 'bob']);
    expect(screen.getByRole('dialog', { name: 'Renew 2 user(s)' })).toBeTruthy();
    expect(unitText()).toBe('months');
    fireEvent.click(screen.getByRole('button', { name: 'Renew' }));

    await waitFor(() =>
      expect(postStub).toHaveBeenCalledWith(
        '/panel/api/clients/renew',
        { emails: ['alice', 'bob'], months: 1, days: 0, resetUsage: true },
        expect.anything(),
      ),
    );
    await waitFor(() => expect(onClose).toHaveBeenCalled());
  });

  it('renews by the days chosen, keeping the usage when unticked', async () => {
    renderModal(['alice']);
    expect(screen.getByText('Renew alice')).toBeTruthy();
    fireEvent.change(screen.getByLabelText('Renew by'), { target: { value: '7' } });
    chooseSelectOption('client-renew-unit', 'days');
    fireEvent.click(screen.getByRole('checkbox', { name: 'Clear used traffic' }));
    fireEvent.click(screen.getByRole('button', { name: 'Renew' }));

    await waitFor(() =>
      expect(postStub).toHaveBeenCalledWith(
        '/panel/api/clients/renew',
        { emails: ['alice'], months: 0, days: 7, resetUsage: false },
        expect.anything(),
      ),
    );
  });

  // Months stop at 1,200, so a day count typed first must not reach the server as months.
  it('keeps the amount within the months limit when switching to months', async () => {
    renderModal(['alice']);
    chooseSelectOption('client-renew-unit', 'days');
    fireEvent.change(screen.getByLabelText('Renew by'), { target: { value: '5000' } });
    chooseSelectOption('client-renew-unit', 'months');
    fireEvent.click(screen.getByRole('button', { name: 'Renew' }));

    await waitFor(() =>
      expect(postStub).toHaveBeenCalledWith(
        '/panel/api/clients/renew',
        { emails: ['alice'], months: 1200, days: 0, resetUsage: true },
        expect.anything(),
      ),
    );
  });

  // One dialog serves every renewal, so what was typed for one user must not carry over.
  it('starts over once closed', () => {
    function Harness() {
      const [open, setOpen] = useState(true);
      return (
        <>
          <button onClick={() => setOpen(true)}>reopen</button>
          <ClientRenewModal open={open} emails={['alice']} onClose={() => setOpen(false)} />
        </>
      );
    }
    renderWithProviders(<Harness />);
    fireEvent.change(screen.getByLabelText('Renew by'), { target: { value: '7' } });
    chooseSelectOption('client-renew-unit', 'days');
    fireEvent.click(screen.getByRole('checkbox', { name: 'Clear used traffic' }));
    fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    fireEvent.click(screen.getByText('reopen'));

    expect((screen.getByLabelText('Renew by') as HTMLInputElement).value).toBe('1');
    expect(unitText()).toBe('months');
    expect(
      (screen.getByRole('checkbox', { name: 'Clear used traffic' }) as HTMLInputElement).checked,
    ).toBe(true);
  });
});
