# v3：保留最新官方版操作

基线为最新官方 FlClash v0.8.99。首页、代理标签、节点卡片和一键测速动作与官方源码逐项比对一致。首页不再注入稳定模式大面板，原来的测速、点选、搜索、排序和导航保留。

代理页右上角“更多”只新增两个入口：

- **住宅链路稳定模式**：按需查看当前主用和两个合格备用，主用优先排列并突出显示。
- **导出诊断**：直接保存有大小上限的诊断 ZIP，仍是 4 MiB 轮转、单次导出不超过 2 MiB。

日常继续在原代理页点闪电一键测速、查看卡片延迟和点选节点；详细稳定状态不占默认首页。普通 Selector/URLTest 逻辑未修改。稳定组仍要求近期 TLS 和固定出口资格，已失效的候选可能被拒绝；这不会放宽成机场直出或忽略证书。

## 验收

构建来源 cb51ad277ec87fe41da32ff0712da65ff2c225e5；[通过的构建](https://github.com/beiming183-cloud/Flclash-urltest-pro/actions/runs/38499345176)。界面回归、Go 和 Android 模块检查全部通过。

真实 API36 模拟器安装、原 IP1 文件导入、原版首页、原按钮一键测速及卡片 ms 返回、新菜单和状态页面、三个合格候选、合格节点点选及后续故障备用、固定出口 HTTPS 查询、菜单直接导出均已执行。导出 5977 字节，实际凭据扫描通过。没有在真实手机登录 ChatGPT，不宣称 SSL 问题已彻底解决。

APK SHA256：7228d60e6ab54b33f0228c93684a9f84cb83106be2c1a1068a7fe83ba270c60e。

## 下载和安装

[手机直接下载 APK](https://github.com/beiming183-cloud/Flclash-urltest-pro/releases/download/stability-pro-test-20261014-v3/FlClash-URLTest-Pro-arm64-test.apk)。build number 为 2026101403，应用 ID 为 com.beiming.flclash.urltestpro.dev。

本次测试签名仍与旧包不同：先保留原 IP1 文件，卸载旧 URLTest Pro 测试版后安装 v3，再重新导入原文件。官方 FlClash 可保留。整机备份迁移测试被执行工具的自动审批拒绝，只返回 blocked by policy，没有具体原因；未执行该动作，也未绕过重试。通过保留的私有 IP1 文件完成验收。

正式电脑 Clash、Cockpit 和账号端口绑定未修改，私有配置与原始个人诊断不上传仓库。
