# AI 防滥用巡查部署包

只在选定的重要节点启用 Codex 日志巡查，其余节点保持原有固定规则。

- 默认关闭；默认至少间隔 12 小时，每 UTC 日最多两次模型调用，失败不自动重试。
- 本机汇总并限制输入大小；无可用连接记录时跳过模型，输出证据不足。
- 报告包含具体依据、误报解释、局限和复核建议；Codex 本身不执行操作。可由面板独立控制器按下述明确策略处理新报告。

完整安装、日志前提、成本限制和停用步骤见 [部署说明](ai-abuse-patrol/references/deployment.md)。

**Pigger Agent 默认关闭访问日志。** 本次新增可选的 `accessAuditLog` 有界 JSONL 出口，需要更新 Agent 并在选定节点单独配置；只安装 skill 不会启用采集。具体步骤和覆盖限制见部署说明。

本地验证（不调用模型）：

```sh
python3 -m unittest discover -s deploy/ai-abuse-patrol/tests -v
```
