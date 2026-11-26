# Xiaomi Camera Direct（小米摄像头本地直连）

在 Home Assistant 里通过**局域网**直接看小米摄像头画面、控制云台。视频流不经过小米
云：Home Assistant 只用小米账号完成登录、拉取设备清单，以及换取每次开流所需的本机
密钥。

> English documentation: [README.md](README.md)

## 现在能做到什么

| 能力 | 说明 |
|---|---|
| 实时画面 | 相机实体提供低延迟 HLS 流，可直接放进任意 dashboard |
| 截图 / 录制 | 标准 `camera` 实体能力，HA 的截图与短片录制都可用 |
| 云台四向 | 已映射驱动的型号自动出现四个方向按钮 |
| 卡片方向盘 | 随集成分发的卡片自带方向键，压在画面上，不用再摆按钮卡 |
| 延迟角标 | 卡片右上角显示距直播边缘的秒数，可关 |
| 声音 | 各型号支持度不同，见[型号能力矩阵](docs/HA-CAMERA-CAPABILITIES.md) |

## 安装

要求 Home Assistant Core **2026.10.4** 或更高，且 HA 能直接访问摄像头的局域网地址。

1. **设置 → Marketplace**，搜索 **Xiaomi Camera Direct**，点**安装**，然后重启
   Home Assistant。Marketplace 是 Home Assistant 2026.11 起内置的 HACS 替代品；
   更老的版本请改用 **HACS → ⋮ → 自定义存储库** 添加
   `https://github.com/moukas/XiaomiCameraDirect`（类型选 Integration）。
2. **设置 → 设备与服务 → 添加集成**，选择 **Xiaomi Camera Direct**，选小米账号
   所在区域并授权。
3. 在账号条目上点**配置**勾选要接入的摄像头（默认全选）。保存后账号条目会重新
   加载，新选的画质档位在下一次开流时生效。

**手动安装**：把 `custom_components/xiaomi_direct_pilot` 整个目录拷进 HA 配置目录
下的 `custom_components`，重启后从第 2 步开始。原有的直连安装沿用同一个集成域名
与配置条目，无需重新添加。

C400 在欧洲区域可勾选 **以较低分辨率串流的 C400 摄像头** 来降带宽。集成的麦克风
相关选项默认关闭，属实验性功能。

## 卡片

卡片随集成一起安装，资源由集成自动注册（URL 带内容哈希，升级后刷新页面即生效），
不需要手动加 Lovelace 资源。

```yaml
type: custom:xiaomi-direct-camera
entity: camera.xiaomi_c701_direct
name: 客厅摄像头
```

| 选项 | 默认 | 作用 |
|---|---|---|
| `entity` | 必填 | 相机实体 |
| `name` | 实体名 | 卡片标题 |
| `ptz` | `true` | 设为 `false` 隐藏方向盘 |
| `latency_indicator` | `true` | 设为 `false` 隐藏右上角延迟秒数 |

交互方式：

- 方向键**点一下走一步**，**按住**每 260 ms 续一步，松手立刻停；同一时刻只有一条
  指令在飞，避免卡顿堆积。
- 没有活跃流时方向盘半透明**且不可点**；视频出第一帧、`ptz_enabled` 变 true 后自动
  变实可点。
- 点画面切静音（浏览器要求静音才允许自动播放）。
- 转向失败时方向盘上方会显示中文原因（例如“请先打开实时画面，再操作云台”），4 秒后
  消失；单条指令超过 4 秒没返回会按超时解锁，不会把方向盘卡死。
- 没有映射驱动表的型号（例如固定机位的 AW300）根本不会显示方向盘——不画按不动
  的按钮。

示例 dashboard 见 [docs/examples/lovelace-xiaomi-direct.json](docs/examples/lovelace-xiaomi-direct.json)，
只需要把实体 ID 换成你自己的。

## 延迟

卡片自己播放，不再套第三方卡片：

```
camera/stream (WS) ──► hls.js ──► <video>
                        ↑
        /xiaomi-direct/hls.light.min.js（集成自带）
```

播放参数写死在卡片里，不让播放器自己漂：

```js
lowLatencyMode: true,
backBufferLength: 0,
liveSyncDuration: 0.6,        // 目标延迟（= 3 个 0.2 秒分片）
liveMaxLatencyDuration: 3,    // 超过 3 秒硬跳
maxLiveSyncPlaybackRate: 1.5, // 落后时 1.5 倍速追帧
```

集成侧同时把流本身从默认的 1 秒分片 / 6 秒分段收紧到 **0.2 秒分片 / 2 秒分段**
（`camera.py` 顶部 `LOW_LATENCY_PART`、`LOW_LATENCY_SEGMENT`）。这两项改在
`create_stream` 内部临时替换全局 `StreamSettings`，建完立即还原，**只影响本集成的
相机**；若你在 `configuration.yaml` 里设了更小的值，不会被反向拉高。替换过程用一把
锁串行，避免两台相机同时建流时互相把对方的设置还原掉。

播放器的目标延迟不能低于 HA 的 `PART-HOLD-BACK`（= 2 个分片），所以 **分片长度
才是延迟的地板**：0.3 秒分片最多压到 0.6，0.2 秒分片才能压到 0.4~0.6。

实测（`chuangmi.camera.029a02`，HA 2026.11.2）：

| 指标 | 改前 | 0.3 秒分片 | 现在 |
|---|---|---|---|
| `PART-TARGET` | 1.000 | 0.300 | **0.200** |
| `PART-HOLD-BACK`（≈地板） | 2.000 | 0.600 | **0.400** |
| `liveSyncDuration` | 播放器默认（漂到 3~5 s） | 0.9 | **0.6** |
| `EXT-X-TARGETDURATION` | 6 | 2 | **2** |

