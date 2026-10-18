import { useEffect } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Alert, Button } from 'antd';
import { z } from 'zod';

import { keys } from '@/api/queryKeys';
import { BanBanner, BanHistory } from '@/components/abuse/BanHistory';
import { AbuseHistorySchema } from '@/generated/zod';
import { PortalSessionEnded } from './portalSession';

const POLL_INTERVAL_MS = 30_000;

const PatrolHistorySchema = AbuseHistorySchema.extend({
  aiPatrolNotices: z
    .array(
      z.object({
        id: z.number().int(),
        at: z.number(),
        action: z.string(),
        reason: z.string(),
        portalSeenAt: z.number(),
        notifiedAt: z.number(),
        forgiven: z.boolean(),
      }),
    )
    .default([]),
});
type PatrolHistory = z.infer<typeof PatrolHistorySchema>;

async function fetchBans(base: string): Promise<{ data: PatrolHistory; at: number }> {
  const res = await fetch(`${base}/bans`, {
    credentials: 'same-origin',
    headers: { Accept: 'application/json' },
  });
  if (!res.ok) {
    await res.text().catch(() => '');
    if (res.status === 401) throw new PortalSessionEnded();
    throw new Error(`portal bans: HTTP ${res.status}`);
  }
  return { data: PatrolHistorySchema.parse(await res.json()), at: Date.now() };
}

function usePortalBans(base: string, email: string) {
  return useQuery({
    queryKey: keys.portal.bans(base, email),
    queryFn: () => fetchBans(base),
    refetchInterval: POLL_INTERVAL_MS,
    refetchIntervalInBackground: false,
    staleTime: 0,
    gcTime: 0,
    retry: false,
  });
}

// PortalBanBanner puts a running ban or a lock above every view: someone who
// cannot connect comes here first to see why.
export function PortalBanBanner({ base, email }: { base: string; email: string }) {
  const query = usePortalBans(base, email);
  const client = useQueryClient();
  const ack = useMutation({
    mutationFn: async (id: number) => {
      const response = await fetch(`${base}/ai-patrol/seen`, {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ ids: [id] }),
      });
      if (!response.ok) throw new Error('确认失败，请重试');
    },
    onSuccess: () => client.invalidateQueries({ queryKey: keys.portal.bans(base, email) }),
  });
  const ban = query.data?.data.status.ban;
  const notice = query.data?.data.aiPatrolNotices
    .slice()
    .reverse()
    .find((n) => !n.forgiven && n.portalSeenAt === 0);
  if (!query.data) return null;
  return (
    <>
      {notice && (
        <Alert
          type="warning"
          showIcon
          title="Codex 巡查违规通知"
          description={
            notice.action === 'ai_suspended'
              ? '你的活动再次被标记为违规，账号已暂停 24 小时。请联系管理员了解详情。'
              : '你的活动已被标记为一次违规，本次不封禁。请联系管理员了解详情。首次违规记录后的 30 天内，收到通知后再次发生独立违规，将暂停账号 24 小时。'
          }
          action={
            <Button onClick={() => ack.mutate(notice.id)} loading={ack.isPending}>
              我已知晓
            </Button>
          }
        />
      )}
      {ack.isError && <p role="alert">确认失败，请重试</p>}
      {ban && <BanBanner ban={ban} nowMs={query.data.at} />}
    </>
  );
}

interface PortalBansProps {
  base: string;
  email: string;
  onSessionEnded: () => void;
}

// The signed-in person's bans of the last 90 days, when and why; only an admin
// can end a ban or a lock.
export default function PortalBans({ base, email, onSessionEnded }: PortalBansProps) {
  const query = usePortalBans(base, email);

  const sessionEnded = query.error instanceof PortalSessionEnded;
  useEffect(() => {
    if (sessionEnded) onSessionEnded();
  }, [sessionEnded, onSessionEnded]);

  if (!query.data) return null;
  const { data, at } = query.data;
  return (
    <>
      {data.aiPatrolNotices.length > 0 && (
        <section className="portal-section" aria-label="Codex 巡查违规记录">
          <div className="portal-section-head">
            <span className="portal-section-title">Codex 巡查违规记录</span>
          </div>
          {data.aiPatrolNotices.map((notice) => (
            <Alert
              key={notice.id}
              type="warning"
              showIcon
              title={`${new Date(notice.at * 1000).toLocaleString()} · ${notice.action === 'ai_suspended' ? '暂停 24 小时' : '违规已记录，未封禁'}`}
              description={`${notice.reason}。请联系管理员了解详情。`}
            />
          ))}
        </section>
      )}
      <section className="portal-section">
        <div className="portal-section-head">
          <span className="portal-section-title">封禁记录</span>
          <span className="portal-section-meta">最近 90 天</span>
        </div>
        <BanHistory history={data} nowMs={at} showBanner={false} />
      </section>
    </>
  );
}
