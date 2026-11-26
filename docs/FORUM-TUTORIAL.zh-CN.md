# 小米摄像头不进云，直接进 Home Assistant：完整上手（含白名单与 AI 调参）

> 适用：Xiaomi Camera Direct 0.5.x，Home Assistant Core 2026.10.4 及以上。
> 内容按仓库当前代码和实测记录整理。没有实测证据的地方会标"未验证"。

---

## 0. 这套东西到底做了什么

市面上的小米摄像头接 HA 方案，大多是"从米家 App 抓一条云端地址，塞给 HA 当 RTSP 播"。这条路的问题大家都懂：地址会过期，摄像头重启换 IP 就废，画质还要被云端转一道。

这个集成换了个做法：

- 小米账号只干三件事：登录、拉设备清单、换取每次开流要用的本机密钥。
- **画面走局域网**：HA 自己和摄像头做 MISS/CS2 协商，媒体流在同二层网络里跑，不经过小米服务器。
- 云台指令也走局域网，四向单步，不依赖 ONVIF。

一句话：**登录要上网，看画面不上网。**

必须先说清楚的地方：原厂固件下，每次新建 MISS 会话仍然要向小米云要密钥。这是协议决定的，不是实现偷懒。所以"完全离线"是做不到的——你能做到的是"画面数据不出门"。

---

## 1. 先看你的机器在不在支持范围

| 型号 | 账号里的标识 | 实时画面 | 云台四向 | 声音 |
|---|---|---|---|---|
| 小米智能摄像机 C701 | `chuangmi.camera.079ae2` | 4K 实拍通过 | 有 | 有 |
| 小米智能摄像机 C201 | `xiaomi.camera.c201` | 通过（见下方备注） | 有 | 有 |
| 小米智能摄像机 C400 | `chuangmi.camera.039a04` | 通过 | 有 | 有 |
| Mi 360 1080p | `chuangmi.camera.026c02` | 通过 | 有 | 未验证 |
| 户外 AW300 | `mxiang.camera.mod13` | 通过 | **没有**（固定机位） | 输入有音，HLS 未提供音轨 |
| 云台版 2K | `chuangmi.camera.029a02` | 通过 | 有 | 未验证 |
| 小米智能摄像机 3 云台版 | `chuangmi.camera.069a01` | 通过 | 有 | 未验证 |

两条备注，都别忽略：

- **云台按钮**：除 AW300 之外，上面所有型号都会生成四向按钮（它们共用同一份指令映射）。但物理上真正确认过能转的只有 C201、C400、Mi 360 三台。表里写"有"是指按钮会出来，不代表我一定试过你那台。
- **C201 画面**：不同固件/网络下表现不一致，有一次复测出现 HLS 超时但截图正常。遇到这种情况先看第 9 节，不要直接判定不支持。

不在表里的型号也能被识别出来，但会显示成 `model_profile_pending`——意思是"账号里看到了这台机器，但没有对应的传输配置"。这种情况提 issue 时把你的型号和账号区域写上。

---

## 2. 装之前，先确认这三件事

**1）HA 版本够。** 设置 → 关于，Core 版本必须 ≥ 2026.10.4。

**2）HA 和摄像头在同一个二层网络。** 注意：不是"能 ping 通"，是同一个广播域。集成靠 ARP 表把账号里的设备 ID 对上局域网 IP，中间隔了三层路由、VLAN 隔离、或者摄像头在 IoT 独立 SSID 且开了客户端隔离，都会卡在"找不到设备"。

**3）HA 所在的机器能直连摄像头。** 如果你把 HA 跑在 Docker 的 bridge 网络里，本身不影响——这是 HA 主动出站连摄像头。但如果是 macvlan 或者和摄像头不同网段，就别折腾了。

---

## 3. 安装

### 3.1 Marketplace（HA 2026.11 起）

设置 → Marketplace → 搜索 **Xiaomi Camera Direct** → 安装 → 重启 HA。

Marketplace 是 HA 新内置的 HACS 替代品。如果你的 HA 还没有这一项，走 3.2。

### 3.2 HACS

HACS → 右上 ⋮ → 自定义存储库 → 填 `https://github.com/moukas/XiaomiCameraDirect`，类型选 **Integration** → 安装 → 重启。

