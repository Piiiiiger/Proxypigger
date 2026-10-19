import { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { Tag, theme } from 'antd';

import { SizeFormatter } from '@/utils';
import { Sparkline } from '@/components/viz';
import { useLimitsText } from '@/lib/plans/planText';
import Socks5ProxyList from '@/components/clients/Socks5Proxies';
import type { Socks5Proxy, TrafficDay } from '@/generated/zod';
import type { PortalPlan } from '@/schemas/portal';

export function PortalPlanCard({ plan }: { plan: PortalPlan }) {
  const { t } = useTranslation();
  const text = useLimitsText()(plan);
  const facts = [
    [t('pages.plans.quota'), text.quota],
    [t('pages.inbounds.periodicTrafficResetTitle'), text.reset],
    [t('pages.clients.limitIp'), text.ipLimit],
  ];
  return (
    <section className="portal-section">
      <div className="portal-section-head">
        <span className="portal-section-title">{t('subscription.portal.plan')}</span>
        <Tag color="volcano" className="portal-plan-name">
          {plan.name}
        </Tag>
      </div>
      <dl className="portal-facts">
        {facts.map(([label, value]) => (
          <div key={label}>
            <dt>{label}</dt>
            <dd>{value}</dd>
          </div>
        ))}
      </dl>
    </section>
  );
}

export function PortalUsageCard({ daily }: { daily: TrafficDay[] }) {
  const { t } = useTranslation();
  const { token } = theme.useToken();
  const series = useMemo(
    () => ({
      up: daily.map((d) => d.up),
      down: daily.map((d) => d.down),
      labels: daily.map((d) => d.day.slice(5)),
      total: daily.reduce((sum, d) => sum + d.up + d.down, 0),
    }),
    [daily],
  );
  return (
    <section className="portal-section">
      <div className="portal-section-head">
        <span className="portal-section-title">
          {t('subscription.portal.usage', { days: daily.length })}
        </span>
        <span className="portal-section-meta">
          {t('subscription.portal.usageTotal', { size: SizeFormatter.sizeFormat(series.total) })}
        </span>
      </div>
      <Sparkline
        data={series.up}
        data2={series.down}
        labels={series.labels}
        height={140}
        strokeWidth={1.75}
        fillOpacity={0.24}
        showTooltip
        showLegend={false}
        valueMax={null}
        stroke={token.colorPrimary}
        stroke2={token.colorTextTertiary}
        name1={t('pages.index.upload')}
        name2={t('pages.index.download')}
        yFormatter={SizeFormatter.sizeFormat}
      />
    </section>
  );
}

export function PortalSocks5Card({ proxies }: { proxies: Socks5Proxy[] }) {
  const { t } = useTranslation();
  return (
    <section className="portal-section">
      <div className="portal-section-head">
        <span className="portal-section-title">{t('pages.clients.socks5Title')}</span>
      </div>
      <Socks5ProxyList proxies={proxies} />
    </section>
  );
}
