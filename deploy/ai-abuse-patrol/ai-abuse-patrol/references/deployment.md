# 选择性部署 AI 防滥用巡查

这是单机 opt-in 包。只拷贝到你选择的重要节点；没有主机自动发现、全节点推送、面板开关或远程通知。原来的固定规则继续实时运行；本任务提供事后人工复核报告，不修改代理核心或封禁数据。

## 包含什么

- `SKILL.md`：行为分析方法、证据约束与中文报告格式。
- `scripts/patrol.py`：只读日志采集、本机聚合、输入和调用次数限制、独立 Codex 执行、报告校验和本地保留。
- 包根目录 `config.example.json`、`install.sh`、`systemd/`：每台机器各自安装和配置。

默认 AI 任务关闭。默认最短间隔 12 小时、每 UTC 日最多两次；模型失败、超时和无效报告也占一次名额，不自动重试。安装的 timer 每小时检查一次是否到期，未到期时不读日志、不调用模型；实际周期约 12–13 小时。改为 24 小时、每天一次适合预算更紧的节点。不同节点没有统一账户预算，选中 N 台时默认上限为每天 2N 个 Codex 任务。这是任务次数，不是底层 API 请求数；CLI 自身可能进行网络重试，包装脚本不重新启动失败任务。

输入默认最多 96,000 **UTF-8 字节**（包括 skill 和 policy），不是 token 或美元上限。最多读 64 MiB/100 万行/16 个文件、100 个账号；每个账号只输出前 20 个端口和目标。选择的账号是扫描到的前 100 个，不是全局风险排名。被截断时报告必须说明盲区。单次最长 10 分钟，模型不指定默认值，部署者必须填写其账号可用的 `model`。准确费用取决于模型、输入/输出、账号计费和服务端行为；超时或每天两次并不保证某个美元金额。

## 先确认日志来源

支持两种输入，均是**连接元数据**，不采集内容、密钥或配置文件：

1. `xray`：带时间、来源、目标与 `email` 的 Xray access.log，例如：
   ```text
   2026/10/11 00:10:00.123456 from tcp:198.51.100.7:51234 accepted tcp:example.com:443 [in-443 >> direct] email: alice
   ```
   Xray 文本时间无时区，必须设置 `log_timezone` 为日志实际使用的 IANA 时区（如 `UTC`、`Asia/Singapore`），不能凭服务器显示时间猜测。
2. `jsonl`：由你已有的日志出口提供，一行一条，`at` 必须带时区：
   ```json
   {"at":"2026-10-11T00:10:00Z","user":"alice","source_ip":"198.51.100.7","destination":"example.com","port":443,"protocol":"tcp","status":"accepted"}
   ```
   `source_ip` 可省略；`status` 只能是 `accepted`/`rejected`，`protocol` 是 `tcp`/`udp`。这是部署包的输入契约，并非宣称现有 Pigger Agent 已提供该出口。

支持显式路径与轮转文件通配符，包括 `.gz`。只读打开文件；保留已有轮转配置。避免复制相同内容到多个匹配文件，否则会重复计数。推荐日志至少保留 24–48 小时并有磁盘容量上限；这需要管理员按照实际机器设置，安装脚本不会修改日志策略。

**ProxyPigger Agent：** 默认仍不落盘访问日志。带有本次 `accessAuditLog` 功能的 Agent 可以在**本机** `/etc/pigger-agent/config.json` 中加入：

```json
{"accessAuditLog":"/var/log/pigger-agent-audit/access.jsonl"}
```

这是增加一个字段，不是用上面的片段替换整个配置；保留原有 master、secret、stateDir。管理员需先建立仅 root 和巡查账户可访问的目录（例如 root:ai-abuse-patrol，模式 2750），更新 Agent 并重启一次。默认关闭、固定规则不受影响。异步队列 4096 条，满时丢弃并计数；每文件 8 MiB，保留 7 个备份，总计约 64 MiB，使用 JSONL 格式。只采集带用户身份的 accepted 连接，保留 outbound 区分阻断路由，不保存内容。

相应巡查配置：