分片结构实测为纯 0.2 秒（每段 10 个，无长尾碎块），`TARGETDURATION` 仍是 2，退化成
整段播放时不会再有 5 秒跳变。

体感上稳定在 **1 秒左右**，最坏情况等一个 2 秒分段。

另外 ffmpeg 的输入探测从 750 ms 降到 300 ms（`PROBE_OPTIONS_VIDEO`）：开流到出第一帧
的等待少了一半左右。带音频的流保持原来的 750 ms，因为音轨可能晚于视频包出现。

**为什么不走 WebRTC**：本集成的媒体中继是 **MPEG-TS over HTTP**
（`http://127.0.0.1:随机端口/随机 token`），而 HA 内置的 go2rtc provider 只认
`rtsp:` / `rtmp:` 开头的源，`camera/webrtc/offer` 会直接返回
`Camera does not support WebRTC`。所以装 WebRTC Camera 也没用——它自带的 go2rtc
同样拉不到这个地址，端口和 token 每次重启都变，无法写死。

## 云台支持范围

目前只有两台带方向驱动的机型会生成按钮与方向盘：

| 型号 | 标识 | 说明 |
|---|---|---|
| 小米智能摄像机 云台版 2K | `chuangmi.camera.029a02` | 四向单步 |
| 小米智能摄像机 3 云台版 | `chuangmi.camera.069a01` | 四向单步 |

协议只有离散单步指令（`0x112`，左 1 / 右 2 / 上 3 / 下 4），**没有连续转动**。
所以“按住”本质是连续发单步，不是匀速云台。新增型号需要先抓到该型号的指令映射，
再在 `const.py` 的 `PTZ_OPERATIONS` 与型号表里登记。

## 网关模式（可选，旧方案）

除了上面的原生集成，仓库里还有一个 Go 网关，用于诊断、对照测试，以及给 Frigate
这类需要 RTSP/ONVIF 的场景使用。它可以让 HA 通过 RTSP 收流、通过 ONVIF 控云台，
也可以用受权的小米账号自己协商本地媒体。细节与部署步骤见英文 README 的
**Xiaomi Local Gateway** 一节，以及
[相机证据](docs/LOCAL-CAMERA-PROGRESS.md)、[托管 API](docs/API-MANAGED.md)、
[直连集成计划](docs/HA-DIRECT-PLAN.md)。

## 架构

```text
小米摄像头 ── 局域网 MISS/CS2 ── 补丁版 go2rtc + xiaomi-onvif（外部）
                                    │ RTSP    │ ONVIF
                                    │         └── 网关 Go REST API
                                    │                  └── HA 云台按钮
                                    ├── Home Assistant（相机流）
                                    └── Frigate（录制 / 检测 / ONVIF 云台）
```

上图是桥接模式。原生直连模式下，集成自己在 HA 进程里跑 MISS/CS2 会话，媒体经
回环 HTTP 交给 HA 的 `stream` 集成转成 HLS。

## 可靠性与安全

- 启动前严格校验配置，重复的摄像头 ID 直接拒绝。
- HTTP 端点有超时上限，云台指令**不做**危险的重试。
- REST API 需要 Bearer 令牌，默认只监听 `127.0.0.1`。
- 默认不向公网暴露任何画面；上游 RTSP/ONVIF 需要你自己加防火墙。
- 不谎报健康状态：`readyz` 只表示网关配置加载成功。
- 仓库内不存放任何摄像头凭据。
- **原厂固件下协商新 MISS 会话仍然需要小米云**，这是协议决定的，不是实现缺陷。
- 网关走明文 HTTP，跨信任边界时请套 TLS 反向代理。
- 上游 xiaomi-onvif 仍是 beta，升级前先锁版本并测试。

## 开发与测试

Windows 上跑 `./scripts/test-camera-quick.ps1`，会在一次性的 WSLc Linux 容器里编译
并跑测试。Linux 上直接用同一个 Dockerfile，或装好 Go 与 Node 后跑
`sh scripts/test-camera-quick.sh`。[集成测试指南](tests/README.md) 说明了隔离 HA
环境的搭法。

```bash
go test -race ./...
go vet ./...
go run ./cmd/gateway -check-config -config config.example.json
python -m compileall -q custom_components legacy/custom_components
node tests/test_ha_direct_camera_card.cjs   # 卡片测试（假 DOM + 假 hls.js + 假时钟）
```

## 外部引用与许可

- [xiaomi-camera-viewer](https://github.com/lubos-hnanicek/xiaomi-camera-viewer)（MIT）— MISS/CS2 密钥协商、局域网发现、PTZ CW400/CW500
- [xiaomi-onvif](https://github.com/SincereXIA/xiaomi-onvif)（MIT）— 补丁版 go2rtc、ONVIF 云台/Frigate
- [ha-xiaomi-camera](https://github.com/willjayyyy/ha-xiaomi-camera)（Apache-2.0）— HA/add-on 交互设计参考
- [hls.js](https://github.com/video-dev/hls.js)（Apache-2.0）— 卡片自带的播放器，许可证见 `custom_components/xiaomi_direct_pilot/www/LICENSE.hls.js`
- [Frigate restream 文档](https://docs.frigate.video/configuration/restream/)

外部组件不随本仓库分发，各自许可证独立适用。原生网关与本集成为 MIT 许可，见
`LICENSE`。
