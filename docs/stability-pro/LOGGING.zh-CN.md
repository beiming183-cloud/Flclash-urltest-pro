# 详细日志，控制体积

日志必须在 native / Core 层采集，Flutter 窗口不在也继续。现有 Flutter Logs 列表和导出可保留，但不能充当唯一的稳定性历史。

## 空间预算

| 内容 | 硬限制 | 超限方式 |
| --- | --- | --- |
| JSONL 事件 | 4 个文件 × 每个 1 MiB，总计 ≤4 MiB | 先轮转再写，删除本日志集合最旧文件 |
| 单条事件 | ≤4 KiB UTF-8 | 拒绝或缩减，仅增加 dropped 计数 |
| 稳定性聚合状态 | ≤2 MiB | 64样本环、网络8个LRU；删统计不删用户节点 |
| 调试模式 | 600 秒 | 单调时钟到期，切回普通；同一个4MiB预算 |
| 标准导出 ZIP | ≤2 MiB | 留摘要、失败和切换；逐步截断事件并显式说明 |
| 应用内临时导出 | 最多1份 | 替换旧导出；停止共享后清理该目录 |

普通日志+状态+单份导出约最多8MiB；这不包括应用本体/上游一般日志/系统缓存。普通模式不写每个数据包或每次 Read/Write；成功探测可只聚合，并每分钟记一次快照。失败、切换、TLS验证问题、补位、用户标记必须保留，3秒内同原因重复故障合并，计数不能丢。

参考 logger 为便于验证逐次记录规范化事件；生产版在此基础上加入成功快照聚合和重复去重，不能声称参考模型已接到 native 后台。

## 白名单事件字段

schemaVersion=1，统一毫秒或秒并在名字体现。时间包含 UTC wallClock 和 monotonicMs；记录 routeGeneration 与 networkEpoch，便于识别旧请求失败误伤新主用。

| 字段 | 含义 |
| --- | --- |
| event / reason | 枚举：probe、switch、cooldown、promote、network_changed、tls_check、budget_exhausted、user_mark、snapshot |
| groupId / nodeId / relayId | IP1/IP2 或本地带盐HMAC伪名；不输出真实服务器端点、住宅账号 |
| source / phase / serviceTag | 主动/被动、relay/CONNECT/S5/TLS/HTTP、固定服务标签 |
| ok / latencyMs / ewmaMs / p95Ms | 是否正常及对应测量口径，缺失延迟不能写0当最快 |
| error | 固定错误码；不写原始异常字符串、URL或响应正文 |
| primaryId / previousId / standbyIds | 脱敏选路变化 |
| lastSuccessAgeMs / sampleCount / reliabilityLower | 备用资格与选优理由 |
| cooldownMs / budgetUsed / budgetLimit / inFlight | 暂停、预算和并发 |
| tlsVerified / certSha256 / spkiSha256 / sanCount | 证书验证摘要，错误时区分握手未完成 |
| repeated / dropped / truncated | 合并/丢弃/导出截断计数 |

不要记录：密码、UUID原文、订阅完整路径/查询/token、账号邮箱/账号ID、Cookie、Authorization、提示词、聊天响应、私人域名或任意未经约束的 message。脱敏必须在事件生成边界完成，先存原文再导出时删不合格。

真实别名可能包含地址/账号；标准导出不放任意别名，手机本地 UI 用内存映射显示，诊断包用伪名对照。certificate/issuer文本也可能包含私人内容，标准包只放摘要哈希与类型，不自由输出。

## 导出结构

```text
FlClash-diagnostic-<time>.zip
  manifest.json       schema、版本、覆盖范围、事件数、预算、是否截断
  summary.json        成功/失败分布、切换原因、冷却/补位、丢弃计数
  events.jsonl        最近15分钟，若被轮转则注明实际覆盖范围
  README.txt          如何重现、标记时间、无凭据说明
```

生产版再加入 `nodes.csv`（伪名、检测数、失败分类、P95/EWMA、最近成功年龄）、`environment.json`（Android/Core版本、网络WiFi/蜂窝/VPN状态，不含SSID/IP/MAC）、`tls.json`。2MiB硬上限适用于最终文件，导出时固定文件集合，没有“附带整个应用目录”。

ZIP 导出采用锁定只读快照或持有短暂复制锁，不能在轮转同时读取半行；只读完整JSON行。ZIP过大时保留摘要，优先故障/切换，舍弃最旧成功快照。说明覆盖时间、被截断记录数和因预算没测的次数，防止以后误以为“没日志就是没故障”。

调试开关显示倒计时；10分钟自动关，即使UI关闭也按native monotonic deadline结束。重启默认关闭，不持久化无限debug开关。用户标记故障时追加该时间事件，方便与ChatGPT截图对齐。

## 参考文件与验证

`reference/stability_pro/diagnostics.py` 提供线程锁、白名单类型/枚举校验、四文件轮转、debug截止、最近事件导出与大小限制；单元测试检查超大记录、多线程、重启、导出脱敏和容量。它不采集用户系统日志，不读取真实订阅。

生产日志验收：快速产出超过16MiB模拟事件，最终轮转目录仍≤4MiB；调试过期后没有debug事件；raw异常携带密码/URL时标准日志不可见；无UI后台故障产生相同结构事件；导出≤2MiB且测试程序能解析全部JSON。
