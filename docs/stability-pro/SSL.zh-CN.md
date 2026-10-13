# SSL / 安全连接问题诊断

用户截图写的是“网络提供非预期的 SSL 证书”，不是 SSH 错误，也不是仅仅网速慢。提供的 FlClash 日志有握手被中止的记录，但没有同一时刻的 ChatGPT TLS 细节。不能断言机场超时是这张截图的根因。

## 已有记录（脱敏）

原手机日志约 9.4 KB / 88 行，主要是应用日志，缺少逐节点健康检查和切换事件。

| 时间（手机本地，2026-10-13） | 现象 | 解释边界 |
| --- | --- | --- |
| 21:59:32 | 应用 IP1-v3 配置 | 不证明每条候选可用 |
| 21:59:37 | 并发 IP 查询多次握手中止，另有 HTTP 502；结果对象 success 但 data=null | 不能当出口验证成功；握手中止不等于证书不匹配 |
| 22:01:34 | 出口查询返回预期 IP1（本仓库不公开具体地址） | 当时该请求链路通；不证明后续 ChatGPT 一直稳定 |
| 之后数次 | ICMP timeout | 不作为住宅 HTTPS 链路存活证据 |
| 22:07:28 | 某旧订阅 HTTP 404 | 订阅更新问题，不直接等于业务链路证书错误 |

原始订阅路径带凭据，不收录。本仓库 `fixtures/stability-pro/observed-incident.json` 只有聚合事实，保留无法确定的项。

## 错误分类与动作

| 类别 | 采集点 | 首次动作 |
| --- | --- | --- |
| network_offline | Android network validation / socket | 暂停大量探测，等待网络恢复；不能给所有节点记永久失败 |
| relay_dial_timeout | 指定中继建立 | 试已验证备用，原路径可疑；复核确认再冷却 |
| residential_auth / connect_rejected | HTTP CONNECT / S5 控制握手 | 相同终端普遍发生时提示住宅凭据/服务问题 |
| tls_aborted | TLS握手 EOF/reset | 记录目标与阶段，交叉服务复核；不能冒充 cert mismatch |
| cert_untrusted / cert_hostname / cert_expired | 标准 TLS 校验 | 隔离候选，保持验证开启；目标域专项复核 |
| unexpected_exit | 出口查询 | 隔离候选，禁止给固定账号使用 |
| service_http_401/403/429 | 通过验证的 HTTPS 响应 | TLS/传输成功单独记录，业务可用性受限；不要把每个403当坏链路 |
| websocket_idle / stream_reset | 已建立连接 | 后续重连走健康路径；已有输出不能无缝移走 |

原版日志里的 `HandshakeException: Connection terminated during handshake` 应分类 tls_aborted。ChatGPT 的证书固定/平台策略可能比标准系统 trust 验证更严格；通用探测 TLS 通过也不保证 ChatGPT app 通过。

## 诊断步骤

1. 用户点“标记刚才故障”，保存时间、当前候选/网络代/最近选择原因。
2. 通过该指定住宅候选连接 `chatgpt.com:443` 与 `android.chat.openai.com:443`，启用 SNI、正常系统信任和主机名校验；另测已知 HTTPS 对照服务。只建立验证或小请求，不携带用户 token。
3. 记录是否握手完成、校验分类、证书 SHA256、公钥 SHA256、有效期范围、SAN 数量、版本。证书链可本地查看，但标准导出仅保留哈希及结构化错误；不能导出原始证书正文或自定义诊断响应全文。
4. 比较当前候选与同一住宅出口备用候选，区分某中继故障与共同终端/目标故障。
5. 若所有路线上相同，检查手机时间、公共 Wi-Fi 登录页、设备私人 DNS、过滤/证书应用、VPN分应用规则；这些由用户确认，不自动修改设置。对比蜂窝和 Wi-Fi，不偷偷绕固定出口发送账号流量。
6. 检查 ChatGPT / 认证 / 静态资源相关域规则与 WebSocket TCP443 是否走同一住宅边界。手机已有普通 URLTest YAML保留，不能仅用另一个域名204通过来宣布修复。

当前 TLS 检测用于诊断；不读取 ChatGPT 应用私钥、不代理解密请求、不安装本地 CA、不设置 `skip-cert-verify`。如果机场自身原配置有该字段，需明确其只属于机场隧道层，不能扩散到目标 HTTPS。

## 官方依据与待验证项

[OpenAI 网络建议](https://help.openai.com/en/articles/9247338-network-recommendations-for-chatgpt-errors-on-web-and-apps) 提到 TLS 检查可能导致 SSL 问题、WebSocket 长连接策略和所需域名。该文部分证书错误说明针对 macOS，不能把它当作 Android 这次错误的已确认根因。

最终验收必须用用户真实 Android ChatGPT app、登录状态与当前网络。云端测试只能证明实现遵守 TLS 验证和故障处理协议。