```json
{
  "sources": [
    {"format":"jsonl","path":"/var/log/pigger-agent-audit/access.jsonl"},
    {"format":"jsonl","path":"/var/log/pigger-agent-audit/access.jsonl.[1-7]","optional":true}
  ],
  "audit_health_path":"/var/log/pigger-agent-audit/access.jsonl.health.json"
}
```

健康文件每 30 秒更新，包含进程启动时间、队列丢弃和写入错误数；日志容量达到上限会覆盖旧记录，不保证保留 12 小时。旧版 Agent 只有内存固定规则检测，安装巡查包或修改面板日志模板不能自动启用这个出口。面板本机的 `abuse-access.log` 达到 32 MiB 时可能被原检测器清空，也不能保证完整时间覆盖。

Snell 共享凭证没有每用户身份；不能将整个入口流量归因给某个账号。没有带宽计量数据，本包只分析连接数，不判断持续满速。

## systemd 机器安装

需要 Python 3.10+（含 IANA tzdata）、Linux、当前 Codex CLI。Codex 必须支持 `exec --ignore-user-config --ignore-rules --ephemeral --output-schema` 和 `shell_tool` 开关；安装脚本不联网安装或更新 Codex。

Agent 的 Go 时间戳可能包含 1–9 位小数。包装脚本将日志与健康文件中的小数秒归一到 Python 支持的微秒精度，兼容 Ubuntu 22.04 的 Python 3.10；不会把无法解析的时间改成采集时刻。

将完整 `deploy/ai-abuse-patrol/` 拷贝到选定机器，再运行：

```sh
sudo sh install.sh
```

安装到 `/opt/ai-abuse-patrol`；配置为 `/etc/ai-abuse-patrol/config.json`。创建无 sudo 的专用账户 `ai-abuse-patrol`，状态目录 `/var/lib/ai-abuse-patrol`。已有配置不会覆盖，新 timer 不会自动启用；重装不改变已有 timer 的启用状态。

按实际机器编辑配置：

- `node_name`、`sources`、`log_timezone`；确保 glob 只匹配相关访问日志。
- `codex_bin` 填完整可执行路径，且专用账户可执行；systemd 默认无法访问 `/home` 中的 CLI 安装。
- `model` 填你确认可用的模型 ID；默认 `reasoning_effort: low`，需所选模型支持；`policy` 写该节点的用途、允许的行为和关注点，不放凭据。
- 先保留 `enabled: false`。只授予专用账户指定访问日志的读取权限及父目录遍历权限；不要给予 sudo 或加入能读取全部系统日志的宽泛组。轮转后新文件也需要保持读取权限。

以专用账户配置独立 Codex 登录（不要复制生产管理员的整套 Codex 配置、MCP、hooks 或其他插件）：

```sh
sudo -u ai-abuse-patrol /usr/local/bin/codex login --device-auth
```

也可使用 CLI 支持的 API key 登录方式，凭据由目标机管理员通过安全渠道提供，不写进 JSON、命令行参数或 skill。包装脚本忽略用户 config/rules，禁用 shell 工具、子代理和 web search，使用只读 sandbox。证据直接放入提示词，模型不需要文件权限，也不应读取其他资料。模型调用仍会把摘要发送到所配置模型服务；域名、连接时间、端口和计数保留用于判断，用户和 IP 做了带本机密钥的 HMAC 化，**不是完全匿名化**。

### 使用 AxonHub 等 Responses 网关

可以使用网关中已配置的 Codex 通道，无需在巡查账户下进行 ChatGPT 登录。给该节点创建独立、仅允许推理的网关 API key，并限制模型/通道及每日额度。配置中填写：

```json
{
  "provider_base_url": "https://gateway.example.com/v1",
  "provider_env_key": "AXONHUB_API_KEY",
  "model": "填写网关实际可用的模型 ID"
}
```

将凭据通过安全文件传输写入 `/etc/ai-abuse-patrol/provider.env`，格式为 `AXONHUB_API_KEY=实际密钥`。该文件由 root 持有、模式 0600；不要将实际值写进仓库、聊天或命令行参数。systemd 读取此文件后注入任务环境。手动使用 `runuser` 不会自动加载该文件，实际模型验证应使用 `systemctl start ai-abuse-patrol.service`。

