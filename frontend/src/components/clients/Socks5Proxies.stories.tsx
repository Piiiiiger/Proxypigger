import type { Meta, StoryObj } from '@storybook/react-vite';
import { expect } from 'storybook/test';

import type { Socks5Proxy } from '@/generated/zod';

import Socks5ProxyList from './Socks5Proxies';

const meta = {
  title: 'Clients/Socks5ProxyList',
  component: Socks5ProxyList,
  tags: ['autodocs'],
  parameters: {
    docs: {
      description: {
        component:
          "A user's SOCKS5 logins, shown in the admin user dialog and the portal: each as a socks5:// link and as host:port:user:pass, both copyable. A login relayed on through a chain names its exit node; subscriptions never carry these.",
      },
    },
  },
  argTypes: {
    proxies: { description: 'The answer of `GET /panel/api/clients/socks5/:email`.' },
  },
} satisfies Meta<typeof Socks5ProxyList>;

export default meta;

type Story = StoryObj<typeof meta>;

const proxies: Socks5Proxy[] = [
  {
    inboundId: 31,
    remark: 'Tokyo SOCKS5',
    host: '198.51.100.10',
    port: 30001,
    username: 'alice',
    password: '4f0c7c2d9a1b4e5f',
    exit: '',
    enable: true,
  },
  {
    inboundId: 32,
    remark: 'Tokyo home SOCKS5',
    host: '198.51.100.10',
    port: 30002,
    username: 'alice',
    password: '4f0c7c2d9a1b4e5f',
    exit: 'Tokyo home',
    enable: true,
  },
];

export const DirectAndRelayed: Story = {
  args: { proxies },
  play: async ({ canvas }) => {
    await expect(
      canvas.getByText('socks5://alice:4f0c7c2d9a1b4e5f@198.51.100.10:30002'),
    ).toBeVisible();
    await expect(canvas.getByText('198.51.100.10:30001:alice:4f0c7c2d9a1b4e5f')).toBeVisible();
    await expect(canvas.getAllByText('SOCKS5')).toHaveLength(2);
  },
};
