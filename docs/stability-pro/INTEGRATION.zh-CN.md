# 接入地图与阶段实现

下表是锁定基线中已核对的接入点；稳定模式本身尚未写入这些文件。不要在 UI 外接 Windows PowerShell 或通过临时改主组来测试。

| 层 | 现有文件 / 符号 | 计划 |
| --- | --- | --- |
| 内核选路 | `core/Clash.Meta/adapter/outboundgroup/urltest.go` / `URLTest.DialContext`, `fast` | 原版只对一个选中代理调用 DialContext，缓存约 10 秒；新增独立稳定组，保留旧组 |
| 组解析 | `core/Clash.Meta/adapter/outboundgroup/parser.go`, `constant/adapters.go` | 新增 opt-in 类型、固定终端校验、循环拒绝；未知类型显式报错 |
| 实际代理握手 | `core/Clash.Meta/adapter/outbound/http.go`, `socks5.go` | 核验 CONNECT/S5 建立完成的准确位置，避免在应用 payload 写出后重试 |
| 生命周期 | `core/hub.go`, `core/method.go`, `core/lib.go` | native 配置应用、启动、关闭唯一调度 owner，取消 context 并等待 goroutine 清理 |
| 桥接契约 | `lib/core/method.dart`, `lib/core/interface.dart`, `core/constant.go` | 对称新增稳定状态查询/设置/指定候选探测/导出方法；更新契约测试 |
| 状态事件 | `core/message.go`, `core/route.go`, `lib/providers/route_state.dart` | 保持 state / priority / bulk 分层；真实切换推动 route generation，使 IP 检测缓存失效 |
| UI | `lib/enum/enum.dart`, `lib/providers/actions/proxies.dart`, `lib/providers/state/proxies.dart` | 稳定组枚举、状态面板和单节点测试，不创建第二自动选择器 |
| 原日志入口 | `lib/providers/app.dart` / `Logs.exportLogs`, `lib/views/logs.dart` | 现有导出是 Flutter 有界列表；新增 native 持久化脱敏诊断包，独立于页面订阅 |
| Android owner | `android/app/src/main/kotlin/com/follow/clash/ServiceState.kt`, `ServiceStateMachine.kt`, `ServiceController.kt` | 继承 service ownership，不因 Flutter detach 停止调度，不启动重复 owner |
| 平台桥 | `lib/core/lib.dart`, `android/app/.../plugins/ServicePlugin.kt`, `android/core/src/main/cpp/` | 保持消息 JSON 信封与 JNI 生命周期，慢探测异步执行不阻塞 dispatcher |
| 构建 | `plugins/setup/hook/build.dart`, `plugins/setup/setup_hooks/`, `setup.dart` | 保留自动 native 构建链和指纹失效机制 |

## 建议协议（待实现）

新增组类型建议 `stable-failover`；普通 stock Mihomo/官方 FlClash 不支持它，必须有清晰报错和普通 URLTest 回退导出，不能将假 YAML 当成可直接导入文件。

```json
{
  "method": "getStabilityState",
  "arguments": {"group": "IP1"},
  "result": {
    "generation": 7,
    "primary": "node-hmac-1",
    "standbys": ["node-hmac-2", "node-hmac-3"],
    "networkEpoch": 2,
    "lastSwitchReason": "dial_timeout",
    "budgetUsed": 32,
    "budgetLimit": 900
  }
}
```

这只是字段示例，实际使用现有 `CoreMethodCall/CoreMethodResponse` 信封，不造新的传输或 double-encode。新增 `probeStabilityCandidate` 接受候选 ID / 白名单服务标签 / deadline，核验 group ownership，并直接对候选发请求；它不调用 `changeProxy`，不改变 `route.go` picks。

生产调度器所有健康记录和选择写操作由单 owner 串行执行；异步探测完成携带 configGeneration、networkEpoch、node identity。过期事件丢弃。selector、route snapshot、diagnostic exporter 只读同一快照。Go 跨 goroutine 不修改 `tunnel.AllProxies()` 的共享 map。

## P0：可构建的隔离基线

- 运行上游构建验证，记 SDK 与依赖实际可用性。
- 将 `android/app/build.gradle.kts` applicationId 改为独立命名，建议 `com.beiming.flclash.urltestpro`。namespace 可保留，不能盲目重命名所有 Kotlin 包。
- 核验 manifest placeholder、FileProvider authority、deep link、导入分享、快捷操作和偏好目录都隔离。
- 查 Firebase google-services 绑定包名；该 fork 默认关闭或移除对应插件/配置/上传 task，不复制上游服务账号或使用别人的遥测项目。
- 更换 app label 为 `FlClash URLTest Pro`，更新通道指向 fork 或关闭，防止上游更新覆盖定制行为。
- 不启用上游发布工作流；使用自己的签名，凭据只在 CI secret。不给未接入 APK 起名“稳定版完成”。

## P1：纯 Go 选举与有界数据

新增 `stable.go`、`stable_stats.go`、`stable_scheduler.go` 和测试。移植参考模型中的硬约束/排序，使用注入时钟、随机源、网络执行器；实现冷池→候补→三候选、冷却与网络代。建立 fixture 测试覆盖时间衰减，不依赖真实机场。

## P2：指定链路探测与安全拨号重试

TLS / HTTP / SOCKS5 都通过完整住宅链路。测试认证失败、CONNECT拒绝、延迟握手、reset、ctx取消、候选全死。并行建立连接如有需要，只竞争代理建立阶段，关闭 loser socket；不能同时发送同一个 ChatGPT 请求。

新增 true-establishment 指标，区分机场拨号、住宅控制握手、TLS探测、HTTP响应时间。passive失败必须来自真实连接错误分类，不从业务响应 403/429 直接认定坏节点。

## P3：生命周期与路由一致性

新调度器跟随 VPN/native core，后台和锁屏仍有效。网络切换、config apply、服务 stop/revoke 清理所有旧探测；页面关闭不停止 native 调度。对重复快速 start/stop 做 race 测试，无僵尸 goroutine 或定时器。

不要借用 `watchRoute(true)` 的 UI 订阅计数启动后台调度：用户隐藏 UI 时 watch 可以停，稳定器不能跟着停。真正选路变化仍要更新 route snapshot，以便 UI 恢复后获得正确结果。

## P4：详细有界日志与 UI

按照 LOGGING 实现 native 脱敏先于写盘，错误码结构化、导出包含覆盖范围和缺失计数、debug 自动到期。保留官方日志页，新增“导出稳定性诊断”。界面设计遵循上游 AppShape、Riverpod和代码生成约定。失败不能只显示“Timeout”，需至少区分阶段。

## P5：故障注入 + 独立 APK + 真机验收

按 BUILD_AND_ACCEPTANCE 跑全部门槛；提供编译来源 commit、APK SHA256、签名指纹和日志导出样例。Android 无 root不等于任意后台精确计时，报告 Doze 延迟。完成后更新 PROGRESS 与 README 状态。