### 3.3 手动

把仓库里 `custom_components/xiaomi_direct_pilot` 整个目录（注意是整个目录，不是里面的文件）拷进 HA 配置目录下的 `custom_components/`，重启。

三种方式装出来的集成域名都是 `xiaomi_direct_pilot`，所以从旧方式迁移到新方式时，**不要删配置条目再重加**——原有条目会直接沿用。

---

## 4. 添加账号

设置 → 设备与服务 → 添加集成 → 搜 **Xiaomi Camera Direct**。

1. **区域**：选你小米账号注册的区域。国内账号选 `cn`，欧洲选 `de`，其余按实际。选错的表现是"账号下没有摄像头"，不是登录失败——这个坑很常见。
2. **授权方式**二选一：
   - **二维码**（推荐）：集成会显示一个二维码，用米家 App 扫码确认。不用在小窗里敲密码，也不容易触发风控。
   - **账号密码**：直接填。可能会遇到图形验证码或短信验证码，按提示走完。
3. 授权成功后账号条目就建好了，摄像头会自动出现在账号下。

---

## 5. 选择要接入的摄像头

在刚建好的账号条目上点**配置**：

- **摄像头**：多选，默认全选。
- **音频**（按型号出现）：C201 / C400 / C701 / AW300 各自的开关，默认关闭。这属于实验性功能，开了之后 HA 里才有声音。
- **以较低分辨率串流的 C400**：C400 专用，带宽紧张时勾上。

保存后账号条目会自动重载，画质档位在下一次开流时生效，不用重启 HA。

保存完你会得到：

| 实体 | 说明 |
|---|---|
| `camera.xxx_direct` | 实时画面，标准 camera 实体，截图/录像都能用 |
| `button.xxx_left/right/up/down` | 四向云台按钮（AW300 没有） |
| `binary_sensor.xxx_*` | C201 的诊断实体，读指示灯/移动侦测/人形侦测等设置，默认关闭 |

---

## 6. 加卡片

卡片资源随集成自动注册（URL 带内容哈希，升级后刷新页面就生效），**不需要手动加 Lovelace 资源**。

```yaml
type: custom:xiaomi-direct-camera
entity: camera.xiaomi_c701_direct
name: 客厅摄像头
ptz: true
latency_indicator: true
```

| 选项 | 默认 | 作用 |
|---|---|---|
| `entity` | 必填 | 相机实体 |
| `name` | 实体名 | 卡片标题 |
| `ptz` | `true` | 设 `false` 隐藏方向盘 |
| `latency_indicator` | `true` | 设 `false` 隐藏右上角延迟秒数 |

用起来的实际手感：

- 方向键**点一下走一步**，**按住**每 260 ms 续一步，松手立刻停。同一时刻只有一条指令在飞，不会越按越卡。
- 没有活跃流时方向盘是半透明的，**点不动**。等视频出第一帧、`ptz_enabled` 变 true 才会实心可点。这是故意的，防止对着没有画面的流瞎发指令。
- 点画面静音——浏览器不静音就不让自动播放，没得选。
- 云台失败会在方向盘上方弹一行中文原因，4 秒后消失。单条指令超过 4 秒没返回会按超时解锁，方向盘不会卡死。
- 没有映射驱动的型号压根不画方向盘。不画按不动的按钮。

---

## 7. 延迟：为什么它比"抓 RTSP"快

集成的卡片自己播，不套第三方播放器：

```
camera/stream (WS) ──► hls.js ──► <video>
                        ↑
        /xiaomi-direct/hls.light.min.js（集成自带，不用装）
```

播放参数写死在卡片里，不让播放器自己漂：

```js
lowLatencyMode: true,
backBufferLength: 0,
liveSyncDuration: 0.9,        // 目标延迟
liveMaxLatencyDuration: 4,    // 超过 4 秒硬跳
maxLiveSyncPlaybackRate: 1.3, // 落后时 1.3 倍速追帧
```

集成侧同时把 HLS 从默认的 1 秒分片 / 6 秒分段收紧到 **0.3 秒分片 / 2 秒分段**，只对本集成的相机生效，不会污染全局设置（内部是临时替换后立刻还原，还加了一把锁，两台相机同时建流也不会互相踩）。

