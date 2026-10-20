import { fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import PlanFormModal from '@/pages/plans/PlanFormModal';
import type { PlanSummary } from '@/generated/zod';
import { HttpUtil, Msg } from '@/utils';
import {
  chooseSelectOption,
  fieldLabels,
  listSelectOptions,
  renderWithProviders,
} from './test-utils';

const plan: PlanSummary = {
  id: 7,
  name: 'Monthly',
  limitIp: 2,
  remark: '',
  templateId: 0,
  inboundIds: [1, 2],
  memberCount: 3,
  sortIndex: 0,
  createdAt: 0,
  updatedAt: 0,
};

const getStub = vi.mocked(HttpUtil.get);
const setupGet = getStub.getMockImplementation();
const NODES = [
  { key: '1:direct', label: '香港-Neburst', inboundId: 1, relayInboundId: 0 },
  { key: '2:direct', label: '新加坡-家宽（直连）', inboundId: 2, relayInboundId: 0 },
  { key: '2:relay:7', label: '新加坡-家宽（中转·香港-Neburst）', inboundId: 2, relayInboundId: 1 },
];

beforeEach(() => {
  getStub.mockImplementation(
    async (url: string) => new Msg(true, '', url === '/panel/api/plans/nodeOptions' ? NODES : []),
  );
});

afterEach(() => {
  if (setupGet) getStub.mockImplementation(setupGet);
});

const TEMPLATES = [
  {
    id: 3,
    name: 'alpha_v3',
    isDefault: true,
    kind: 'yaml',
    size: 10,
    planCount: 2,
    updatedAt: 0,
    baseId: 0,
    changes: [],
  },
  {
    id: 5,
    name: 'beta_v3',
    isDefault: false,
    kind: 'yaml',
    size: 10,
    planCount: 0,
    updatedAt: 0,
    baseId: 0,
    changes: [],
  },
];

async function save() {
  await waitFor(() =>
    expect((screen.getByRole('button', { name: 'Save' }) as HTMLButtonElement).disabled).toBe(
      false,
    ),
  );
  fireEvent.click(screen.getByRole('button', { name: 'Save' }));
}

describe('PlanFormModal', () => {
  // Quota, validity and reset belong to each user now; a plan is its servers, rules and IP limit.
  it('asks only for what a plan holds', () => {
    renderWithProviders(<PlanFormModal open plan={plan} onClose={() => {}} onConfirm={vi.fn()} />);
    expect(fieldLabels()).toEqual([
      'Name',
      'IP Limit',
      '计费周期',
      'Nodes',
      'Rule template',
      'Remark',
    ]);
  });

  // A plan sold only by some terms saves their calendar months, shortest first; none
  // ticked sells it for any.
  it('saves the terms the plan is sold by', async () => {
    const onConfirm = vi.fn();
    renderWithProviders(
      <PlanFormModal
        open
        plan={{ ...plan, termMonths: [1] }}
        onClose={() => {}}
        onConfirm={onConfirm}
      />,
    );
    const monthly = screen.getByRole('checkbox', { name: '月付（1 个月）' }) as HTMLInputElement;
    expect(monthly.checked).toBe(true);
    fireEvent.click(monthly);
    fireEvent.click(screen.getByRole('checkbox', { name: '年付（12 个月）' }));
    fireEvent.click(screen.getByRole('checkbox', { name: '季付（3 个月）' }));
    await save();
    await waitFor(() => expect(onConfirm).toHaveBeenCalledTimes(1));
    expect(onConfirm.mock.calls[0][0].termMonths).toEqual([3, 12]);
  });

  // Ticked by default, every edit re-stamped the members' limits, even one that only added a server.
  it('saves an edit without re-applying the IP limit unless the box is ticked', async () => {
    const onConfirm = vi.fn();
    renderWithProviders(
      <PlanFormModal open plan={plan} onClose={() => {}} onConfirm={onConfirm} />,
    );
    const reapply = screen.getByRole('checkbox', {
      name: 'Also apply the IP limit to the 3 user(s) on this plan',
    }) as HTMLInputElement;
    expect(reapply.checked).toBe(false);
    expect(
      screen.getByText(
        "Nodes you add or remove always reach this plan's users, with or without this option.",
      ),
    ).toBeTruthy();

    await save();
    await waitFor(() => expect(onConfirm).toHaveBeenCalledTimes(1));
    expect(onConfirm.mock.calls[0][1]).toBe(false);

    fireEvent.click(reapply);
    await save();
    await waitFor(() => expect(onConfirm).toHaveBeenCalledTimes(2));
    expect(onConfirm.mock.calls[1][1]).toBe(true);
  });

  // The rules box became a template picker; 0 stays on the default template.
  it('names the default template and saves the one picked', async () => {
    getStub.mockImplementation(async (url: string) =>
      url === '/panel/api/ruleTemplates/list'
        ? new Msg(true, '', TEMPLATES)
        : new Msg(true, '', url === '/panel/api/plans/nodeOptions' ? NODES : []),
    );
    const onConfirm = vi.fn();
    renderWithProviders(
      <PlanFormModal open plan={plan} onClose={() => {}} onConfirm={onConfirm} />,
    );

    await screen.findByText('Default (alpha_v3)');
    chooseSelectOption(screen.getByLabelText('Rule template').id, 'beta_v3');
    await save();

    await waitFor(() => expect(onConfirm).toHaveBeenCalledTimes(1));
    expect(onConfirm.mock.calls[0][0]).toMatchObject({ templateId: 5 });
  });

  it('shows direct and relay variants together and saves them independently', async () => {
    const onConfirm = vi.fn();
    const view = renderWithProviders(
      <PlanFormModal open plan={plan} onClose={() => {}} onConfirm={onConfirm} />,
    );
    await screen.findByText('新加坡-家宽（中转·香港-Neburst）');
    expect(screen.getByText('新加坡-家宽（直连）')).toBeTruthy();
    expect(screen.getByText('新加坡-家宽（中转·香港-Neburst）')).toBeTruthy();
    expect(listSelectOptions('plan-node-options')).toEqual(NODES.map((node) => node.label));
    fireEvent.click(screen.getByRole('button', { name: /Clear all/i }));
    chooseSelectOption('plan-node-options', NODES[1].label);
    chooseSelectOption('plan-node-options', NODES[2].label);
    await save();
    await waitFor(() => expect(onConfirm).toHaveBeenCalledOnce());
    const saved = onConfirm.mock.calls[0][0];
    expect(saved.nodeKeys).toEqual(['2:direct', '2:relay:7']);
    expect(saved.inboundIds).toEqual([2, 1]);
    view.unmount();
    renderWithProviders(
      <PlanFormModal open plan={{ ...plan, ...saved }} onClose={() => {}} onConfirm={vi.fn()} />,
    );
    await screen.findByText(NODES[2].label);
    const select = screen.getByLabelText('Nodes').closest('.ant-select')!;
    const chips = Array.from(select.querySelectorAll('.ant-select-selection-item')).map((item) =>
      item.getAttribute('title'),
    );
    expect(chips).toContain(NODES[2].label);
    expect(chips).not.toContain(NODES[0].label);
    expect(chips).toContain(NODES[1].label);
  });

  it('allows deleting a direct variant while retaining the relay variant', async () => {
    renderWithProviders(<PlanFormModal open plan={plan} onClose={() => {}} onConfirm={vi.fn()} />);
    await screen.findByText(NODES[2].label);
    fireEvent.click(screen.getByRole('button', { name: /Clear all/i }));
    chooseSelectOption('plan-node-options', NODES[2].label);

    const select = screen.getByLabelText('Nodes').closest('.ant-select')!;
    const chips = Array.from(select.querySelectorAll('.ant-select-selection-item')).map((item) =>
      item.getAttribute('title'),
    );
    expect(chips).toContain(NODES[2].label);
    expect(chips).not.toContain(NODES[0].label);
  });

  it('keeps an explicitly selected relay alongside its chain after saving and reopening', async () => {
    const onConfirm = vi.fn();
    const view = renderWithProviders(
      <PlanFormModal open plan={plan} onClose={() => {}} onConfirm={onConfirm} />,
    );
    await screen.findByText(NODES[2].label);
    fireEvent.click(screen.getByRole('button', { name: /Clear all/i }));
    chooseSelectOption('plan-node-options', NODES[2].label);
    chooseSelectOption('plan-node-options', NODES[0].label);
    await save();
    await waitFor(() => expect(onConfirm).toHaveBeenCalledOnce());
    const saved = onConfirm.mock.calls[0][0];
    expect(saved.nodeKeys).toEqual(['2:relay:7', '1:direct']);
    view.unmount();
    renderWithProviders(
      <PlanFormModal open plan={{ ...plan, ...saved }} onClose={() => {}} onConfirm={vi.fn()} />,
    );
    await screen.findByText(NODES[2].label);
    const select = screen.getByLabelText('Nodes').closest('.ant-select')!;
    const chips = Array.from(select.querySelectorAll('.ant-select-selection-item')).map((item) =>
      item.getAttribute('title'),
    );
    expect(chips).toContain(NODES[0].label);
    expect(chips).toContain(NODES[2].label);
  });

  it('keeps independent variants in proxy-group assignments', async () => {
    const onConfirm = vi.fn();
    const groupPlan = {
      ...plan,
      proxyGroupNames: ['🤖 AI 服务'],
      proxyGroups: [{ name: '🤖 AI 服务', inboundIds: [2], nodeKeys: ['2:relay:7'] }],
    };
    renderWithProviders(
      <PlanFormModal open plan={groupPlan} onClose={() => {}} onConfirm={onConfirm} />,
    );
    await save();
    await waitFor(() => expect(onConfirm).toHaveBeenCalledOnce());
    expect(onConfirm.mock.calls[0][0].proxyGroups).toEqual([
      { name: '🤖 AI 服务', inboundIds: [2], nodeKeys: ['2:relay:7'] },
    ]);
  });

  it('blocks saving when node options are malformed instead of clearing assignments', async () => {
    getStub.mockImplementation(
      async (url: string) => new Msg(true, '', url === '/panel/api/plans/nodeOptions' ? {} : []),
    );
    const onConfirm = vi.fn();
    renderWithProviders(
      <PlanFormModal open plan={plan} onClose={() => {}} onConfirm={onConfirm} />,
    );
    await screen.findByText('节点加载失败，请刷新后重试');
    expect((screen.getByRole('button', { name: 'Save' }) as HTMLButtonElement).disabled).toBe(true);
    expect(onConfirm).not.toHaveBeenCalled();
  });
});
