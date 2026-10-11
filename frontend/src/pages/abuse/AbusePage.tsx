import { useEffect, useId, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Alert,
  Button,
  Card,
  ConfigProvider,
  Form,
  InputNumber,
  Layout,
  Result,
  Select,
  Spin,
  Table,
  Tag,
  message,
} from 'antd';
import { UnlockOutlined } from '@ant-design/icons';
import { FormProvider } from 'react-hook-form';

import AppNav from '@/layouts/AppNav';
import { PageHeader, SettingListItem } from '@/components/ui';
import { useTheme } from '@/hooks/useTheme';
import { setMessageInstance } from '@/utils/messageBus';
import { FormField, useZodForm } from '@/components/form/rhf';
import { useAbuseMutations, useAbuseOverview, type AbuseMode } from '@/api/queries/useAbuse';
import type { AbuseEventView, AbuseServer, AbuseSettings, BanRecord } from '@/generated/zod';
import {
  ABUSE_MODES,
  ABUSE_OUTCOMES,
  RULE_ACTIONS,
  SIGNUP_ACTIONS,
  banOutcome,
  banTime,
  eventSamples,
  unixClock,
} from '@/lib/abuse';
import { AbuseSettingsFormSchema, type AbuseSettingsFormValues } from '@/schemas/abuse';
import './AbusePage.css';

type RuleAction = keyof AbuseSettingsFormValues['actions'];
type Threshold = keyof AbuseSettingsFormValues['rules'];

// Each rule with its thresholds, as the settings form groups them.
const RULES: { action: RuleAction; title: string; hint?: string; fields: [Threshold, string][] }[] =
  [
    {
      action: 'spam',
      title: '垃圾邮件（25 端口）',
      fields: [
        ['spamAttempts', '尝试次数'],
        ['spamWindowMin', '窗口（分钟）'],
      ],
    },
    {
      action: 'bt',
      title: 'BT 下载',
      fields: [
        ['btAttempts', '连接次数'],
        ['btWindowMin', '窗口（分钟）'],
      ],
    },
    {
      action: 'scan',
      title: '端口扫描',
      hint: '只计直接连 IP 的连接，按域名访问不算。',
      fields: [
        ['scanIps', '不同 IP 数'],
        ['scanPortsOnIp', '同一 IP 的端口数'],
        ['scanSensitiveIps', '远程登录/数据库端口的 IP 数'],
        ['scanWindowMin', '窗口（分钟）'],
      ],
    },
    {
      action: 'flood',
      title: '网络攻击（每分钟）',
      fields: [
        ['floodPerDest', '同一目标连接数'],
        ['floodTotal', '总连接数'],
      ],
    },
    {
      action: 'crawler',
      title: '爬虫',
      fields: [
        ['crawlerConns', '连接数'],
        ['crawlerHosts', '不同网站数'],
        ['crawlerWindowMin', '窗口（分钟）'],
        ['crawlerWindows', '连续窗口数'],
      ],
    },
    {
      action: 'speedtest',
      title: '反复测速',
      fields: [
        ['speedTestsPerHour', '每小时次数'],
        ['speedTestsPerDay', '每天次数'],
        ['speedTestGapMin', '两次测速至少间隔（分钟）'],
      ],
    },
    {
      action: 'fullspeed',
      title: '长时间满速',
      hint: '到「提醒」时长只提醒用户；选「封禁」时，到「封禁」时长才计一次违规。',
      fields: [
        ['fullSpeedMbps', '速度（Mbps）'],
        ['fullSpeedWarnMin', '提醒（分钟）'],
        ['fullSpeedStrikeMin', '封禁（分钟）'],
      ],
    },
    {
      action: 'register',
      title: '批量注册（AI / Google / 微软账号）',
      hint: '按分钟计：某分钟里连过平台的登录/注册服务器（auth.openai.com、accounts.google.com、signup.live.com）就算 1 分钟，超过上限记一次。看不出是登录还是注册、注册了几个账号；Google 在浏览器和手机后台也常连，所以上限宽、默认不按天算。',
      fields: [
        ['openaiAuthMinPerHour', 'OpenAI 每小时分钟数'],
        ['openaiAuthMinPerDay', 'OpenAI 每天分钟数'],
        ['googleAuthMinPerHour', 'Google 每小时分钟数'],
        ['googleAuthMinPerDay', 'Google 每天分钟数'],
        ['microsoftSignupMinPerHour', '微软每小时分钟数'],
        ['microsoftSignupMinPerDay', '微软每天分钟数'],
      ],
    },
  ];

