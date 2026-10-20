import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Checkbox, Form, InputNumber, Modal, Select, Space, message } from 'antd';

import { useClientRenew } from '@/api/queries/useClientRenew';

interface ClientRenewModalProps {
  open: boolean;
  emails: string[];
  onClose: () => void;
  onRenewed?: () => void;
}

type Unit = 'months' | 'days';

const MAX_MONTHS = 1200;

/** 续期: calendar months or days on top of the later of today and each user's expiry, the usage cleared or kept. */
export default function ClientRenewModal({
  open,
  emails,
  onClose,
  onRenewed,
}: ClientRenewModalProps) {
  const { t } = useTranslation();
  const [messageApi, messageContextHolder] = message.useMessage();
  const renew = useClientRenew();
  const [amount, setAmount] = useState<number | null>(1);
  const [unit, setUnit] = useState<Unit>('months');
  const [resetUsage, setResetUsage] = useState(true);
  const [saving, setSaving] = useState(false);

  // Closed, it starts over: the next renewal opens on a month with the usage cleared.
  function close() {
    setAmount(1);
    setUnit('months');
    setResetUsage(true);
    onClose();
  }

  async function submit() {
    if (!amount || amount < 1) return;
    setSaving(true);
    try {
      const term = unit === 'months' ? { months: amount, days: 0 } : { months: 0, days: amount };
      const msg = await renew({ emails, ...term, resetUsage });
      if (msg?.success) {
        messageApi.success(t('pages.plans.toasts.renewed', { count: emails.length }));
        onRenewed?.();
        close();
      }
    } finally {
      setSaving(false);
    }
  }

  return (
    <Modal
      open={open}
      title={
        emails.length === 1
          ? t('pages.clients.renewOneTitle', { email: emails[0] })
          : t('pages.clients.renewManyTitle', { count: emails.length })
      }
      okText={t('pages.plans.renew')}
      cancelText={t('cancel')}
      okButtonProps={{ disabled: !amount || amount < 1 }}
      confirmLoading={saving}
      width={440}
      onOk={submit}
      onCancel={close}
    >
      {messageContextHolder}
      <Form layout="vertical" component="div">
        <Form.Item
          label={t('pages.clients.renewAdd')}
          htmlFor="client-renew-amount"
          extra={t('pages.clients.renewHint')}
        >
          <Space.Compact style={{ display: 'flex' }}>
            <InputNumber
              id="client-renew-amount"
              min={1}
              max={unit === 'months' ? MAX_MONTHS : 36500}
              precision={0}
              value={amount}
              onChange={(value) => setAmount(value)}
              style={{ flex: 1 }}
            />
            <Select<Unit>
              id="client-renew-unit"
              aria-label={t('pages.settings.notifyTime.unit')}
              value={unit}
              onChange={(next) => {
                setUnit(next);
                if (next === 'months' && amount && amount > MAX_MONTHS) setAmount(MAX_MONTHS);
              }}
              options={[
                { value: 'months', label: t('pages.plans.monthsUnit') },
                { value: 'days', label: t('pages.plans.daysUnit') },
              ]}
              style={{ width: 120 }}
            />
          </Space.Compact>
        </Form.Item>
        <Checkbox checked={resetUsage} onChange={(event) => setResetUsage(event.target.checked)}>
          {t('pages.plans.resetTraffic')}
        </Checkbox>
      </Form>
    </Modal>
  );
}
