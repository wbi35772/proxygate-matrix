# 架构

这份文档回答三个问题：进程里有什么、一次请求怎么走、槽位为什么会换节点。

## 系统上下文

```mermaid
C4Context
    title ProxyGate Matrix 上下文
    Person(operator, "运维", "面板、配置、排障")
    Person(client, "终端用户", "订阅客户端 / 指纹浏览器")
    System(pg, "ProxyGate Matrix", "节点库、槽位矩阵、订阅、本地接入")
    System_Ext(pool, "VPNGate 池", "公共节点列表与测速数据")
    System_Ext(edge, "TLS 边缘", "Instabox / Nginx / Caddy")
    System_Ext(site, "目标站点", "被访问的业务")

    Rel(operator, pg, "8080 面板")
    Rel(client, edge, "8090 订阅 / Trojan")
    Rel(edge, pg, "反代到 8090")
    Rel(pg, pool, "HTTP 采集")
    Rel(pg, site, "经槽位出口访问")
```

## 进程与端口

一个二进制里跑着三套监听：Fiber 面板、订阅 HTTP 服务、每个槽位一个 SOCKS5 监听。

```mermaid
block-beta
  columns 4
  panel["Fiber 面板<br/>:8080"] sub["订阅服务<br/>:8090"] slot["槽位 SOCKS5<br/>:10801+"] local["本地 sing-box<br/>127.0.0.1:12001+"]
  store["SQLite /data/proxygate.db"] guard["拨号闸门"] manager["槽位管理器"]
  panel --> manager
  sub --> manager
  slot --> manager
  manager --> store
  manager --> guard
  local --> sub
```

| 组件 | 代码 | 职责 |
| --- | --- | --- |
| 面板 | `internal/server` + `web/` | 登录、槽位、配置、分享页 |
| 槽位管理器 | `internal/matrix` | 分配、候选、拨号、轮换、健康 |
| 节点采集 | `internal/vpngate` | 拉列表、测速、ASN、分类 |
| 订阅服务 | `internal/subscription` | `/sub/*`、Trojan over WS、VLESS 兼容 |
| 存储 | `internal/store` | SQLite WAL，节点与矩阵元数据 |
| 前置代理 | `internal/frontproxy` | 拨号链路：slot / api / socks5 / http |

## 数据流

```mermaid
flowchart TD
  H[每 3 分钟采集] --> P[(nodes 表)]
  P --> Q{候选查询}
  Q -->|国家 / 协议 / 端口 / 冷却| C[候选列表]
  C --> G{拨号闸门}
  G -->|令牌 / 每端口 / 每小时| D[建立隧道]
  D --> V[探测出口 IP 与延迟]
  V -->|成功| R[(matrix_nodes 质量分)]
  V -->|失败| F[冷却 + 失败计数]
  R --> S[槽位 Ready]
  S --> SUB[订阅渲染]
  S --> LOC[本地接入包]
```

## 一次订阅请求

```mermaid
sequenceDiagram
    autonumber
    participant C as 客户端
    participant E as TLS 边缘
    participant S as 订阅服务 :8090
    participant M as 槽位管理器
    participant D as SQLite

    C->>E: GET /sub/TOKEN/v2ray?countries=JP,KR
    E->>S: 反代（明文 HTTP）
    S->>S: 匹配 token / 推断格式
    S->>M: Status()
    M->>D: 读取矩阵节点
    M-->>S: slots[]
    S->>S: 过滤 ready+healthy+exitIp
    S->>S: 国家 / 国家排除 / 槽位 / 排序 / 截断
    S-->>C: base64(trojan://...)
    Note over S,C: X-ProxyGate-Nodes 等响应头
```

## 槽位状态机

```mermaid
stateDiagram-v2
    [*] --> Switching: Allocate
    Switching --> Ready: 拨号成功且出口可用
    Switching --> Error: 候选耗尽或全部冷却
    Ready --> Switching: Rotate / 健康失败 / ModeRotating 到期
    Error --> Switching: 冷却结束或手动重试
    Ready --> [*]: 释放
```