// Outcomes that cost the user something stand out; recorded-only hits keep the plain tag.
const OUTCOME_COLORS: Record<string, string> = {
  warned: 'orange',
  banned: 'red',
  locked: 'red',
  held: 'red',
  blocked: 'red',
};

// thresholdMax is the schema's own bound, so the input stops where saving would fail.
function thresholdMax(name: Threshold): number | undefined {
  return AbuseSettingsFormSchema.shape.rules.shape[name].maxValue ?? undefined;
}

function RulesCard({
  settings,
  onSave,
}: {
  settings: AbuseSettings;
  onSave: (next: AbuseSettings) => Promise<boolean>;
}) {
  const methods = useZodForm(AbuseSettingsFormSchema, {
    defaultValues: settings as AbuseSettingsFormValues,
  });
  const formId = useId();
  const [saving, setSaving] = useState(false);
  async function submit(values: AbuseSettingsFormValues) {
    setSaving(true);
    try {
      if (await onSave(values)) methods.reset(values);
    } finally {
      setSaving(false);
    }
  }
  // The form sits inside the card body, as the settings page's rows sit inside its tabs:
  // the card's own border token then stays off the row dividers.
  return (
    <Card
      size="small"
      title="规则设置"
      extra={
        <Button
          type="primary"
          htmlType="submit"
          form={formId}
          loading={saving}
          disabled={!methods.formState.isDirty}
        >
          保存设置
        </Button>
      }
    >
      <FormProvider {...methods}>
        <Form id={formId} layout="vertical" onFinish={methods.handleSubmit(submit)}>
          <Alert
            className="abuse-rules-note"
            type="info"
            showIcon
            title="阈值对所有开启检测的服务器生效，0 表示关闭该项。「处理」只在设为「封禁」模式的服务器上生效；「观察」模式的服务器一律只记录。"
          />
          {RULES.map((rule) => (
            <SettingListItem
              key={rule.action}
              paddings="small"
              title={rule.title}
              description={rule.hint}
            >
              <div className="abuse-rule-fields" role="group">
                <FormField name={`actions.${rule.action}`} label="处理">
                  <Select options={RULE_ACTIONS.map((a) => ({ value: a.value, label: a.label }))} />
                </FormField>
                {rule.fields.map(([name, label]) => (
                  <FormField key={name} name={`rules.${name}`} label={label}>
                    <InputNumber min={0} max={thresholdMax(name)} style={{ width: '100%' }} />
                  </FormField>
                ))}
              </div>
            </SettingListItem>
          ))}
          <SettingListItem
            paddings="small"
            title="注册保护（用户页面）"
            description="同一网络（IPv4 /24 或 IPv6 /48）24 小时内注册达到上限时通知你；选「阻止注册」时，该网络 24 小时内不能再注册。"
          >
            <div className="abuse-rule-fields" role="group">
              <FormField name="signup.action" label="处理">
                <Select options={SIGNUP_ACTIONS.map((a) => ({ value: a.value, label: a.label }))} />
              </FormField>
              <FormField name="signup.limit" label="同一网络 24 小时内注册数">
                <InputNumber min={0} style={{ width: '100%' }} />
              </FormField>
            </div>
          </SettingListItem>
        </Form>
      </FormProvider>
    </Card>
  );
}

