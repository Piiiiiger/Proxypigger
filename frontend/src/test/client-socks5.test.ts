import { describe, expect, it } from 'vitest';

import { socks5Line, socks5Url } from '@/lib/clients/socks5';

const base = {
  inboundId: 31,
  remark: 'Tokyo SOCKS5',
  host: '198.51.100.10',
  port: 30001,
  username: 'alice',
  password: 'p@ss:word/1',
  exit: '',
  enable: true,
};

describe('socks5Url', () => {
  it('percent-encodes the login so a password with @ or : still parses', () => {
    expect(socks5Url(base)).toBe('socks5://alice:p%40ss%3Aword%2F1@198.51.100.10:30001');
    expect(new URL(socks5Url(base)).password).toBe('p%40ss%3Aword%2F1');
  });

  it('keeps a non-ASCII user name intact after decoding', () => {
    const url = new URL(socks5Url({ ...base, username: '燕子' }));
    expect(decodeURIComponent(url.username)).toBe('燕子');
  });

  it('brackets an IPv6 host', () => {
    expect(socks5Url({ ...base, host: '2001:db8::10' })).toBe(
      'socks5://alice:p%40ss%3Aword%2F1@[2001:db8::10]:30001',
    );
  });
});

describe('socks5Line', () => {
  it('gives the host:port:user:pass form fingerprint browsers import', () => {
    expect(socks5Line(base)).toBe('198.51.100.10:30001:alice:p@ss:word/1');
  });
});
