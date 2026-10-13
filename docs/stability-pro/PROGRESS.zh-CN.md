# 开发进度

2026-10-13：开始将参考模型接入实际 Go / Flutter / Android 客户端。

## P0 隔离基线

独立 applicationId：com.beiming.flclash.urltestpro（无正式签名的测试 APK 带 .dev）。名称 FlClash URLTest Pro，导入协议 flclashpro。保留 Kotlin namespace。移除 Firebase 插件及依赖、关闭上游更新请求。保留动态 applicationId 权限、快捷操作和分享隔离。

实际工具：Flutter 3.47.4 / Dart 3.13.3、Go 1.26.4、JDK 17、Rust 1.98.1。flutter pub get 已通过。构建工具以 CI=true 和 FLUTTER_SUPPRESS_ANALYTICS=true 运行，跳过云环境元数据探测。

## P1–P3 原生稳定组与故障处理

新增 opt-in stable-failover；普通 URLTest/fallback 保持原实现。只允许显式 HTTP/SOCKS5 住宅候选，端点白名单、同认证身份、固定 expected-exit、具体 RF2-Relay 引用；拒绝 DIRECT、自动中继组、嵌套中继及混身份。候选最多512、64延迟样本、衰减可靠性、三候选、多样性、10秒三次验证、健康主用保持、冷却与隔离、900次总预算上限。

通过指定完整候选进行 HTTPS、两个目标域 TLS、出口核验，不临时改实际选择。HTTP CONNECT 在返回应用连接前重试；整个调用共享 deadline；不重放应用 payload，禁用本组 UDP。调度属于 Core，跟随原生服务启停及配置替换。Android 网络观察者发送网络代失效事件，不依赖页面定时器。

已执行：Go wrapper go test；新组和统计模块 go test -race。覆盖认证拒绝后备用建立、payload仅一次、总deadline、全死失败关闭、旧epoch拒绝、一次成功不晋升、主用保持、多样性、备用过期、预算并发、100次启停和手动探测不改路由。

## P4 日志与界面

原生 HMAC 伪名、白名单事件、4×1MiB轮转、10分钟debug、2MiB ZIP硬上限。保存有界聚合，恢复不恢复活跃资格。新增首页稳定面板：当前链路、三个候选、EWMA/P95、最近检测、自动备用、省电、调试倒计时、标记和导出。页面定时器只查询状态，不控制后台选路。

已执行：超过16MiB模拟事件、秘密字符串拒绝、ZIP JSON解析、轮转、debug到期、重启身份保持测试。Flutter analyze 已通过（No issues found）。本地化通过源ARB与生成器更新四种语言。

## P5 验收状态

Flutter全套测试首次受Rust跨平台组件下载阻断，正在使用同版本明确工具链重试；Android APK构建正在准备；GitHub Actions新增真实原生/安卓验证流程。尚未取得APK、SHA256或真机证据，不能宣称全部功能验收完成。

新增按网络独立聚合、最多8个LRU、网络验证状态为离线时暂停调度；TLS导出只含证书/SPKI哈希和有效期/SAN数量。受控CONNECT认证拒绝30轮：原URLTest当次恢复0/30，稳定模式30/30，P95约0.9ms，仅代表内存故障注入，不是实际机场性能。

Flutter完整本地回归：3348通过，3个失败：两个WebDAV仿真服务未解码新名称空格（已修正测试仿真器，14个WebDAV测试通过）；一个当前PID可见性受本地环境限制；所改界面/协议/导入/标题回归均通过。GitHub native CI已通过。Android SDK实际包名为platforms;android-37.0，已取得同API37 SDK，未降低SDK。

仍需完善或验证：具体阶段建立时延分解；目标HTTP业务状态展示；WiFi/蜂窝/离线/Doze及ChatGPT真机验收。调试和测试服务只能证明标准TLS校验，不能证明ChatGPT应用的证书策略。本地不含真实账号配置。
