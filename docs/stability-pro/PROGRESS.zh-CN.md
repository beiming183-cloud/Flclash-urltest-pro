# 开发进度

## 2026-10-14 v3 保留官方操作

按用户要求恢复官方首页，把稳定模式放到可选页面；在代理页“更多”增加状态和诊断两个入口，保留原一键测速及点选。构建 cb51ad277ec87fe41da32ff0712da65ff2c225e5 的云端检查及真实模拟器操作验收完成。详情见 [v3 原版操作说明](OFFICIAL_UI_V3.zh-CN.md)。

## 2026-10-14 客户端修复复测

修复构建 d6592dc1e63da175da69b8d753a71a3df9cbddfb 的云端 native/android 检查全部成功。本机 API36 模拟器在原始网络设置下验证：候选页恢复、未通过 Android 联网验证的底层网络可正常预热、三个合格候选、真实固定出口、后台检测继续、5220 字节诊断导出及凭据脱敏。详情与签名安装说明见 [客户端修复记录](CLIENT_FIX_20261014.zh-CN.md)。这更正了早期将 VALIDATED 直接当作 online 的实现；本轮仍不宣称真实手机 ChatGPT SSL 已彻底解决。

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

## P5 自动化验收与测试 APK

最终构建 [GitHub Actions #10](https://github.com/beiming183-cloud/Flclash-urltest-pro/actions/runs/38440611267) 的 native / android 两个任务全部成功。Flutter analyze：No issues found；Flutter 全量回归：3348通过、3跳过；Go统计/出站组race、vet和wrapper测试通过；Android独立Kotlin编译及common/service/app JVM测试任务通过。此前本地WebDAV仿真测试对新名称空格的处理已修正。

[下载 arm64 测试包](https://github.com/beiming183-cloud/Flclash-urltest-pro/actions/runs/38440611267/artifacts/11837316516)，解压后安装FlClash-URLTest-Pro-arm64-test.apk（构建日志64.7MB）。下载包还包含SHA256SUMS.txt、SIGNATURE.txt、SOURCE_COMMIT.txt。签名经apksigner verify校验；这是debug签名测试版，applicationId为com.beiming.flclash.urltestpro.dev，不能视为正式签名发行版。来源commit：494c811b653e444300e2216552120e0ffe5bb2fc。GitHub artifact ZIP为62,885,952字节，其ZIP摘要为e06d303c11f0751831e838812175e5bce38225fc72464fbe861f67932f4a2659；APK自身摘要在包内SHA256SUMS.txt。

新增按网络独立聚合、最多8个LRU、网络验证状态离线时暂停调度；TLS导出只含证书/SPKI哈希和有效期/SAN数量。受控CONNECT认证拒绝30轮：原URLTest当次恢复0/30，稳定模式30/30，P95约0.7–0.9ms，仅代表内存故障注入，不是实际机场性能。参考Python测试34项已通过。

工具链保持上游pin：Flutter3.47.4、Dart3.13.3、Go1.26.4、Rust1.98.1、JDK17、NDK28.2.13676358；AndroidSDK实际包名为platforms;android-37.0（同API37）。Flutter native hook通过ANDROID_NDK_HOME/ROOT明确选择该NDK；测试临时关闭Go build hook，APK步骤已恢复build_assets:true并实际编译Go/Rust/JNI。

[安装与配置步骤](QUICKSTART.zh-CN.md)说明如何启用stable-failover及执行手机测试。普通URLTest配置不会自动开启新策略，示例保留地址不能直接使用。

## 尚未完成的验收

具体阶段建立时延分解、目标HTTP业务状态展示尚需完善；Wi-Fi/蜂窝/离线/Doze及ChatGPT真机验收尚未执行。云端及测试服务只能证明标准TLS校验与受控逻辑，不能证明ChatGPT应用的证书策略或用户实际网络故障已修复。测试不包含真实账号配置，不宣称20个真机案例全部通过。