export default function AbusePage() {
  const { t } = useTranslation();
  const { isDark, isUltra, antdThemeConfig } = useTheme();
  const { data, error, isLoading, isFetching, refetch, dataUpdatedAt } = useAbuseOverview();
  const { setMode, saveSettings, lift } = useAbuseMutations();
  const [messageApi, messageContextHolder] = message.useMessage();
  useEffect(() => {
    setMessageInstance(messageApi);
  }, [messageApi]);
  // Time left is shown as of the last poll, which repeats every 15 seconds.
  const now = dataUpdatedAt;

  const pageClass = useMemo(() => {
    const classes = ['abuse-page'];
    if (isDark) classes.push('is-dark');
    if (isUltra) classes.push('is-ultra');
    return classes.join(' ');
  }, [isDark, isUltra]);

  async function changeMode(server: AbuseServer, mode: AbuseMode) {
    const msg = await setMode(server.nodeId, mode);
    const label = ABUSE_MODES.find((m) => m.value === mode)?.label;
    if (msg?.success) messageApi.success(`${server.name}：已改为「${label}」`);
  }

  async function liftBan(email: string) {
    const msg = await lift(email);
    if (msg?.success) messageApi.success(`已解除 ${email} 的封禁`);
  }

  async function storeSettings(next: AbuseSettings) {
    const msg = await saveSettings(next);
    if (msg?.success) messageApi.success('设置已保存');
    return !!msg?.success;
  }

  return (
    <ConfigProvider theme={antdThemeConfig}>
      {messageContextHolder}
      <Layout className={pageClass}>
        <AppNav />
        <Layout className="content-shell">
          <Layout.Content id="content-layout" className="content-area">
            <PageHeader
              title="防滥用"
              description="只看连接和流量，不看内容。默认关闭；先在少数服务器上开「观察」一周，确认没有误报再改为「封禁」。每次违规封禁 30 分钟，30 天内第 4 次停用账号，直到你解除；同时在线 IP 超限不计入违规。"
            />
            <Spin spinning={isLoading} delay={200} description={t('loading')} size="large">
              {isLoading ? (
                <div className="loading-spacer" />
              ) : !data ? (
                <Result
                  status="error"
                  title={t('somethingWentWrong')}
                  subTitle={error?.message}
                  extra={
                    <Button type="primary" loading={isFetching} onClick={() => refetch()}>
                      {t('refresh')}
                    </Button>
                  }
                />
              ) : (
                <div className="abuse-body">
                  {error && <Alert type="error" showIcon title={error.message} />}
                  <Card size="small" title="服务器">
                    <Table<AbuseServer>
                      size="small"
                      rowKey="nodeId"
                      pagination={false}
                      scroll={{ x: 'max-content' }}
                      dataSource={data.servers}
                      columns={[
                        { title: '服务器', dataIndex: 'name' },
                        {
                          title: '检测',
                          render: (_, server) => (
                            <Select<AbuseMode>
                              aria-label={`${server.name} 的检测模式`}
                              value={server.mode as AbuseMode}
                              style={{ width: 120 }}
                              options={ABUSE_MODES.map((m) => ({ value: m.value, label: m.label }))}
                              onChange={(mode) => void changeMode(server, mode)}
                            />
                          ),
                        },
                        {
                          title: '状态',
                          render: (_, server) =>
                            server.mode === 'off' ? (
                              '—'
                            ) : server.capable ? (
                              <Tag color="green">检测中</Tag>
                            ) : (
                              <Tag color="red">节点 agent 未连接或需要更新</Tag>
                            ),
                        },
                      ]}
                    />
                  </Card>
                  <Card size="small" title={`封禁中（${data.bans.length}）`}>
                    <Table<BanRecord>
                      size="small"
                      rowKey="id"
                      pagination={false}
                      scroll={{ x: 'max-content' }}
                      dataSource={data.bans}
                      locale={{ emptyText: '没有正在生效的封禁' }}
                      columns={[
                        { title: '用户', dataIndex: 'email' },
                        { title: '原因', dataIndex: 'reason' },
                        {
                          title: '第几次',
                          render: (_, b) =>
                            b.expiresAt === 0 ? <Tag color="red">停用</Tag> : b.strike,
                        },
                        { title: '开始', render: (_, b) => banTime(b) },
                        { title: '状态', render: (_, b) => banOutcome(b, now) },
                        {
                          title: '操作',
                          render: (_, b) => (
                            <Button
                              type="text"
                              icon={<UnlockOutlined />}
                              onClick={() => void liftBan(b.email)}
                            >
                              解除
                            </Button>
                          ),
                        },
                      ]}
                    />
                  </Card>
                  <Card size="small" title="最近记录">
                    <Table<AbuseEventView>
                      size="small"
                      rowKey="id"
                      scroll={{ x: 'max-content' }}
                      dataSource={data.events}
                      locale={{ emptyText: '还没有检测记录' }}
                      pagination={{ pageSize: 20, hideOnSinglePage: true }}
                      columns={[
                        { title: '时间', render: (_, e) => unixClock(e.at) },
                        { title: '用户', dataIndex: 'email' },
                        { title: '来源', dataIndex: 'server' },
                        { title: '规则', dataIndex: 'label' },
                        { title: '依据', dataIndex: 'evidence' },
                        {
                          title: '样本',
                          render: (_, e) => eventSamples(e.samples).join('、') || '—',
                        },
                        {
                          title: '处理',
                          render: (_, e) => (
                            <Tag color={OUTCOME_COLORS[e.action]}>
                              {ABUSE_OUTCOMES[e.action] ?? e.action}
                            </Tag>
                          ),
                        },
                      ]}
                    />
                  </Card>
                  <RulesCard settings={data.settings} onSave={storeSettings} />
                </div>
              )}
            </Spin>
          </Layout.Content>
        </Layout>
      </Layout>
    </ConfigProvider>
  );
}
