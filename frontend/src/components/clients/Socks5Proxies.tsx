import { useTranslation } from 'react-i18next';
import { Tag, Typography } from 'antd';

import type { Socks5Proxy } from '@/generated/zod';
import { socks5Line, socks5Url } from '@/lib/clients/socks5';

import './Socks5Proxies.css';

interface Socks5ProxyListProps {
  proxies: Socks5Proxy[];
}

// Each SOCKS5 login as a link and as host:port:user:pass, since no subscription carries them.
export default function Socks5ProxyList({ proxies }: Socks5ProxyListProps) {
  const { t } = useTranslation();
  const copyTips = [t('copy'), t('copied')];
  return (
    <div className="socks5-list">
      <Typography.Text type="secondary" className="socks5-hint">
        {t('pages.clients.socks5Hint')}
      </Typography.Text>
      {proxies.map((p) => {
        const url = socks5Url(p);
        const line = socks5Line(p);
        return (
          <div key={p.inboundId} className="socks5-row">
            <div className="socks5-row-head">
              <Tag color="purple">SOCKS5</Tag>
              <span className="socks5-row-name">{p.remark}</span>
              {p.exit && <Tag color="blue">{t('pages.clients.socks5Exit', { name: p.exit })}</Tag>}
              {!p.enable && <Tag>{t('disabled')}</Tag>}
            </div>
            <Typography.Text
              code
              copyable={{ text: url, tooltips: copyTips }}
              className="socks5-value"
            >
              {url}
            </Typography.Text>
            <Typography.Text
              code
              copyable={{ text: line, tooltips: copyTips }}
              className="socks5-value"
            >
              {line}
            </Typography.Text>
          </div>
        );
      })}
    </div>
  );
}
