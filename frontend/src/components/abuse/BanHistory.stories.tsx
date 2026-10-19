import type { Meta, StoryObj } from '@storybook/react-vite';

import type { BanRecord } from '@/generated/zod';

import { BanHistory, type BanHistoryProps } from './BanHistory';

const meta = {
  title: 'Abuse/BanHistory',
  component: BanHistory,
  tags: ['autodocs'],
  parameters: {
    docs: {
      description: {
        component:
          "An account's bans of the last 90 days, newest first, with when, why and how each ended, and its standing on top: a running ban or a lock, and its strikes within 30 days. IP-limit bans are listed too but never count as strikes. The admin's dialog passes onLift and onForgive; the user page passes neither.",
      },
    },
  },
  argTypes: {
    history: { description: 'The answer of `GET /panel/api/abuse/history/:email`.' },
    nowMs: { description: 'When the history was fetched (ms); time left counts from it.' },
    onLift: { description: 'Admin only: ends the running ban; shown while one runs.' },
    onForgive: { description: 'Admin only: clears the strikes; shown while there are any.' },
    busy: { description: 'An admin action is in flight.' },
  },
} satisfies Meta<typeof BanHistory>;

export default meta;

type Story = StoryObj<typeof meta>;

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
const history: BanHistoryProps['history'] = {
  status: { ban: running, strikes: 2, limit: 3 },
  records: [
    running,
    record({
      id: 2,
      kind: 'iplimit',
      rule: 'iplimit',
      strike: 0,
      reason: '同时在线 IP 超过上限（3 个），暂停 198.51.100.9',
      bannedAt: NOW - 2 * 86400,
      expiresAt: NOW - 2 * 86400 + 1800,
    }),
    record({ id: 1, bannedAt: NOW - 5 * 86400, expiresAt: NOW - 5 * 86400 + 1800 }),
  ],
};

export const AdminView: Story = {
  args: { history, nowMs: NOW_MS, onLift: () => {}, onForgive: () => {} },
};

export const UserPage: Story = {
  args: { history, nowMs: NOW_MS },
};

export const Locked: Story = {
  args: {
    history: {
      status: { ban: record({ id: 4, strike: 4, expiresAt: 0 }), strikes: 4, limit: 3 },
      records: [record({ id: 4, strike: 4, expiresAt: 0 })],
    },
    nowMs: NOW_MS,
  },
};

export const Clean: Story = {
  args: { history: { status: { ban: null, strikes: 0, limit: 3 }, records: [] }, nowMs: NOW_MS },
};