- `ModeSticky`：默认，保持同一出口，只有失败或手动轮换才换。
- `ModeRotating`：`rotateInterval` 到期后换节点；默认 5 分钟，只在轮换模式生效。
- `Ready` 之外的状态不会进入订阅与本地接入包。

## 核心类型

```mermaid
classDiagram
    class Manager {
      +Allocate(AllocateOptions) (Slot, bool, error)
      +Rotate(slotID int) error
      +Status() ([]SlotStatus, int)
      +DialSlot(ctx, slotID, network, target)
      +Stats(ctx) Stats
    }
    class Slot {
      +SlotID int
      +Country string
      +Mode string
      +Configure(AllocateOptions) error
      +maybeRotate()
      +Status() SlotStatus
    }
    class dialGuard {
      +Acquire(ctx, ip, port) error
      +FilterCandidates([]Candidate) []Candidate
      +Snapshot() DialStats
    }
    class Store {
      +MatrixNodes(ctx)
      +SaveMatrixNode(ctx, node)
    }
    class SubscriptionServer {
      +Start()
      +handleSubscription()
      +handleTrojan()
    }
    Manager "1" --> "*" Slot
    Manager --> dialGuard
    Manager --> Store
    SubscriptionServer --> Manager
```

## 数据库

```mermaid
erDiagram
    NODES {
      text ip PK
      text host_name
      int port
      int score
      int ping_ms
      int speed_bps
      text country_short
      text protocols
      int measured_bps
      text last_failure
    }
    MATRIX_NODES {
      text ip PK
      int port PK
      text protocol PK
      text asn
      text asn_org
      int residential
      int fail_count
      int success_count
      real quality_score
      text cooldown_until
      text source
      text last_seen
    }
    METADATA {
      text key PK
      text value
    }
```

`matrix_nodes` 在 `nodes` 之上补了 ASN、住宅标记、质量分、成功/失败计数与冷却时间；主键是 `(ip, port, protocol)`，所以同一 IP 的不同协议可以分别评分。

## Trojan over WebSocket

订阅下发的是 `trojan://` 链接，不是 VLESS。原因是 Trojan 的 UDP 帧简单，mihomo、sing-box、Xray 都支持；VLESS 的 mux/XUDP 在网关侧没有实现。

```mermaid
packet-beta
  0-55: "key = hex(sha224(password))，56 字节"
  56-63: "CRLF"
  64-71: "CMD：0x01 TCP / 0x03 UDP"
  72-79: "ATYP / 地址"
  80-95: "PORT"
  96-111: "LEN + CRLF（UDP 帧）"
```

TCP 走 `DialSlot(ctx, slotID, "tcp", target)`；UDP 由槽位开一个非连接套接字，按帧里的目标地址转发，域名在隧道内解析并按会话缓存。

## 出站链路

```mermaid
flowchart LR
  A[槽位拨号] --> B{front proxy}
  B -->|关闭| C[直连节点]
  B -->|slot| D[借另一个 Ready 槽位]
  B -->|api| E[外部 dialer API]
  B -->|socks5 / http| F[固定上游]
  B -->|list| G[代理列表轮换]
  D --> H[目标节点]
  E --> H
  F --> H
  G --> H
  C --> H
```

超时默认 `15s`，可在面板「设置 → 系统 → 前置代理」或 `PUT /api/matrix/front-proxy` 修改。

## 启动顺序

1. 读取 `config.json`，应用环境变量覆盖，校验并持久化。
2. 打开 SQLite，执行迁移。
3. 启动面板 :8080。
4. 启动订阅服务 :8090。
5. 恢复持久化槽位（每个间隔 `startStaggerMs`，默认 3 秒）。
6. `AUTOSTART=false` 时不会新建槽位，只恢复已有的；新增槽位需要显式分配。
