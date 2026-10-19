import { Protocols } from '@/schemas/primitives';

/*
 * Protocols whose inbounds can live on a sub-node (the "Deploy To" set).
 * Everything else (http, tunnel, tun, mtproto) is panel-local only; mixed is
 * eligible because user SOCKS5 ports live on the server they exit from.
 * Shared by the inbound form's Deploy To selector and the clone dialog's
 * target picker so the two surfaces can never drift apart.
 */
export const NODE_ELIGIBLE_PROTOCOLS: Readonly<Record<string, true>> = {
  [Protocols.SNELL]: true,
  [Protocols.VLESS]: true,
  [Protocols.VMESS]: true,
  [Protocols.TROJAN]: true,
  [Protocols.SHADOWSOCKS]: true,
  [Protocols.HYSTERIA]: true,
  [Protocols.WIREGUARD]: true,
  [Protocols.MIXED]: true,
};