实测（`chuangmi.camera.029a02`，HA 2026.11.2）：

| 指标 | 默认 | 现在 |
|---|---|---|
| `PART-TARGET` | 1.000 | **0.300** |
| `PART-HOLD-BACK`（≈延迟） | 2.000 | **0.600** |
| `EXT-X-TARGETDURATION` | 6 | **2** |

体感稳定在 **0.6~1.5 秒**，最坏情况等一个 2 秒分段。你家的数字不一定一样，WiFi 弱、摄像头码率高会更差。

**为什么不走 WebRTC**：这个集成的媒体中继是 MPEG-TS over HTTP，监听在 `127.0.0.1` 的随机端口 + 随机 token。HA 内置的 go2rtc 只认 `rtsp:`/`rtmp:` 开头的源，`camera/webrtc/offer` 会直接回 `Camera does not support WebRTC`。装 WebRTC Camera 也没用，它自带的 go2rtc 一样拉不到这个地址，端口和 token 每次重启都变，写不死。

---

## 8. 云台能做什么、不能做什么

协议只有离散单步指令（`0x112`，左 1 / 右 2 / 上 3 / 下 4），**没有连续转动**。

所以"按住"本质是连续发单步，不是匀速云台。想让它平滑地一直转，做不到；想精确对准某个位置，得自己按。

新增型号需要先抓到该型号的指令映射，再在 `const.py` 的 `PTZ_OPERATIONS` 和 `button.py` 的型号清单里登记。两份对不上，按钮就不会出现。

---

## 9. 出问题先查这 8 条

| 现象 | 大概率原因 | 怎么查 |
|---|---|---|
| 添加集成时提示账号下没有摄像头 | **区域选错** | 换成账号实际注册区域（国内 `cn`）重试 |
| 实体有了，画面一直转圈 | 开流失败：网络不同二层、摄像头 IP 变了、固件不支持 | 先试"截图"——截图走独立路径，能出图说明设备可达，问题在 HLS |
| 截图也失败 | HA 到摄像头不通 | 从 HA 的宿主 ping 摄像头；查 ARP 表里有没有它的 MAC |
| 方向盘灰的、点不动 | 没有活跃流 | 先打开实时画面，等第一帧；`ptz_enabled` 变 true 才可点 |
| 根本没有方向盘 | AW300 固定机位，或型号未映射 | 看第 1 节表格 |
| 实体名后面带 `model_profile_pending` | 型号不在传输配置表里 | 提 issue 带型号 + 区域 |
| 升级后卡片异常 | 浏览器缓存了旧资源 | 先强制刷新（Ctrl+F5）再排查；资源 URL 带内容哈希，正常刷新一次就应恢复 |
| 画面偶尔卡 2 秒再追上 | 落后超过阈值触发硬跳 | 正常现象，是防延迟累积的设计 |

---

## 10. 进阶：网关模式（要 RTSP / Frigate 的人看这里）

原生集成只给 HA 用。如果你还要接 Frigate 录像、或者要一条标准 RTSP 给别的系统，仓库里另有一个 Go 网关。

**先说门槛，别浪费时间**：网关用的是"托管模式"，需要 `XLG_XIAOMI_USER_ID`、`XLG_XIAOMI_REGION`、`XLG_XIAOMI_TOKEN` 三个值。这三个值来自本地 Xiaomi Viewer 的加密 profile，**目前没有一键导出工具**。如果你只是想"在 HA 里看到画面"，用第 3 节的原生集成，别碰网关。

### 10.1 起容器

```sh
docker build --tag xlg-gateway:local .
docker run -d --name xlg-gateway --restart unless-stopped --network host \
  --mount type=volume,source=xlg-managed-data,target=/data \
  --env-file /path/to/private/xlg-managed.env \
  xlg-gateway:local -managed -listen 192.168.1.20:8099
```

`--network host` 是必须的，摄像头的 CS2 发现报文要能进来。

`xlg-managed.env` 长这样（值自己填，**别提交到 git**）：

