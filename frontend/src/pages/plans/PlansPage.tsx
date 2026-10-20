import { useMemo, useState } from 'react';
import type { ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate } from 'react-router';
import {
  Button,
  Card,
  ConfigProvider,
  Empty,
  Layout,
  Modal,
  Result,
  Segmented,
  Space,
  Spin,
  Table,
  Tooltip,
  Typography,
  message,
} from 'antd';
import {
  AppstoreOutlined,
  CalendarOutlined,
  ClusterOutlined,
  DeleteOutlined,
  EditOutlined,
  FileTextOutlined,
  InfoCircleOutlined,
  KeyOutlined,
  PlusOutlined,
  SafetyOutlined,
  StarFilled,
  TeamOutlined,
  UnorderedListOutlined,
  UserAddOutlined,
} from '@ant-design/icons';

import AppNav from '@/layouts/AppNav';
import { PageHeader } from '@/components/ui';
import { useTheme } from '@/hooks/useTheme';
import { usePlansQuery } from '@/api/queries/usePlansQuery';
import { usePlanMutations } from '@/api/queries/usePlanMutations';
import { useRuleTemplatesQuery } from '@/api/queries/useRuleTemplates';
import type { PlanSummary } from '@/generated/zod';
import type { PlanFormValues } from '@/schemas/plan';
import AssignPlanModal from './AssignPlanModal';
import PlanFormModal from './PlanFormModal';
import ActivationCodesModal from './ActivationCodesModal';
import { termsText, useInboundChoices } from './planText';
import { usePlanNodeOptions } from '@/api/queries/usePlanNodeOptions';
import './PlansPage.css';

type PlansView = 'grid' | 'list';
const VIEW_KEY = 'plans-view';

function readView(): PlansView {
  try {
    return localStorage.getItem(VIEW_KEY) === 'list' ? 'list' : 'grid';
  } catch {
    return 'grid';
  }
}

