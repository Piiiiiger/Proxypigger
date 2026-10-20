import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Card, Tooltip, Typography } from 'antd';
import {
  CalendarOutlined,
  ClusterOutlined,
  FileTextOutlined,
  InfoCircleOutlined,
  SafetyOutlined,
  TeamOutlined,
} from '@ant-design/icons';

import './PlanCard.css';

export interface PlanCardProps {
  name: string;
  remark?: string;
  template: ReactNode;
  ipLimit: ReactNode;
  terms: ReactNode;
  nodes: ReactNode;
  members: ReactNode;
  actions: ReactNode[];
}

function PlanRow({
  icon,
  label,
  children,
}: {
  icon: ReactNode;
  label: string;
  children: ReactNode;
}) {
  return (
    <div className="plan-row">
      <span className="plan-row-label">
        {icon}
        {label}
      </span>
      <span className="plan-row-value">{children}</span>
    </div>
  );
}

export default function PlanCard({
  name,
  remark,
  template,
  ipLimit,
  terms,
  nodes,
  members,
  actions,
}: PlanCardProps) {
  const { t } = useTranslation();
  return (
    <Card
      className="plan-card"
      title={
        <span className="plan-card-title">
          <Typography.Text ellipsis>{name}</Typography.Text>
          {remark && (
            <Tooltip title={remark}>
              <InfoCircleOutlined className="plan-card-remark-icon" />
            </Tooltip>
          )}
        </span>
      }
      actions={actions}
    >
      <PlanRow icon={<FileTextOutlined />} label={t('pages.plans.template')}>
        {template}
      </PlanRow>
      <PlanRow icon={<SafetyOutlined />} label={t('pages.clients.limitIp')}>
        {ipLimit}
      </PlanRow>
      <PlanRow icon={<CalendarOutlined />} label="计费周期">
        {terms}
      </PlanRow>
      <PlanRow icon={<ClusterOutlined />} label={t('pages.plans.servers')}>
        {nodes}
      </PlanRow>
      <PlanRow icon={<TeamOutlined />} label={t('pages.plans.people')}>
        {members}
      </PlanRow>
    </Card>
  );
}