```sh
XLG_API_TOKEN=<至少 24 位的随机串，用 openssl rand -hex 32 生成>
XLG_XIAOMI_USER_ID=<账号 user id>
XLG_XIAOMI_REGION=cn
XLG_XIAOMI_TOKEN=<账号 token>
XLG_RTSP_ADVERTISE_HOST=192.168.1.20
XLG_RTSP_LISTEN=192.168.1.20:8554
```

### 10.2 HA 侧

把 `legacy/custom_components/xiaomi_local_gateway` 拷进 HA 的 `custom_components`，重启，然后添加 **Xiaomi Gateway (Legacy)** 集成，填网关地址和 API token。

### 10.3 简单验一下

```sh
curl -H "Authorization: Bearer YOUR_TOKEN" http://192.168.1.20:8099/v1/cameras
```

返回相机列表就说明网关活了。注意 `readyz` 只代表"配置加载成功"，不代表摄像头能用——这个项目不谎报健康状态。

---

## 11. 本地媒体白名单：让"只能连局域网"变成硬约束

这一节只对上面第 10 节的网关模式有效。用原生集成的人可以跳过。

### 11.1 它是干什么的

默认情况下，网关会尽力走局域网，但"尽力"是个软约束。白名单是把它变成硬约束的开关：

- 摄像头 IP **必须**落在你列出的网段里，否则拒开；
- 内核选出的路由**必须**走你指定的那张网卡，否则拒开；
- 校验不通过时**不发出任何 MISS 云请求**，直接失败。

也就是说，配好白名单之后，"我的摄像头流量跑到公网了"这件事在代码层面不可能发生。

### 11.2 格式（这些是硬规则，错一个字符直接启动失败）

```
CIDR@网卡名;CIDR@网卡名
```

```sh
XLG_XIAOMI_LOCAL_MEDIA_REQUIRED=true
XLG_XIAOMI_LOCAL_MEDIA_NETWORKS=192.168.1.0/24@以太网
```

规则清单：

- 每条必须是 `网段@网卡名`，`@` 两侧**不能有空格**（写成 `192.168.1.0/24 @ 以太网` 直接判非法）；
- 多条用**英文分号** `;` 分隔，最多 **16 条**；
- 整串最长 **2048 字节**；
- 网段必须是合法 CIDR，`192.168.1.1` 这种不带掩码的不行；
- 网卡名里**不能含 `@`**（设计上就不支持，避免歧义）；
- 开了 `XLG_XIAOMI_LOCAL_MEDIA_REQUIRED=true` 却没配网段，网关直接起不来——这是故意的，fail-closed。
- **`XLG_XIAOMI_LOCAL_MEDIA_REQUIRED` 才是总开关**。只填 `NETWORKS` 不设 `REQUIRED=true`，网关只校验格式、不会真的拦截，你会以为自己守住了其实没有。两个都要给。

### 11.3 网卡名到底该写什么

这是最容易错的地方。要写的是**适配器名称**——`Get-NetAdapter` 的 `Name` 列，或者 `ip -o` 输出的第一列。不是 `InterfaceDescription`（那是芯片型号，比如 `Realtek PCIe GbE Family Controller`），也不是你在别处给网卡起的备注名。

Linux：直接看内核接口名，`ip -o` 输出的第一列就是：

```sh
ip -4 -o addr show scope global
# 2: enp3s0    inet 192.168.1.20/24 brd 192.168.1.255 scope global enp3s0
#             ^^^^^^ 这个就是网卡名
```

Windows（PowerShell）：

```powershell
Get-NetAdapter | Select-Object Name, InterfaceDescription, Status
```

中文系统上这里会显示"以太网"、"WLAN"这样的中文名，Go 读到的也是同样的名字，可以直接用。但 `.env` 文件建议存成 **UTF-8 无 BOM**，否则部分编辑器写出的 GBK 会让中文名变成乱码，然后被判非法。

Docker 的 host 网络模式下，容器里看到的接口名和宿主一样，照上面的结果填即可。

### 11.4 写到哪里

三选一，别重复设：

