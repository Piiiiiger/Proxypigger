import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Alert,
  Button,
  Form,
  Input,
  InputNumber,
  Modal,
  Popconfirm,
  Select,
  Space,
  Table,
  Tag,
  Typography,
  message,
} from 'antd';
import { FormProvider, useForm, useWatch } from 'react-hook-form';

import type { PlanSummary } from '@/generated/zod';
import { useActivationCodes } from '@/api/queries/useActivationCodes';
import { FormField, rhfZodValidate } from '@/components/form/rhf';
import { ActivationCodeFormSchema, type ActivationCodeFormValues } from '@/schemas/activationCode';
import { ClipboardManager, SizeFormatter } from '@/utils';
import { termLabel } from './planText';

// The server works out when a code lapses, calendar months included; this counts down to it.
export function activationCodeExpiry(code: { expiresAt: number }, now: number) {
  const { expiresAt } = code;
  return {
    expiresAt,
    remainingDays: expiresAt ? Math.max(0, Math.ceil((expiresAt - now) / 86_400_000)) : 0,
    expired: expiresAt > 0 && now >= expiresAt,
  };
}

export default function ActivationCodesModal({
  plan,
  onClose,
}: {
  plan: PlanSummary;
  onClose: () => void;
}) {
  const { t, i18n } = useTranslation();
  const codes = useActivationCodes(plan.id);
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    const update = () => setNow(Date.now());
    const timer = window.setInterval(update, 60_000);
    document.addEventListener('visibilitychange', update);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener('visibilitychange', update);
    };
  }, []);
  const [messageApi, contextHolder] = message.useMessage();
  const terms = plan.termMonths ?? [];
  const methods = useForm<ActivationCodeFormValues>({
    defaultValues: {
      count: 1,
      quotaGB: 0,
      validity: terms[0] ?? 1,
      unit: 'months',
      resetDay: 0,
      note: '',
    },
  });
  const unit = useWatch({ control: methods.control, name: 'unit' });

  async function create(values: ActivationCodeFormValues) {
    const reply = await codes.create.mutateAsync({
      planId: plan.id,
      count: values.count,
      totalGB: Math.round(values.quotaGB * 1024 ** 3),
      months: values.unit === 'months' ? values.validity : 0,
      days: values.unit === 'days' ? values.validity : 0,
      resetDay: values.resetDay,
      note: values.note,
    });
    if (reply?.success) messageApi.success(t('pages.plans.codes.created'));
  }

  async function copyUnused() {
    const unused = (codes.data ?? [])
      .filter((code) => code.usedAt === 0 && !activationCodeExpiry(code, Date.now()).expired)
      .map((code) => code.code)
      .join('\n');
    if (unused && (await ClipboardManager.copyText(unused))) messageApi.success(t('copied'));
  }

  return (
    <Modal
      open
      title={`${t('pages.plans.codes.title')} · ${plan.name}`}
      onCancel={onClose}
      footer={null}
      width={760}
    >
      {contextHolder}
      <FormProvider {...methods}>
        <Form layout="vertical" onFinish={methods.handleSubmit(create)}>
          <div className="plan-form-row">
            <FormField
              name="count"
              label={t('pages.plans.codes.count')}
              rules={{ validate: rhfZodValidate(ActivationCodeFormSchema.shape.count) }}
            >
              <InputNumber min={1} max={200} precision={0} style={{ width: '100%' }} />
            </FormField>
            <FormField
              name="quotaGB"
              label={t('pages.plans.quota')}
              tooltip={t('pages.plans.zeroUnlimited')}
              rules={{ validate: rhfZodValidate(ActivationCodeFormSchema.shape.quotaGB) }}
            >
              <InputNumber min={0} precision={2} suffix="GB" style={{ width: '100%' }} />
            </FormField>
            <FormField
              name="validity"
              label={t('pages.plans.codes.validity')}
              rules={{ validate: rhfZodValidate(ActivationCodeFormSchema.shape.validity) }}
            >
              {terms.length > 0 ? (
                <Select
                  options={terms.map((months) => ({ value: months, label: termLabel(months) }))}
                />
              ) : (
                <InputNumber
                  min={0}
                  max={unit === 'months' ? 1200 : 36500}
                  precision={0}
                  style={{ width: '100%' }}
                />
              )}
            </FormField>
            {terms.length === 0 && (
              <FormField
                name="unit"
                label={t('pages.settings.notifyTime.unit')}
                onAfterChange={(next) => {
                  if (next === 'months' && methods.getValues('validity') > 1200) {
                    methods.setValue('validity', 1200);
                  }
                }}
              >
                <Select
                  id="activation-code-unit"
                  options={[
                    { value: 'months', label: t('pages.plans.monthsUnit') },
                    { value: 'days', label: t('pages.plans.daysUnit') },
                  ]}
                />
              </FormField>
            )}
            <FormField
              name="resetDay"
              label={t('pages.plans.codes.resetDay')}
              rules={{ validate: rhfZodValidate(ActivationCodeFormSchema.shape.resetDay) }}
            >
              <InputNumber min={0} max={31} precision={0} style={{ width: '100%' }} />
            </FormField>
          </div>
          <Typography.Paragraph type="secondary">
            {t('pages.plans.codes.zeroHint')}
          </Typography.Paragraph>
          <FormField name="note" label={t('remark')}>
            <Input maxLength={256} />
          </FormField>
          <Space style={{ marginBottom: 16 }}>
            <Button htmlType="submit" type="primary" loading={codes.create.isPending}>
              {t('pages.plans.codes.create')}
            </Button>
            <Button
              onClick={copyUnused}
              disabled={
                !codes.data?.some(
                  (code) => !code.usedAt && !activationCodeExpiry(code, now).expired,
                )
              }
            >
              {t('pages.plans.codes.copyAll')}
            </Button>
          </Space>
        </Form>
      </FormProvider>
      {codes.isError && (
        <Alert
          type="error"
          showIcon
          title={t('subscription.portal.loadFailed')}
          action={<Button onClick={() => codes.refetch()}>{t('refresh')}</Button>}
        />
      )}
      <Table
        rowKey="id"
        size="small"
        dataSource={codes.data ?? []}
        loading={codes.isFetching}
        scroll={{ x: 580 }}
        pagination={{ pageSize: 10 }}
        columns={[
          {
            title: t('subscription.portal.code'),
            dataIndex: 'code',
            render: (code: string) => (
              <Typography.Text code copyable>
                {code}
              </Typography.Text>
            ),
          },
          {
            title: t('pages.plans.quota'),
            key: 'grant',
            render: (_, row) => (
              <div>
                {row.totalGB ? SizeFormatter.sizeFormat(row.totalGB) : t('unlimited')} ·{' '}
                {row.months || row.days
                  ? row.usedAt
                    ? row.months
                      ? `${row.months} ${t('pages.plans.monthsUnit')}`
                      : `${row.days} ${t('pages.plans.daysUnit')}`
                    : t('pages.plans.codes.remainingDays', {
                        days: activationCodeExpiry(row, now).remainingDays,
                      })
                  : t('pages.plans.permanent')}
                {!row.usedAt && row.expiresAt > 0 && (
                  <Typography.Text type="secondary" style={{ display: 'block', fontSize: 12 }}>
                    {t('pages.plans.codes.expiresAt')}{' '}
                    <time
                      dateTime={new Date(activationCodeExpiry(row, now).expiresAt).toISOString()}
                    >
                      {new Date(activationCodeExpiry(row, now).expiresAt).toLocaleString(
                        i18n.language,
                      )}
                    </time>
                  </Typography.Text>
                )}
              </div>
            ),
          },
          {
            title: t('status'),
            key: 'status',
            render: (_, row) => (
              <div>
                <Tag
                  color={
                    row.usedAt
                      ? undefined
                      : activationCodeExpiry(row, now).expired
                        ? 'red'
                        : 'green'
                  }
                >
                  {row.usedAt
                    ? t('pages.plans.codes.used', { user: row.usedBy })
                    : activationCodeExpiry(row, now).expired
                      ? t('pages.plans.codes.expired')
                      : t('pages.plans.codes.unused')}
                </Tag>
                {row.usedAt > 0 && (
                  <Typography.Text type="secondary" style={{ display: 'block', fontSize: 12 }}>
                    <time dateTime={new Date(row.usedAt).toISOString()}>
                      {new Date(row.usedAt).toLocaleString(i18n.language)}
                    </time>
                  </Typography.Text>
                )}
              </div>
            ),
          },
          { title: t('remark'), dataIndex: 'note', ellipsis: true },
          {
            key: 'delete',
            render: (_, row) => (
              <Popconfirm
                title={t('pages.plans.codes.deleteConfirm')}
                onConfirm={() => codes.remove.mutateAsync(row.id)}
              >
                <Button danger type="text" loading={codes.remove.isPending}>
                  {t('delete')}
                </Button>
              </Popconfirm>
            ),
          },
        ]}
      />
    </Modal>
  );
}
