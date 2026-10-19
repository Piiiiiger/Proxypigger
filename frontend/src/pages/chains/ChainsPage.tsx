import { useCallback, useEffect, useMemo, useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { keys } from '@/api/queryKeys';
import {
  Alert,
  Button,
  Card,
  ConfigProvider,
  Form,
  Input,
  Layout,
  Modal,
  Select,
  Space,
  Switch,
  Table,
  Tag,
  message,
} from 'antd';
import { DeleteOutlined, EditOutlined, PlusOutlined, LinkOutlined } from '@ant-design/icons';
import AppNav from '@/layouts/AppNav';
import { PageHeader } from '@/components/ui';
import { useTheme } from '@/hooks/useTheme';
import { HttpUtil } from '@/utils';
import './ChainsPage.css';

type Inbound = { id: number; remark?: string; protocol?: string; nodeId?: number | null };
type Chain = {
  id: number;
  name: string;
  targetInboundId: number;
  relayInboundId: number;
  enabled: boolean;
  relayName?: string;
};
const JSON_HEADERS = { headers: { 'Content-Type': 'application/json' } } as const;

function unwrap<T>(msg: { success?: boolean; msg?: string; obj?: T | null }): T {
  if (!msg.success) throw new Error(msg.msg || '请求失败');
  return (msg.obj ?? []) as T;
}

export default function ChainsPage() {
  const queryClient = useQueryClient();
  const { isDark, isUltra, antdThemeConfig } = useTheme();
  const [chains, setChains] = useState<Chain[]>([]);
  const [inbounds, setInbounds] = useState<Inbound[]>([]);
  const [checking, setChecking] = useState<number | null>(null);
  const [checks, setChecks] = useState<Record<number, string>>({});
  const check = async (row: Chain) => {
    setChecking(row.id);
    try {
      const result = unwrap<{ message: string }>(
        await HttpUtil.post(`/panel/api/proxyChains/check/${row.id}`, {}, JSON_HEADERS),
      );
      setChecks((old) => ({ ...old, [row.id]: result.message }));
      messageApi.success(result.message);
    } catch (error) {
      const text = error instanceof Error ? error.message : '检查失败';
      setChecks((old) => ({ ...old, [row.id]: text }));
      messageApi.error(text);
    } finally {
      setChecking(null);
    }
  };
  const [open, setOpen] = useState(false);
  const [editing, setEditing] = useState<Chain | null>(null);
  const [form] = Form.useForm<Chain>();
  const targetId = Form.useWatch('targetInboundId', form);
  const relayId = Form.useWatch('relayInboundId', form);
  const relayName = Form.useWatch('relayName', form);
  const [messageApi, contextHolder] = message.useMessage();
  const inboundName = useMemo(
    () => new Map(inbounds.map((item) => [item.id, item.remark || `入站 #${item.id}`])),
    [inbounds],
  );
  const targetLabel = inboundName.get(targetId) || '目标节点';
  const relayLabel = inboundName.get(relayId) || '中转节点';
  const defaultRelayName = targetLabel + '（中转·' + relayLabel + '）';
  const protocolOf = useMemo(
    () => new Map(inbounds.map((item) => [item.id, item.protocol || ''])),
    [inbounds],
  );
  const socksRelay = protocolOf.get(relayId) === 'mixed';
  const load = useCallback(async () => {
    try {
      const [chainMsg, inboundMsg] = await Promise.all([
        HttpUtil.get<Chain[]>('/panel/api/proxyChains/list', undefined, { silent: true }),
        HttpUtil.get<Inbound[]>('/panel/api/inbounds/list', undefined, { silent: true }),
      ]);
      setChains(unwrap<Chain[]>(chainMsg));
      setInbounds(unwrap<Inbound[]>(inboundMsg));
    } catch (error) {
      messageApi.error(error instanceof Error ? error.message : '加载失败');
    }
  }, [messageApi]);
  useEffect(() => {
    // Fetch the initial snapshot from the panel API.
    // oxlint-disable-next-line react/set-state-in-effect
    void load();
  }, [load]);
  const choices = inbounds.map((item) => ({
    value: item.id,
    label: `${item.remark || `入站 #${item.id}`} · ${item.protocol || ''}`,
  }));
  const save = async () => {
    try {
      const values = await form.validateFields();
      const msg = editing
        ? await HttpUtil.post(`/panel/api/proxyChains/update/${editing.id}`, values, JSON_HEADERS)
        : await HttpUtil.post('/panel/api/proxyChains/add', values, JSON_HEADERS);
      unwrap(msg);
      await queryClient.invalidateQueries({ queryKey: keys.plans.root() });
      setOpen(false);
      setEditing(null);
      form.resetFields();
      await load();
      messageApi.success('链式配置已保存');
    } catch (error) {
      if (error instanceof Error) messageApi.error(error.message);
    }
  };
  const edit = (chain: Chain) => {
    setEditing(chain);
    form.resetFields();
    form.setFieldsValue({
      ...chain,
      relayName: chain.relayName || '',
    });
    setOpen(true);
  };
  const remove = async (chain: Chain) => {
    const msg = await HttpUtil.post(`/panel/api/proxyChains/del/${chain.id}`);
    if (!msg.success) {
      messageApi.error(msg.msg || '删除失败');
      return;
    }
    await load();
    await queryClient.invalidateQueries({ queryKey: keys.plans.root() });
    messageApi.success('已删除');
  };
  return (
    <ConfigProvider theme={antdThemeConfig}>
      {contextHolder}
      <Layout className={`chains-page${isDark ? ' is-dark' : ''}${isUltra ? ' is-ultra' : ''}`}>
        <AppNav />
        <Layout className="content-shell">
          <Layout.Content id="content-layout" className="content-area">
            <PageHeader
              title="链式管理"
              description="设置目标节点通过中转节点连接，保存后自动写入 Clash/Mihomo 的 dialer-proxy。链路方向：客户端 → 中转节点 → 目标节点。"
            />
            <Card
              className="chains-card"
              title={
                <Space>
                  <LinkOutlined />
                  转发链
                </Space>
              }
              extra={
                <Button
                  type="primary"
                  icon={<PlusOutlined />}
                  onClick={() => {
                    setEditing(null);
                    form.resetFields();
                    form.setFieldsValue({ enabled: true });
                    setOpen(true);
                  }}
                >
                  创建转发链
                </Button>
              }
            >
              <Table
                rowKey="id"
                scroll={{ x: 1000 }}
                dataSource={chains}
                pagination={false}
                columns={[
                  {
                    title: '名称',
                    dataIndex: 'name',
                    render: (value: string) => value || '未命名链路',
                  },
                  {
                    title: '目标节点',
                    render: (_: unknown, row: Chain) => (
                      <Tag color="blue">
                        {inboundName.get(row.targetInboundId) || `#${row.targetInboundId}`}
                      </Tag>
                    ),
                  },
                  {
                    title: '中转节点',
                    render: (_: unknown, row: Chain) => (
                      <Tag color="cyan">
                        {inboundName.get(row.relayInboundId) || `#${row.relayInboundId}`}
                      </Tag>
                    ),
                  },
                  {
                    title: '订阅显示名称',
                    render: (_: unknown, row: Chain) =>
                      protocolOf.get(row.relayInboundId) === 'mixed' ? (
                        <Tag>SOCKS5 · 服务器转发，不进订阅</Tag>
                      ) : (
                        <div className="chain-name-preview">
                          <div>
                            <Tag color="purple">中转</Tag>
                            {row.relayName ||
                              (inboundName.get(row.targetInboundId) || '目标节点') +
                                '（中转·' +
                                (inboundName.get(row.relayInboundId) || '中转节点') +
                                '）'}
                          </div>
                        </div>
                      ),
                  },
                  {
                    title: '配置状态',
                    dataIndex: 'enabled',
                    render: (value: boolean) => (
                      <Tag color={value ? 'green' : 'default'}>{value ? '启用' : '停用'}</Tag>
                    ),
                  },
                  {
                    title: '连通性',
                    render: (_: unknown, row: Chain) => (
                      <span>{checks[row.id] || '未检查（启用不代表可连通）'}</span>
                    ),
                  },
                  {
                    title: '操作',
                    render: (_: unknown, row: Chain) => (
                      <Space>
                        <Button
                          loading={checking === row.id}
                          disabled={checking !== null}
                          onClick={() => void check(row)}
                        >
                          检查链路
                        </Button>
                        <Button type="text" icon={<EditOutlined />} onClick={() => edit(row)} />
                        <Button
                          danger
                          type="text"
                          icon={<DeleteOutlined />}
                          onClick={() => void remove(row)}
                        />
                      </Space>
                    ),
                  },
                ]}
                locale={{ emptyText: '还没有链式配置' }}
              />
            </Card>
          </Layout.Content>
        </Layout>
      </Layout>
      <Modal
        title={editing ? '编辑转发链' : '创建转发链'}
        open={open}
        onCancel={() => setOpen(false)}
        onOk={() => void save()}
        okText="保存"
      >
        <Form form={form} layout="vertical">
          <Form.Item name="name" label="链路名称">
            <Input placeholder="例如：新加坡家宽经奶爸中转" />
          </Form.Item>
          <Form.Item
            name="targetInboundId"
            label="目标节点"
            rules={[{ required: true, message: '请选择目标节点' }]}
          >
            <Select
              showSearch
              optionFilterProp="label"
              options={choices.filter((choice) => choice.value !== relayId)}
              placeholder="用户最终使用的节点"
            />
          </Form.Item>
          <Form.Item
            name="relayInboundId"
            label="中转节点"
            rules={[{ required: true, message: '请选择中转节点' }]}
          >
            <Select
              showSearch
              optionFilterProp="label"
              options={choices.filter((choice) => choice.value !== targetId)}
              placeholder="先连接这个节点"
            />
          </Form.Item>
          {socksRelay ? (
            <Alert
              type="info"
              showIcon
              style={{ marginBottom: 16 }}
              title="SOCKS5 中转由服务器转发：用户登录中转节点的 SOCKS5，中转再以该用户自己的身份连接目标节点，只在目标计费一次。用户需要同时关联这两个节点；这条线路不进入订阅和套餐。"
            />
          ) : (
            <>
              <Form.Item
                name="relayName"
                label="中转显示名称"
                extra="目标节点本身的名称用于直连；这里只设置经过中转后的名称。"
              >
                <Input allowClear maxLength={128} placeholder={defaultRelayName} />
              </Form.Item>
              <div className="chain-name-preview chain-name-preview--form">
                <div>
                  <Tag color="purple">中转</Tag>
                  {relayName?.trim() || defaultRelayName}
                </div>
              </div>
            </>
          )}
          <Form.Item name="enabled" label="启用" valuePropName="checked">
            <Switch />
          </Form.Item>
        </Form>
      </Modal>
    </ConfigProvider>
  );
}