```sh
# 方式一：docker --env-file
--env-file /path/to/private/xlg-managed.env

# 方式二：compose
environment:
  XLG_XIAOMI_LOCAL_MEDIA_REQUIRED: "true"
  XLG_XIAOMI_LOCAL_MEDIA_NETWORKS: "192.168.1.0/24@enp3s0"

# 方式三：直接跑 go
XLG_XIAOMI_LOCAL_MEDIA_REQUIRED=true \
XLG_XIAOMI_LOCAL_MEDIA_NETWORKS='192.168.1.0/24@enp3s0' \
  go run ./cmd/gateway -managed -listen 192.168.1.20:8099
```

### 11.5 报错怎么读

白名单校验分两个阶段，报错形态完全不同：

**阶段一：启动时格式校验。** 格式错了网关直接退出。注意这里有个坑：启动日志不会打印"白名单写错了"，只会打一句类似这样的东西

```
Xiaomi camera backend unavailable (*errors.errorString)
```

看不出是白名单问题。判断方法：把 `XLG_XIAOMI_LOCAL_MEDIA_REQUIRED` 和 `XLG_XIAOMI_LOCAL_MEDIA_NETWORKS` 两个变量临时清空再启动，能起来就说明是白名单背锅。

**阶段二：运行时策略拒绝。** 格式对但实际情况不匹配，日志里会是：

```
xiaomi_backend event=local_media_preflight result=error model_profile=%s reason=<原因>
```

`reason` 对照表：

| reason | 意思 | 怎么改 |
|---|---|---|
| `policy_invalid` | 配置本身不合法（格式、条数、字节数） | 回 11.2 逐条对规则 |
| `target_outside_allowed_network` | 摄像头 IP 不在你列的网段里 | 网段写错，或摄像头换了 IP |
| `route_interface_mismatch` | 内核选的路由走的不是你以为的那张网卡 | 网卡名写错了，或这台机器有双网卡 |
| `local_interface_unverified` | 找不到对应接口，或者本机地址不唯一 | 查 `ip -o` 是否有同一地址挂在两张卡上（VPN、虚拟网桥常见） |
| `identity_unverified` | 设备 ID / 型号 / 固件跟清单对不上 | IP 被 DHCP 分给了别的设备，或 MAC 变了 |
| `inventory_stale` | 读取过程中摄像头地址变了 | 给摄像头配 DHCP 静态绑定 |
| `target_invalid` | 摄像头拿到的是公网/回环/组播地址 | 账号清单里的地址异常，重新拉清单 |
| `cloud_material_unavailable` | 向小米云要密钥失败 | 网络/账号问题，不是白名单问题 |
| `session_open` | 密钥拿到了但会话建不起来 | 摄像头忙、并发开流数超限 |

### 11.6 用 AI 改白名单（这节是重点）

先说 AI 能干什么、不能干什么，别抱错期待：

**AI 猜不出你机器的网卡名。** 任何模型都会一本正经地给你编一个 `Ethernet0` 或 `eth0`。它只能做一件事——把你贴给它的真实输出，转成符合 11.2 规则的那一行字符串。所以**必须贴原始命令输出**，贴得越全越准。

值得用 AI 的理由很实际：Windows 中文网卡名、双网卡选哪张、摄像头跨了子网掩码怎么算 CIDR、`@` 前后多打了个空格——这些错误人眼扫三遍都发现不了，而网关只会丢给你一句 `backend unavailable`。

#### 第一步：把这三样准备好

1. 网关所在主机的系统（Windows 11 / Debian 12 / 群晖 DSM 7 …）
2. 网卡信息原始输出
   - Linux：`ip -4 -o addr show scope global`
   - Windows：`Get-NetAdapter | Select-Object Name,Status` + `Get-NetIPAddress -AddressFamily IPv4 | Select-Object IPAddress,PrefixLength,InterfaceAlias`
3. 摄像头的局域网 IP（在路由器 DHCP 列表里能看到），以及 HA 的 IP

#### 第二步：把这段 prompt 丢给任意大模型

