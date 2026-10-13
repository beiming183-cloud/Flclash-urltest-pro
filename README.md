# FlClash URLTest Pro — 云端开发交接包

目标：住宅出口固定，机场中继可以改变；从完整链路中选出三个稳定候选，故障时快速尝试备用，并输出体积受控的诊断日志。

**状态：源码基线 + 完整开发规格 + 可运行参考模型。Android 稳定选择器、拨号重试和新日志界面尚未接入。直接编译当前 Flutter 源码得到的是上游基线，不是已经修复的客户端。**

请从 [云端任务入口](docs/stability-pro/START_HERE.zh-CN.md) 开始。原项目说明保留在 [README_UPSTREAM.md](README_UPSTREAM.md)。

| 文件 | 用途 |
| --- | --- |
| [产品与算法规格](docs/stability-pro/SPEC.zh-CN.md) | 分层候选池、固定出口、故障处理、测试预算 |
| [接入地图](docs/stability-pro/INTEGRATION.zh-CN.md) | Go / Flutter / Android 的修改边界和阶段任务 |
| [证书诊断](docs/stability-pro/SSL.zh-CN.md) | 区分证书错误、握手中止、超时和目标站故障 |
| [日志及导出](docs/stability-pro/LOGGING.zh-CN.md) | 4 MiB 轮转、10 分钟调试、最大 2 MiB 导出包 |
| [构建与验收](docs/stability-pro/BUILD_AND_ACCEPTANCE.zh-CN.md) | 独立 APK、云端构建、故障注入和手机验收 |
| [参考实现](reference/stability_pro/engine.py) | 无网络副作用的状态机与同次拨号重试模型 |
| [日志参考实现](reference/stability_pro/diagnostics.py) | 白名单字段、轮转与 ZIP 导出 |
| [脱敏案例](fixtures/stability-pro/observed-incident.json) | 用户手机日志实际能证明和不能证明的事实 |
| [版本来源](docs/stability-pro/UPSTREAM.md) | 上游应用和内核的精确版本、许可证 |

## 运行参考测试

Python 3.10+，无需第三方依赖：

```sh
python -m unittest discover -s reference/tests -v
python scripts/verify_handoff.py
python reference/demo.py --output ./test-results/demo
```

测试的是参考逻辑及日志边界；它不会修改 Clash、切换账号、打开 VPN 或发送任何网络请求。参考模型不替代 Go / Android 实现和真机测试。

## 隐私与隔离

本仓库不包含真实住宅代理密码、机场订阅或账号资料。示例使用保留地址，不能直接联网。自己的配置只在手机本地导入，不提交到 Git。

云端开发只修改本仓库，保留电脑 Clash、Cockpit、IP1/IP2 端口绑定及官方手机应用。独立 APK 必须更换应用 ID，启用后同一手机只能同时运行一个 VPN。

源代码遵循上游 GPL-3.0；保留根目录 LICENSE、内核 LICENSE 和各组件声明。新增交接与参考代码也采用 GPL-3.0。