包装脚本通过 CLI 参数指定 Responses provider，仍忽略其他用户配置；网关凭据只在环境中传递，HTTP 请求与流式恢复的 CLI 自动重试均设为 0。网关自身的路由重试或上游计费不受此开关控制。非 HTTPS 地址及 URL 内的凭据均不接受。

如果网关需要额外的非敏感请求头，可配置 `provider_http_headers`，例如 `{"Version":"0.162.1"}`。不要在其中放凭据。有的网关按 `Version` 请求头判断客户端能力：它落后于实际 CLI 版本时，上游可能误报模型不支持。此值应与部署的实际 CLI 版本一起维护，不是所有网关的通用默认值。

首次配置若因模型映射等问题失败，修正后可显式使用 `--run-now` 做一次人工验证。它只跳过 12 小时间隔，仍受每日最多两次、失败占名额、非 root 执行及并发锁限制，不会清空调用历史。定时器不使用此参数。网关 `/models` 中可见的名称不保证上游实际支持，应完成一次有效报告后再启用 timer。

先执行零模型成本的采集：

```sh
sudo -u ai-abuse-patrol python3 /opt/ai-abuse-patrol/ai-abuse-patrol/scripts/patrol.py \
  --config /etc/ai-abuse-patrol/config.json --collect-only
```

检查 `/var/lib/ai-abuse-patrol/preview-evidence.json` 的 `coverage`、时间范围、解析失败数和用户统计。全是旧记录、未解析或空输入时，先修正日志；不要启用一个没有证据来源的巡查器。`--collect-only` 不会调用模型、不进入历史基线、不消耗每日名额。

准备好之后，将 `enabled` 设为 `true`，手动验证一次模型调用及报告，再启用 timer：

```sh
sudo systemctl start ai-abuse-patrol.service
sudo systemctl status ai-abuse-patrol.service
sudo cat /var/lib/ai-abuse-patrol/latest-status.json
sudo systemctl enable --now ai-abuse-patrol.timer
sudo systemctl list-timers ai-abuse-patrol.timer
```

`ProtectSystem` 限制写入到状态目录。代理日志位于非标准路径时，检查服务账户和 systemd 的路径访问权限。不要为了运行任务禁用 sandbox 或使用 root 运行 Codex。

## OpenRC / cron

安装器目前只处理 systemd。OpenRC 机器可按相同目录手动安装文件、专用账户和权限，并用专用账户的 crontab 调用同一脚本。需要支持 Codex CLI 的运行环境；本包不保证 Alpine/musl 可直接运行所有 Codex 二进制。

```cron
0 * * * * /usr/bin/python3 /opt/ai-abuse-patrol/ai-abuse-patrol/scripts/patrol.py --config /etc/ai-abuse-patrol/config.json >> /var/lib/ai-abuse-patrol/cron.log 2>&1
```

这是每小时检查预算，仍由 `interval_hours` 控制实际模型周期。对 cron.log 单独设置本机日志轮转。先手工运行验证，再添加 crontab。

## 结果、保留和停用

- `latest-status.json`：最近有证据的模型结果或最近一次证据不足；`failed` 不代表正常。务必同时查看 `at` 时间；配置/权限错误可能使本次执行在更新状态前就失败。
- `runs/<UTC时间>/report.md` 和 `report.json`：中文人工报告及结构化结果。
- 同目录 `evidence.json`：送入模型的摘要；`identities.json`：用户代号到原账号的本地映射，**不发送模型**。
- `codex.log`：本次 CLI 的本地诊断；也视为敏感报告数据。目录 0700，生成文件 0600。
- 保留最近 7 天（`retain_days` 可调）；每次实际采集时仅清理本包旧运行目录的已知文件，不碰原日志。`identity.key` 保持稳定，删除后不能与旧代号关联。preview、调用预算和最新状态单独保存。
- 错过的任务不无限补跑；只分析当前配置窗口，并在历史窗口之间存在间隔时提示缺口。
- 空/缺失日志：写 `insufficient_data`，不调用模型，退出码 2；可用数据中有截断/不可读来源时，允许模型分析剩余数据但必须披露局限。调度失败可从 systemd/cron 状态发现，本包不接外部告警渠道。

