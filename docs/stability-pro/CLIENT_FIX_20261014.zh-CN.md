# 2026-10-14 客户端回归修复

## 已复现的问题

在隔离 Android API36 模拟器导入真实私有 IP1 文件后，旧测试 APK 有两个独立问题：

1. 内核输出 StableFailover 组，但 Dart buildGroups 仍只接受旧类型列表，代理页只剩“出口”标签。
2. 底层网络已连接并具备 INTERNET，但 Android 未赋予 VALIDATED 标志时，NetworkObserveModule 把 online 设为 false，导致稳定检测停止，没有主用节点。

第二项经过原始条件、临时联网验证对照、恢复原始条件的复现。对照只用于测试模拟器，不是给手机修改系统设置的建议。

## 修复范围

- buildGroups 通过统一的组类型识别保留 StableFailover，复用现有自动选择组界面，恢复候选标签和节点卡片。旧组类型、排序和原始代理映射保持兼容。
- Android 基于底层网络身份、INTERNET、NOT_VPN 和 NOT_SUSPENDED 判断检测是否可运行。VALIDATED 的变化不再重置候选；真正失去网络、网络暂停或网络身份变化仍通知内核。
- 检测继续受现有并发和预算约束。候选仍需通过正常 TLS 验证及固定住宅出口核验，不能回落到 DIRECT 或机场直出。
- Android build number 增至 2026101402。住宅配置、机场与住宅的具体绑定、电脑 Clash/Cockpit 不属于本次修改范围。

## 自动化验证

新增完整组转换回归，覆盖“出口 Selector + IP1 StableFailover + HTTP/SOCKS5 成员”，并检查普通出站及未知类型不会混入组列表、原始映射不被修改。

新增六项 Android 状态策略测试，覆盖可用但未验证、重复能力回调、网络身份变化、断网、暂停及恢复、缺少互联网能力或 VPN 不作为底层网络。六项已在本机 JDK17/Kotlin JVM 运行通过；完整 Android 模块编译及全量 Flutter 回归由云端流水线执行。

构建来源：d6592dc1e63da175da69b8d753a71a3df9cbddfb。
[修复构建流水线](https://github.com/beiming183-cloud/Flclash-urltest-pro/actions/runs/38492440671)。

## 实测边界

新 APK 已在原始系统网络设置下实际安装并导入私有 IP1：IP1 候选标签恢复；底层网络未赋予 VALIDATED 标志，也能选出主用并补足三个候选；真实 HTTPS 查询返回预期住宅出口。退到 Android 首页后检测预算由 53 增至 77，证明原生检测继续运行。诊断导出 5220 字节，凭据扫描通过，控制服务与两个 ChatGPT 服务的标准 TLS 校验成功。

首次预热还没有合格主用时，一次 HTTPS 查询失败；主用通过验证后，相同请求成功。因此应等待首页出现主用后再使用业务，不把预热期间的失败当作已验证线路的故障。

APK SHA256：c6f975e962623d83970619830d60077074051a6ea936efc2c48ba3c0894323d6。私有 YAML、真实代理凭据及原始个人诊断没有上传仓库。

## 安装注意

这次云端测试签名与旧包不同，模拟器已实际复现 INSTALL_FAILED_UPDATE_INCOMPATIBLE。先保留或导出私有配置，再卸载旧 URLTest Pro 测试版并安装本版本，重新导入配置。无需卸载官方 FlClash。固定可持续签名的正式发行还未设置，测试包不能承诺每次覆盖升级。

[手机直接下载修复 APK](https://github.com/beiming183-cloud/Flclash-urltest-pro/releases/download/stability-pro-test-20261014-v2/FlClash-URLTest-Pro-arm64-test.apk)。

模拟器中的标准 TLS 校验不能替代真实手机 ChatGPT 应用的证书策略验收；本次不承诺既有 TCP/流式输出可无缝迁移，也不承诺所有手机 SSL 错误均已解决。
