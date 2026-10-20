import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Alert, Checkbox, Form, Input, InputNumber, Modal, Select } from 'antd';
import { StarFilled } from '@ant-design/icons';
import { FormProvider, useForm, useWatch } from 'react-hook-form';

import { FormField, rhfZodValidate } from '@/components/form/rhf';
import SelectAllClearButtons from '@/components/form/SelectAllClearButtons';
import { useRuleTemplatesQuery } from '@/api/queries/useRuleTemplates';
import type { PlanSummary } from '@/generated/zod';
import { PlanFormSchema, type PlanFormValues } from '@/schemas/plan';
import { usePlanNodeOptions } from '@/api/queries/usePlanNodeOptions';
import { PLAN_TERMS, termLabel } from './planText';

// reapplyLimits rides along in the form so reopening the modal resets it too.
type PlanFormState = PlanFormValues & { reapplyLimits: boolean };

function initialState(plan: PlanSummary | null): PlanFormState {
  return {
    name: plan?.name ?? '',
    limitIp: plan?.limitIp ?? 0,
    remark: plan?.remark ?? '',
    templateId: plan?.templateId ?? 0,
    inboundIds: [...(plan?.inboundIds ?? [])],
    nodeKeys: plan?.nodeKeys ? [...plan.nodeKeys] : undefined,
    proxyGroups: (plan?.proxyGroups ?? []).map((group) => ({
      name: group.name,
      inboundIds: [...group.inboundIds],
      nodeKeys: group.nodeKeys ? [...group.nodeKeys] : undefined,
    })),
    termMonths: [...(plan?.termMonths ?? [])],
    reapplyLimits: false,
  };
}

interface PlanFormModalProps {
  open: boolean;
  plan: PlanSummary | null;
  onClose: () => void;
  onConfirm: (values: PlanFormValues, reapplyLimits: boolean) => Promise<void> | void;
}

