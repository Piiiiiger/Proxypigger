import { Alert, Button, Empty, List, Space, Tag, Typography } from 'antd';

import type { AbuseHistory, BanRecord } from '@/generated/zod';
import { banKind, banOutcome, banTime } from '@/lib/abuse';

export interface BanHistoryProps {
  // AI patrol notices have their own section on the user page; this list never reads them.
  history: Pick<AbuseHistory, 'status' | 'records'>;
  nowMs: number;
  // The user page shows the running ban in its own banner at the top.
  showBanner?: boolean;
  // Admin only: ending the running ban, and clearing the strikes.
  onLift?: () => void;
  onForgive?: () => void;
  busy?: boolean;
}

// BanBanner is a running ban with its reason and time left, or a lock.
export function BanBanner({ ban, nowMs }: { ban: BanRecord; nowMs: number }) {
  return ban.expiresAt === 0 ? (
    <Alert
      type="error"
      showIcon
      title="账号已停用：30 天内违规超过 3 次"
      description="请联系管理员说明情况。"
    />
  ) : (
    <Alert
      type="error"
      showIcon
      title={`账号封禁中：${ban.reason}`}
      description={banOutcome(ban, nowMs)}
    />
  );
}

// An account's bans of the last 90 days, newest first, with when and why;
// IP-limit bans are listed too but never counted as strikes.
export function BanHistory({
  history,
  nowMs,
  showBanner = true,
  onLift,
  onForgive,
  busy,
}: BanHistoryProps) {
  const { status, records } = history;
  const ban = status.ban;
  const locked = ban?.expiresAt === 0;
  return (
    <Space orientation="vertical" size="middle" style={{ width: '100%' }}>
      {ban && showBanner && <BanBanner ban={ban} nowMs={nowMs} />}
      {!locked && (
        <Typography.Text type={status.strikes > 0 ? 'warning' : 'secondary'}>
          30 天内违规 {status.strikes}/{status.limit} 次
          {status.strikes >= status.limit ? '，再违规一次账号将停用' : ''}
        </Typography.Text>
      )}
      {(onLift || onForgive) && (
        <Space wrap>
          {onLift && ban && (
            <Button danger onClick={onLift} loading={busy}>
              解除封禁
            </Button>
          )}
          {onForgive && status.strikes > 0 && (
            <Button onClick={onForgive} loading={busy}>
              清除违规次数
            </Button>
          )}
        </Space>
      )}
      {records.length === 0 ? (
        <Empty image={Empty.PRESENTED_IMAGE_SIMPLE} description="最近 90 天没有封禁" />
      ) : (
        <List
          size="small"
          dataSource={records}
          rowKey="id"
          renderItem={(record) => (
            <List.Item>
              <Space orientation="vertical" size={2} style={{ width: '100%' }}>
                <Space wrap size={6}>
                  <Typography.Text strong>{banTime(record)}</Typography.Text>
                  <Tag color={record.kind === 'iplimit' ? 'geekblue' : 'red'}>
                    {banKind(record)}
                  </Tag>
                  {record.forgiven && <Tag>已清除</Tag>}
                </Space>
                <Typography.Text>{record.reason}</Typography.Text>
                <Typography.Text type="secondary">{banOutcome(record, nowMs)}</Typography.Text>
              </Space>
            </List.Item>
          )}
        />
      )}
    </Space>
  );
}