停用：`sudo systemctl disable --now ai-abuse-patrol.timer` 并把 `enabled` 改为 `false`。已在运行的一次任务可用 `sudo systemctl stop ai-abuse-patrol.service` 停止。不会停止固定规则检测。卸载时先停用任务，再由管理员决定是否保留状态和 Codex 凭据。

## 可选的持久违规与延迟处罚

面板读取 `/etc/ai-abuse-patrol/enforcement.json`，结构见包根的 `enforcement.example.json`。默认关闭。启用时将 `enabled_after` 设为本次启用的 UTC Unix 秒，旧报告不追溯处罚。面板启动时检查一次，此后每 5 分钟通过独立、仅能执行固定 `export.py` 的 SSH 密钥读取所选节点最近报告。严格校验已知主机密钥，不向模型提供面板 API 凭据或处罚工具。节点 root 的 `authorized_keys` 条目必须带 `restrict,command="/usr/bin/python3 /opt/ai-abuse-patrol/ai-abuse-patrol/scripts/export.py"`；导出器只接受 `ai-patrol-export-v2`，拒绝其他命令、符号链接和超限文件。

资格条件：报告在启用后产生且不超过两小时，分析窗口为 12–24 小时；访问审计在窗口前已启动，健康信息新鲜，无截断、解析失败、丢弃、写入错误或不可读来源；用户统计完整，用户身份必须属于该节点当前启用入站的真实账号。必须同时满足模型 `assessment: review_needed`、该发现 `severity: high`、`confidence: high`、`violation: true`，并有非空证据。自动处理仅限 `scan` 与 `connection_abuse`；注册、SMTP、共享账号等证据较弱类别继续人工复核。

独立事实门槛只统计已接受、路由为 `direct` 的连接：扫描至少 100 次连接、50 个端口；连接滥用至少 5,000 次连接且每分钟峰值至少 1,000。达到这些门槛本身不判违规，仍须满足上述模型判断和覆盖条件。每份报告至多处理两个账号。阻断、中转、未知路由及被拒绝的记录不能充当成功出站佐证。

策略由管理员在 2026-10-19 明确指定：首次符合条件的违规写入 `ai_patrol_violations` 并通知用户「活动已标记为一次违规，请联系管理员了解详情」，不封禁。首次记录后的 30 天内再次发生不重叠的独立违规，且新窗口开始于先前通知成功送达或用户在门户明确确认之后，暂停账号 24 小时，到期自动恢复。处罚为账号在所有节点的代理访问暂停，门户仍可登录。Codex 违规不计入实时检测的“三次机会后锁定”次数，不产生永久锁定。

`ai_patrol_reviews` 的节点/报告唯一键、跨节点的账号时间窗口去重以及违规、事件和处罚同一事务提交，保证同一报告或重叠窗口只记一次。若窗口内已有实时处罚，跳过延迟处罚；已在处罚中的账号不追加或延长暂停。原账号映射只通过受限 SSH 送给已持有账号资料的面板，不发给模型。

Telegram 使用已有账号绑定。失败不写成功回执，未送达记录在重启后继续重试，无十分钟截止时间；成功回执保存到 `account_notifications` 和违规记录的 `notified_at`。未绑定或无法送达的用户在门户持续看到通知，点击「我已知晓」写入仅限本人记录的 `portal_seen_at`。没有送达/确认回执时，后续发现仍只记录，不自动升级处罚。管理员可通过原有解除封禁和清除违规 API 解除暂停或撤销违规。

违规、回执和处罚保存在面板数据库，不随节点七天报告清理删除。停用：将面板配置的 `enabled` 改为 `false`；现有暂停可正常到期或由管理员提前解除。节点巡查 timer 可继续产生报告。

## 官方接口依据

Codex 非交互调用、只读 sandbox、结构化结果和登录复用见 [OpenAI 非交互模式文档](https://learn.chatgpt.com/docs/non-interactive-mode)；skill 的目录和发现机制见 [OpenAI skill 文档](https://learn.chatgpt.com/docs/build-skills)。这里只实现调用次数与输入体积控制，没有使用不存在的 CLI 每次美元预算参数。