```
你是网络配置助手。把下面信息转换成 Xiaomi Local Gateway 的本地媒体白名单环境变量值。

输出格式硬规则：
- 每条是 CIDR@网卡名，多条用英文分号分隔
- @ 两侧不能有任何空格
- 最多 16 条，整串不超过 2048 字节
- 网卡名必须与命令输出里的接口名逐字一致，不要翻译、不要改写、不要猜测
- 不要输出 0.0.0.0/0，不要输出公网网段
- 只允许私网网段（10/8、172.16/12、192.168/16）

我提供的信息：
1. 系统：<例如 Windows 11 中文版>
2. 网卡输出：
<把命令原始输出粘在这里>
3. 摄像头 IP：<例如 192.168.1.31、192.168.1.32>
4. HA 地址：<例如 192.168.1.10>

输出：
第一行只给可直接粘贴的 XLG_XIAOMI_LOCAL_MEDIA_NETWORKS 值，不要加引号、不要加变量名。
第二行用一句话说明为什么选这张网卡。
如果信息不足，直接列出缺什么，不要猜。
```

它应该回你类似这样的一行（这是示例，不是标准答案）：

```
192.168.1.0/24@以太网
```

#### 第三步：报错了再让 AI 修

格式错了，或者日志里出现 `target_outside_allowed_network` / `route_interface_mismatch`，把这三样贴回去：

```
这个白名单没通过：
现象：<粘贴日志里的 reason，或"网关启动即退出">
当前值：<你填的那一行>
网卡输出：<原始输出，重贴一次>
摄像头 IP：<实际 IP>

请指出具体哪一条、哪一段不合法，给出修正后的完整值。
如果问题不在白名单本身（比如是双网卡路由问题），直接说明，不要硬凑一个值。
```

最后那句"不要硬凑"很重要。AI 在被要求"给个答案"时，会倾向于编一个看起来合理的值——而这里编一个值的代价是网关起不来，你还以为是自己网络有问题。

#### 第四步：改完必须做的三件事

1. **先备份**当前的 env 文件（`cp xlg-managed.env xlg-managed.env.bak`）。改错了能一秒回滚。
2. **看它能不能稳住**：重新起网关，日志里没有 `Xiaomi camera backend unavailable`、进程不退出，才算格式过关。
3. **故意制造一次拒绝**，确认策略真的在生效：临时把白名单改成 `192.168.99.0/24@<你的网卡名>`，重启，看日志里出不出 `target_outside_allowed_network`。出来了说明白名单真的在拦，再改回去。

第 3 步很多人跳过，然后一直以为自己配的生效了——实际上 `REQUIRED` 没开的话，那一行网段就只是个摆设。

#### 别把这些喂给 AI

账号密码、`XLG_XIAOMI_TOKEN`、`XLG_API_TOKEN`、公网 IP。白名单这一段你只需要给内网网段和网卡名，给多了纯属自找麻烦。

---

## 12. 安全边界与已知限制

写在这儿，省得你踩完再回来问。

- **必须能连小米云才能开新流**。原厂固件决定的。断网期间开不了新流，重启 HA 也得等云恢复。
- **必须同二层**。三层路由、VLAN 隔离、客户端隔离都会失败。
- **云台只有单步**，没有连续转动，没有绝对位置。
- **声音是实验性功能**，各型号支持度不一，HA 里能不能播还取决于浏览器解码。
- **默认不向公网暴露任何画面**。网关 REST API 要 Bearer token，默认只监听 `127.0.0.1`；跨信任边界请自己套 TLS 反向代理。
- **仓库里不存任何摄像头凭据**。你的 token 存哪儿自己负责。
- **上游依赖是 beta**：xiaomi-onvif 还是 beta，升级前锁版本、先测再上。

---

## 附：常用命令速查

```bash
# 网关配置静态校验（只校验 JSON，不校验白名单环境变量）
go run ./cmd/gateway -check-config -config config.json

# 拉相机列表
curl -H "Authorization: Bearer $TOKEN" http://127.0.0.1:8099/v1/cameras

# 云台单步
curl -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"direction":"left"}' http://127.0.0.1:8099/v1/cameras/garden/ptz/step

# 本机网卡与地址（Linux）
ip -4 -o addr show scope global

# 本机网卡与地址（Windows PowerShell）
Get-NetIPAddress -AddressFamily IPv4 | Select-Object IPAddress,PrefixLength,InterfaceAlias
```

注意第一条：`-check-config` **只管 JSON**，不看白名单。白名单只在真正启动 provider 时才校验，错了直接退出、还不告诉你原因。这一点很容易搞错，先记住能省半小时。