function writeView(view: PlansView) {
  try {
    localStorage.setItem(VIEW_KEY, view);
  } catch {
    /* a blocked store only forgets the choice */
  }
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

export default function PlansPage() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const { isDark, isUltra, antdThemeConfig } = useTheme();
  const [messageApi, messageContextHolder] = message.useMessage();
  const [modal, modalContextHolder] = Modal.useModal();
  const { plans, fetched, fetchError, loading, refetch } = usePlansQuery();
  const [codePlan, setCodePlan] = useState<PlanSummary | null>(null);
  const { create, update, remove } = usePlanMutations();
  const { templates } = useRuleTemplatesQuery();
  const { labelOf } = useInboundChoices();
  const { data: planNodeOptions = [] } = usePlanNodeOptions();

  const [view, setView] = useState<PlansView>(readView);
  const [formOpen, setFormOpen] = useState(false);
  const [editing, setEditing] = useState<PlanSummary | null>(null);
  const [assignPlanId, setAssignPlanId] = useState<number | null>(null);

  const pageClass = useMemo(() => {
    const classes = ['plans-page'];
    if (isDark) classes.push('is-dark');
    if (isUltra) classes.push('is-ultra');
    return classes.join(' ');
  }, [isDark, isUltra]);

  // A plan naming no template gets the default one, shown with its star.
  function templateOf(plan: PlanSummary): ReactNode {
    const tpl = templates.find((x) => (plan.templateId ? x.id === plan.templateId : x.isDefault));
    if (!tpl)
      return <Typography.Text type="secondary">{t('pages.plans.templateNone')}</Typography.Text>;
    return (
      <span className="plan-template-name">
        {tpl.name}
        {tpl.isDefault && (
          <StarFilled className="plan-template-star" aria-label={t('pages.rules.isDefault')} />
        )}
      </span>
    );
  }

  function ipLimitOf(plan: PlanSummary): string {
    return plan.limitIp > 0 ? String(plan.limitIp) : t('unlimited');
  }

  function nodesOf(plan: PlanSummary): ReactNode {
    const labels =
      plan.nodeKeys?.map((key) => planNodeOptions.find((node) => node.key === key)?.label ?? key) ??
      plan.inboundIds.map((id) => labelOf(id));
    const count = labels.length;
    if (count === 0) return <Typography.Text type="secondary">0</Typography.Text>;
    return (
      <Tooltip title={labels.join(' · ')}>
        <span className="plan-count">{count}</span>
      </Tooltip>
    );
  }

  function membersOf(plan: PlanSummary): ReactNode {
    return (
      <Button
        type="link"
        size="small"
        className="plan-members-link"
        onClick={() => navigate(`/clients?plan=${plan.id}`)}
      >
        {t('pages.plans.members', { count: plan.memberCount })}
      </Button>
    );
  }

  function changeView(next: PlansView) {
    setView(next);
    writeView(next);
  }

  function openForm(plan: PlanSummary | null) {
    setEditing(plan);
    setFormOpen(true);
  }

  async function savePlan(values: PlanFormValues, reapplyLimits: boolean) {
    const msg = editing ? await update(editing.id, values, reapplyLimits) : await create(values);
    if (msg?.success) {
      messageApi.success(t('pages.plans.toasts.saved'));
      setFormOpen(false);
    }
  }

  function confirmDelete(plan: PlanSummary) {
    if (plan.memberCount > 0) {
      messageApi.warning(t('pages.plans.deleteBlocked', { count: plan.memberCount }));
      return;
    }
    modal.confirm({
      title: t('pages.plans.deleteConfirm', { name: plan.name }),
      okText: t('delete'),
      okType: 'danger',
      cancelText: t('cancel'),
      onOk: async () => {
        const msg = await remove(plan.id);
        if (msg?.success) messageApi.success(t('pages.plans.toasts.deleted'));
      },
    });
  }

  const addButton = (
    <Button type="primary" icon={<PlusOutlined />} onClick={() => openForm(null)}>
      {t('pages.plans.add')}
    </Button>
  );

  const headerExtra =
    plans.length > 0 ? (
      <Space>
        <Segmented<PlansView>
          value={view}
          onChange={changeView}
          options={[
            { value: 'grid', icon: <AppstoreOutlined />, title: t('pages.plans.viewGrid') },
            { value: 'list', icon: <UnorderedListOutlined />, title: t('pages.plans.viewList') },
          ]}
        />
        {addButton}
      </Space>
    ) : undefined;

  const actionsOf = (plan: PlanSummary) => [
    <Button key="codes" type="text" icon={<KeyOutlined />} onClick={() => setCodePlan(plan)}>
      {t('pages.plans.codes.title')}
    </Button>,
    <Button
      key="assign"
      type="text"
      icon={<UserAddOutlined />}
      onClick={() => setAssignPlanId(plan.id)}
    >
      {t('pages.plans.addPeople')}
    </Button>,
    <Button key="edit" type="text" icon={<EditOutlined />} onClick={() => openForm(plan)}>
      {t('edit')}
    </Button>,
    <Button
      key="delete"
      type="text"
      danger
      icon={<DeleteOutlined />}
      onClick={() => confirmDelete(plan)}
    >
      {t('delete')}
    </Button>,
  ];

  const grid = (
    <div className="plan-grid">
      {plans.map((plan) => {
        return (
          <Card
            key={plan.id}
            className="plan-card"
            title={
              <span className="plan-card-title">
                <Typography.Text ellipsis>{plan.name}</Typography.Text>
                {plan.remark && (
                  <Tooltip title={plan.remark}>
                    <InfoCircleOutlined className="plan-card-remark-icon" />
                  </Tooltip>
                )}
              </span>
            }
            actions={actionsOf(plan)}
          >
            <PlanRow icon={<FileTextOutlined />} label={t('pages.plans.template')}>
              {templateOf(plan)}
            </PlanRow>
            <PlanRow icon={<SafetyOutlined />} label={t('pages.clients.limitIp')}>
              {ipLimitOf(plan)}
            </PlanRow>
            <PlanRow icon={<CalendarOutlined />} label="计费周期">
              {termsText(plan.termMonths)}
            </PlanRow>
            <PlanRow icon={<ClusterOutlined />} label={t('pages.plans.servers')}>
              {nodesOf(plan)}
            </PlanRow>
            <PlanRow icon={<TeamOutlined />} label={t('pages.plans.people')}>
              {membersOf(plan)}
            </PlanRow>
          </Card>
        );
      })}
    </div>
  );

  const list = (
    <Card styles={{ body: { padding: 0 } }}>
      <Table<PlanSummary>
        rowKey="id"
        pagination={false}
        scroll={{ x: 'max-content' }}
        dataSource={plans}
        columns={[
          { title: t('pages.plans.name'), dataIndex: 'name', key: 'name' },
          {
            title: t('pages.plans.template'),
            key: 'template',
            render: (_, plan) => templateOf(plan),
          },
          {
            title: t('pages.clients.limitIp'),
            key: 'ip',
            render: (_, plan) => ipLimitOf(plan),
          },
          { title: '计费周期', key: 'terms', render: (_, plan) => termsText(plan.termMonths) },
          { title: t('pages.plans.servers'), key: 'nodes', render: (_, plan) => nodesOf(plan) },
          { title: t('pages.plans.people'), key: 'users', render: (_, plan) => membersOf(plan) },
          {
            title: t('pages.plans.actions'),
            key: 'actions',
            render: (_, plan) => <Space size={0}>{actionsOf(plan)}</Space>,
          },
        ]}
      />
    </Card>
  );

  return (
    <ConfigProvider theme={antdThemeConfig}>
      {messageContextHolder}
      {modalContextHolder}
      <Layout className={pageClass}>
        <AppNav />
        <Layout className="content-shell">
          <Layout.Content id="content-layout" className="content-area">
            <PageHeader
              title={t('menu.plans')}
              description={t('pages.plans.intro')}
              extra={headerExtra}
            />
            <Spin spinning={!fetched} delay={200} description={t('loading')} size="large">
              {!fetched ? (
                <div className="loading-spacer" />
              ) : fetchError ? (
                <Result
                  status="error"
                  title={t('somethingWentWrong')}
                  subTitle={fetchError}
                  extra={
                    <Button type="primary" loading={loading} onClick={() => refetch()}>
                      {t('refresh')}
                    </Button>
                  }
                />
              ) : plans.length === 0 ? (
                <Card>
                  <Empty description={t('pages.plans.empty')}>{addButton}</Empty>
                </Card>
              ) : view === 'list' ? (
                list
              ) : (
                grid
              )}
            </Spin>
          </Layout.Content>
        </Layout>
      </Layout>
      <PlanFormModal
        open={formOpen}
        plan={editing}
        onClose={() => setFormOpen(false)}
        onConfirm={savePlan}
      />
      <AssignPlanModal
        open={assignPlanId !== null}
        plans={plans}
        emails={[]}
        planId={assignPlanId ?? undefined}
        onClose={() => setAssignPlanId(null)}
      />
      {codePlan && <ActivationCodesModal plan={codePlan} onClose={() => setCodePlan(null)} />}
    </ConfigProvider>
  );
}
