import type { Socks5Proxy } from '@/generated/zod';

type Socks5Login = Pick<Socks5Proxy, 'host' | 'port' | 'username' | 'password'>;

function urlHost(host: string): string {
  return host.includes(':') && !host.startsWith('[') ? `[${host}]` : host;
}

export function socks5Url(p: Socks5Login): string {
  const login = `${encodeURIComponent(p.username)}:${encodeURIComponent(p.password)}`;
  return `socks5://${login}@${urlHost(p.host)}:${p.port}`;
}

// The plain form most fingerprint browsers and proxy tools import.
export function socks5Line(p: Socks5Login): string {
  return `${p.host}:${p.port}:${p.username}:${p.password}`;
}
