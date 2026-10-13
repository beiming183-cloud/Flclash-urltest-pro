# 云端构建和验收

## 工具链基线

下列版本来自锁定上游的源码/CI，不代表已在本机完整构建验证。云端先核验下载与兼容，记录实际版本，不静默换到不同 SDK。

| 工具 | 基线 |
| --- | --- |
| Flutter | 上游 CI `3.47.4`（`.github`原工作流现存于upstream-workflows） |
| Go | 上游 CI `1.26.4`；wrapper go.mod language1.21，Core go.mod language1.20不等于构建工具版本 |
| JDK | 17 |
| NDK | r28c / `28.2.13676358` |
| Rust | `plugins/rust_api/rust/rust-toolchain.toml` 锁定 `1.98.1` |
| Dart | 随 Flutter；pubspec最低3.10，代码/依赖实际需求以解析结果为准 |
| Python参考测试 | 3.10+，stdlib |

如果镜像找不到这些上游 pin，先报告失败和获取来源，不宣称构建成功。库构建涉及 Go + Rust + JNI；纯 Flutter 安装不足以完成 APK。

## 构建步骤

本仓库已 vendored Core，不需要 SSH 子模块访问。依赖仍按上游 pubspec/lock 获取，不把 SDK、缓存、住宅配置提交 Git。参考测试与产品测试分开。

```sh
python -m unittest discover -s reference/tests -v
python scripts/verify_handoff.py
flutter pub get
flutter analyze --no-fatal-infos
flutter test
cd core
CGO_ENABLED=0 go test .
CGO_ENABLED=0 go vet .
cd ..
dart setup.dart android
cd android
./gradlew :service:compileDebugKotlin :app:compileDebugKotlin
cd ..
```

上游未跟踪 Gradle wrapper 启动脚本，因此先通过 Flutter/setup 构建生成需要的 Android 工程辅助文件，再运行独立 Kotlin 编译检查。纯 Dart测试时参考 `.agents/commands.md` 的 build hook规则；不要提交 `build_assets:false`，否则打包可能缺少新内核。Core功能变更还需在 `core/Clash.Meta` 运行新组/适配器针对性 Go测试和race测试，及网络故障注入套件。

先完成独立 package ID 和 Firebase配置隔离后再构建。release无自有签名时上游可能退回debug签名和`.dev` suffix：显示为测试包，不伪称正式版。稳定更新需要自有固定签名，不共享私钥。

不要把官方 APK、SDK、编译缓存打进交接ZIP。保存 APK、SHA256、签名指纹、来源commit与验证报告为独立CI artifacts；APK在完成以下门槛以后才称为可验收版。

## 必须通过的案例

| ID | 注入/操作 | 预期 |
| --- | --- | --- |
| A01 | IP1组误加入IP2/不同exitKey/DIRECT/自引用 | 解析拒绝，原配置仍可用 |
| A02 | 两条同中继HTTP/S5加其它中继 | 优先不同中继，只有合格成员可入池 |
| A03 | 新节点一次极快成功 | 不立即晋升；至少多次验证 |
| A04 | 备用快20ms，主用健康 | 主用保持，排名可更新 |
| A05 | 主用DialContext超时，备用近期成功 | 同次建立尝试备用；记录阶段和总deadline，非等待全量测速 |
| A06 | HTTP CONNECT延迟/拒绝或S5认证失败 | 在应用payload前处理；关闭失败/loser socket |
| A07 | 应用请求已发送后断开 | 不自动重放；后续重连可换线，流式可能中断如实说明 |
| A08 | 备用最后成功超过30秒或已失败 | 不盲切；专项小批验证后再使用 |
| A09 | 同一个失败重复上报/旧routegeneration返回 | 去重或忽略，不反复切换 |
| A10 | 所有候选共享终端故障 | 提示无住宅链路，不DIRECT/机场直出/另一IP救援 |
| A11 | 探测服务A失败、B正常 | 记录服务故障，不永久淘汰全部候选 |
| A12 | 系统不信任证书/主机名错/到期/握手EOF | 分类准确，TLS验证不关闭，EOF不是certificate mismatch |
| A13 | 收到已验证TLS的HTTP401/403/429 | 传输和业务状态分开，不能误当拨号失败 |
| A14 | 手机WiFi↔蜂窝/配置重载/飞行模式 | 新网络代复核，旧结果失效，无并行重复调度 |
| A15 | 页面退出/锁屏30分钟/Doze | native控制器存在，记录实际探测间隔，不伪报实时 |
| A16 | 连续start/stop/revoke100次 | 无重复timer/goroutine/listener，服务owner一致 |
| A17 | 临时“测试候选”按钮 | route picks、账号出口不因测试发生切换；真实调度切换另记原因 |
| A18 | 大量日志超过16MiB/异常带密码URL | 常驻≤4MiB，所有输出无秘密，debug10min停止 |
| A19 | 导出后解析、压缩不可压随机内容 | ZIP≤2MiB，JSON合法，覆盖/截断计数准确 |
| A20 | 重启应用及恢复统计 | 统计可恢复，但活跃资格复核；不使用昨天备用直发 |

A05需报告分位数而不是最快一次；建议30轮受控故障，比较原URLTest与新模式的首次成功恢复时间、断请求数、探测请求数和内存/电量。目标是显著缩短空挡，不预先保证每次1秒或无断输出。

## 真机接受步骤

安装独立APK，官方版保留但停止其VPN；本地导入自己的IP1 YAML，首次验证出口。打开ChatGPT连续使用，记录错误时点；测试WiFi与蜂窝、锁屏恢复、长输出和手动标记。导出诊断检查固定出口没有越界，并确认同一设备后台行为。

核验“没有再报SSL”需要多次相同环境复测；云端的系统TLS探测通过不足以证明ChatGPT app的证书策略通过。

## 回退

停止定制VPN，重新启动官方FlClash并选原配置。不能同时开两个VPN。定制APK数据隔离；删除它不会删除官方配置。电脑Clash、Cockpit和绑定端口未在本任务改动。
