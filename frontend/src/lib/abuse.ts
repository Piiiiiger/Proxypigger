import type { BanRecord } from '@/generated/zod';

export const ABUSE_MODES = [
  { value: 'off', label: '关闭' },
  { value: 'observe', label: '观察' },
  { value: 'enforce', label: '封禁' },
] as const;

// What a rule's hits do on an enforcing server; a sign-up guard cannot warn.
export const RULE_ACTIONS = [
  { value: 'record', label: '只记录' },
  { value: 'warn', label: '提醒用户' },
  { value: 'ban', label: '封禁' },
] as const;

export const SIGNUP_ACTIONS = [
  { value: 'record', label: '只记录' },
  { value: 'ban', label: '阻止注册 24 小时' },
] as const;

// What the policy did about a hit, as the events list says it.
export const ABUSE_OUTCOMES: Record<string, string> = {
  observed: '观察（未处罚）',
  noticed: '只记录',
  warned: '已提醒用户',
  banned: '封禁 30 分钟',
  locked: '账号停用',
  held: '封禁中再次触发',
  blocked: '该网络暂停注册',
  ai_violation: 'Codex 违规已记录（未封禁）',
  ai_suspended: 'Codex 复犯：暂停 24 小时',
};

// eventSamples reads the few destinations or accounts a hit stored as evidence.
export function eventSamples(raw: string): string[] {
  try {
    const parsed: unknown = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed.filter((v): v is string => typeof v === 'string') : [];
  } catch {
    return [];
  }
}

function clock(ms: number): string {
  const d = new Date(ms);
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

// unixClock writes a time in seconds as "MM-DD HH:mm" in the viewer's zone.
export function unixClock(seconds: number): string {
  return clock(seconds * 1000);
}

export function banTime(record: BanRecord): string {
  return unixClock(record.bannedAt);
}

// How a ban ended, or how long it still runs; a lock runs until an admin lifts it.
export function banOutcome(record: BanRecord, nowMs: number): string {
  if (record.liftedAt > 0) return '已由管理员解除';
  if (record.expiresAt === 0) return '账号停用中，请联系管理员';
  const leftMs = record.expiresAt * 1000 - nowMs;
  if (leftMs > 0) {
    const end = new Date(record.expiresAt * 1000);
    const pad = (n: number) => String(n).padStart(2, '0');
    return `还剩 ${Math.max(1, Math.ceil(leftMs / 60000))} 分钟（${pad(end.getHours())}:${pad(end.getMinutes())} 恢复）`;
  }
  return `封禁 ${Math.round((record.expiresAt - record.bannedAt) / 60)} 分钟，已恢复`;
}

export function banKind(record: BanRecord): string {
  if (record.kind === 'ai_patrol') return 'Codex 巡查：暂停 24 小时';
  return record.kind === 'iplimit' ? 'IP 超限' : `违规第 ${record.strike} 次`;
}
