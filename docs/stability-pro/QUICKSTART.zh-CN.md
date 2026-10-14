# FlClash URLTest Pro 测试版使用说明

## 安装

在手机点击[修复版 APK 直接下载](https://github.com/beiming183-cloud/Flclash-urltest-pro/releases/download/stability-pro-test-20261014-v2/FlClash-URLTest-Pro-arm64-test.apk)。[Release 页面](https://github.com/beiming183-cloud/Flclash-urltest-pro/releases/tag/stability-pro-test-20261014-v2)另有 SHA256、签名证书和来源 commit 文件，便于核验。

这次测试签名与旧包不同：先保留或导出配置，卸载旧 URLTest Pro 测试版，再安装修复版并重新导入原 IP1 文件。官方 FlClash 可以保留。首次启动等待首页出现主用节点后，再打开业务应用；三个候选会继续补齐。

此包适用于 Android arm64，是测试签名版；applicationId 为 `com.beiming.flclash.urltestpro.dev`，与官方版数据隔离。安装本身不会导入住宅代理配置。启动定制 VPN 前停止手机上其他 VPN。

## 配置稳定组

[配置示例](../../fixtures/stability-pro/profile.stable.example.yaml) 使用保留 IP 和虚构账号，不能直接联网。请仅在自己的设备上替换为真实配置，不把密码或订阅提交到仓库。

1. 定义具体中继节点，名称以 `RF2-Relay-` 开头；中继必须是具体节点，不能使用自动选择组、DIRECT 或嵌套 dialer-proxy。
2. 定义 HTTP / SOCKS5 住宅候选，每个候选的 `dialer-proxy` 指向具体中继。所有候选使用同一住宅认证身份。
3. 建立 `type: stable-failover` 组。设置 `expected-exit` 为预期公网出口 IP；`residential-endpoints` 为允许使用的住宅代理 `server:port` 列表；`proxies` 显式列出住宅候选。
4. 可用 `relay-airports` 标注中继所属机场，提高备用多样性。将目标规则指向该组。
5. 导入配置并启动。首次合格需要同一网络内三次成功、跨度至少十秒、目标 TLS 和出口核验；单次测速成功不会立即成为主用。若全部不合格，组会拒绝连接。

普通 URLTest / fallback 保持原行为。只有使用上述自定义类型的组启用本版稳定策略；其他客户端无法识别此类型。

## 面板与故障测试

首页稳定面板显示当前链路、三个活跃候选、EWMA / P95、探测预算和状态。候选的“测试”只执行指定链路探测，不临时切换实际主用。

- 自动备用：启用时保持健康主用，建立失败可尝试经过近期验证的备用；关闭时仅使用合格主用。
- 省电：降低探测频率，备用仍必须满足近期成功要求。
- 调试：原生计时十分钟后自动结束；页面关闭不延长计时。
- 标记：标记当次故障时间，不发送账号请求。
- 导出：生成最大 2 MiB 的脱敏 ZIP；不包含代理凭据、订阅、聊天正文或 TLS 明文。

建议先确认自己的公网出口，再依次测试主中继暂时不可达、所有中继不可达、Wi-Fi / 蜂窝切换、飞行模式、锁屏恢复及配置重载。全部链路失败时应显示无住宅链路，不能改用 DIRECT 或另一个住宅身份。

备用重试发生在返回连接给应用之前。应用已经发出的请求和流式内容不会被自动重放；失败后仍可能需要应用重新连接。

## 验收范围

自动化证据与当前 APK 来源见 [开发进度](PROGRESS.zh-CN.md)。受控故障注入不能代表真实机场时延或电量，标准 TLS 验证不能证明 ChatGPT 应用的全部证书策略。具体阶段时延、目标业务 HTTP 状态、Doze 和真实 ChatGPT 使用需继续验证。

回退时停止定制 VPN，启动官方版并使用原配置。删除测试版不会删除官方版配置。