export default function PlanFormModal({ open, plan, onClose, onConfirm }: PlanFormModalProps) {
  const { t } = useTranslation();
  const methods = useForm<PlanFormState>({ defaultValues: initialState(plan) });
  const [saving, setSaving] = useState(false);
  const isEdit = plan != null;
  const members = plan?.memberCount ?? 0;

  useEffect(() => {
    if (open) methods.reset(initialState(plan));
  }, [open, plan, methods]);

  const inboundIds = useWatch({ control: methods.control, name: 'inboundIds' });
  const nodeKeys = useWatch({ control: methods.control, name: 'nodeKeys' });
  const templateId = useWatch({ control: methods.control, name: 'templateId' });
  const proxyGroups = useWatch({ control: methods.control, name: 'proxyGroups' }) ?? [];
  const { templates } = useRuleTemplatesQuery();
  const defaultTemplate = templates.find((tpl) => tpl.isDefault);
  const selectedTemplate = templateId
    ? templates.find((tpl) => tpl.id === templateId)
    : defaultTemplate;
  const groupNames =
    selectedTemplate?.groups ??
    (templateId === plan?.templateId ? plan?.proxyGroupNames : undefined) ??
    [];
  const templateOptions = [
    {
      value: 0,
      label: defaultTemplate
        ? t('pages.plans.templateDefault', { name: defaultTemplate.name })
        : t('pages.plans.templateNoDefault'),
    },
    ...templates.map((tpl) => ({
      value: tpl.id,
      label: tpl.isDefault ? (
        <span>
          {tpl.name} <StarFilled className="plan-template-star" />
        </span>
      ) : (
        tpl.name
      ),
    })),
  ];
  const nodeQuery = usePlanNodeOptions();
  const nodes = nodeQuery.data ?? [];
  const rawSelectedKeys =
    nodeKeys ??
    nodes.filter((node) => (inboundIds ?? []).includes(node.inboundId)).map((node) => node.key);
  // Explicit choices and runtime relay grants are separate. A relay can also
  // be deliberately selected as a direct node in the same plan.
  const selectedKeys = [
    ...new Set(rawSelectedKeys.filter((key) => nodes.some((node) => node.key === key))),
  ];
  const nodeOptions = nodes.map((node) => ({ label: node.label, value: node.key }));
  const assignableNodeOptions = nodeOptions.filter((option) => selectedKeys.includes(option.value));
  const idsForKeys = (keys: string[], includeRelayDependencies = false) => {
    const ids = new Set<number>(
      nodes.filter((node) => keys.includes(node.key)).map((node) => node.inboundId),
    );
    if (includeRelayDependencies) {
      nodes
        .filter((node) => keys.includes(node.key) && node.relayInboundId > 0)
        .forEach((node) => ids.add(node.relayInboundId));
    }
    return [...ids];
  };
  const assignedKeys = (name: string) => {
    const group = proxyGroups.find((item) => item.name === name);
    const keys =
      group?.nodeKeys ??
      (group
        ? nodes.filter((node) => group.inboundIds.includes(node.inboundId)).map((node) => node.key)
        : selectedKeys);
    return keys.filter((key) => selectedKeys.includes(key));
  };
  function selectNodes(next: string[]) {
    const keys = new Set(next);
    const visibleKeys = [...keys];
    methods.setValue('nodeKeys', visibleKeys, { shouldDirty: true });
    methods.setValue('inboundIds', idsForKeys(visibleKeys, true), { shouldDirty: true });
  }

  async function onFinish({ reapplyLimits, ...values }: PlanFormState) {
    if (!nodeQuery.isSuccess) return;
    setSaving(true);
    try {
      const groups = groupNames.map((name) => {
        const keys = assignedKeys(name);
        return { name, inboundIds: idsForKeys(keys), nodeKeys: keys };
      });
      const parsed = PlanFormSchema.parse({
        ...values,
        inboundIds: idsForKeys(selectedKeys, true),
        nodeKeys: selectedKeys,
        proxyGroups: groups,
      });
      await onConfirm(parsed, isEdit && members > 0 && reapplyLimits);
    } finally {
      setSaving(false);
    }
  }

  return (
    <Modal
      open={open}
      title={isEdit ? t('pages.plans.edit') : t('pages.plans.add')}
      okText={isEdit ? t('save') : t('create')}
      cancelText={t('cancel')}
      confirmLoading={saving}
      okButtonProps={{ disabled: !nodeQuery.isSuccess }}
      mask={{ closable: false }}
      width="600px"
      onOk={methods.handleSubmit(onFinish)}
      onCancel={onClose}
    >
      <FormProvider {...methods}>
        <Form layout="vertical">
          <FormField
            label={t('pages.plans.name')}
            name="name"
            required
            rules={{ validate: rhfZodValidate(PlanFormSchema.shape.name) }}
          >
            <Input maxLength={64} />
          </FormField>

          <FormField
            label={t('pages.clients.limitIp')}
            name="limitIp"
            tooltip={t('pages.plans.zeroUnlimited')}
          >
            <InputNumber min={0} precision={0} style={{ width: '100%' }} />
          </FormField>

          <FormField
            label="计费周期"
            name="termMonths"
            extra="勾选后，续期和激活码只能用这些周期；都不勾表示不限。"
          >
            <Checkbox.Group
              options={PLAN_TERMS.map((months) => ({ value: months, label: termLabel(months) }))}
            />
          </FormField>

          {nodeQuery.isError && <Alert type="error" title="节点加载失败，请刷新后重试" />}
          <Form.Item
            label={t('pages.plans.servers')}
            htmlFor="plan-node-options"
            extra="直连和中转可独立选择；中转版用于 Clash/Mihomo 订阅，并自动保留所需的中转入口。"
          >
            <Select<string[]>
              id="plan-node-options"
              mode="multiple"
              options={nodeOptions}
              value={selectedKeys}
              onChange={selectNodes}
              loading={nodeQuery.isPending}
              disabled={!nodeQuery.isSuccess}
              listHeight={240}
              placeholder={t('pages.plans.noServers')}
              showSearch={{ optionFilterProp: 'label' }}
            />
          </Form.Item>
          {nodeQuery.isSuccess && (
            <SelectAllClearButtons
              options={nodeOptions}
              value={selectedKeys}
              onChange={selectNodes}
            />
          )}

          {groupNames.length > 0 && (
            <div className="plan-proxy-groups">
              <div className="plan-proxy-groups-title">节点代理组</div>
              <div className="plan-proxy-groups-hint">
                规则模板只定义代理组；这里决定本套餐的节点进入哪些组。
              </div>
              {groupNames.map((name) => {
                const assigned = assignedKeys(name);
                return (
                  <Form.Item label={name} key={name}>
                    <Select<string[]>
                      aria-label={name}
                      mode="multiple"
                      options={assignableNodeOptions}
                      value={assigned}
                      maxTagCount="responsive"
                      listHeight={200}
                      placeholder="选择节点"
                      showSearch={{ optionFilterProp: 'label' }}
                      onChange={(next) => {
                        const current = methods.getValues('proxyGroups') ?? [];
                        const nextGroups = current.filter((group) => group.name !== name);
                        nextGroups.push({ name, inboundIds: idsForKeys(next), nodeKeys: next });
                        methods.setValue('proxyGroups', nextGroups, { shouldDirty: true });
                      }}
                    />
                  </Form.Item>
                );
              })}
            </div>
          )}

          <FormField
            label={t('pages.plans.template')}
            name="templateId"
            tooltip={t('pages.plans.templateHint')}
          >
            <Select options={templateOptions} />
          </FormField>

          <FormField label={t('remark')} name="remark">
            <Input maxLength={256} />
          </FormField>

          {isEdit && members > 0 && (
            <FormField
              name="reapplyLimits"
              valueProp="checked"
              extra={t('pages.plans.serverChangesApply')}
            >
              <Checkbox>{t('pages.plans.reapplyIpLimit', { count: members })}</Checkbox>
            </FormField>
          )}
        </Form>
      </FormProvider>
    </Modal>
  );
}
