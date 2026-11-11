# 订阅

订阅是公网最省事的接入方式：客户端通过 HTTPS 拉一份配置，节点列表里每个槽位对应一条 Trojan-over-WS 链接。

```mermaid
sankey-beta
请求,格式识别,100
格式识别,节点筛选,100
节点筛选,国家过滤,100
国家过滤,排序与截断,88
排序与截断,格式渲染,88
格式渲染,客户端,88
```

## URL 形状

```
https://<publicHost>/sub/<token>[/<format>][?参数]
```

- `token`：主订阅 token，或 `subscriptionClients` 里某个客户端的 token。
- `format`：省略时按 `User-Agent` 自动判断。
- 协议固定为 Trojan over WebSocket，TLS 由入口（Instabox / Nginx / CDN）终止。

```mermaid
flowchart TD
  A[/sub/token/format/] --> B{format 显式?}
  B -- 是 --> Z[按格式渲染]
  B -- 否 --> C[读 User-Agent]
  C --> D{含 sing-box?}
  D -- 是 --> S[singbox]
  D -- 否 --> E{含 surge?}
  E -- 是 --> SU[surge]
  E -- 否 --> F{含 quantumult?}
  F -- 是 --> Q[qx]
  F -- 否 --> G{含 clash/mihomo/stash/flclash?}
  G -- 是 --> CL[clash]
  G -- 否 --> V[v2ray]
```

## 格式

| format | Content-Type | 内容 |
| --- | --- | --- |
| `v2ray` / `v2rayn` / `txt` / `base64` | text/plain | base64 的 trojan 链接列表 |
| `clash` / `mihomo` / `yaml` | text/yaml | Clash/Mihomo 配置，含 `ProxyGate`、`Auto`、`PG-Fallback`、`PG-XX` 分组 |
| `singbox` / `sing-box` / `sb` | application/json | sing-box 配置，`mixed` 入站 `127.0.0.1:2080` |
| `surge` | text/plain | Surge `[Proxy]` / `[Proxy Group]` / `[Rule]` |
| `qx` / `quantumultx` | text/plain | Quantumult X server + policy |
| `socks` / `socks5` | text/plain | `socks5://user:pass@host:port` 列表 |
| `socks5h` | text/plain | 同上，scheme 为 `socks5h` |
| `socksb64` / `socks-v2ray` | text/plain | 上面列表的 base64 |
| `sockscsv` | text/csv | `host,port,username,password,country,slot,name` |

单条 Trojan 链接长这样：

```
trojan://PASSWORD@sub.example.com:443?type=ws&host=sub.example.com&path=%2Fpgtrojan&security=tls&sni=sub.example.com&fp=chrome#pg-07-KR-KT
```

## 参数

| 参数 | 示例 | 说明 |
| --- | --- | --- |
| `count` | `12` | 最多返回多少个节点；`0` 表示不截断 |
| `countries` | `JP,KR` | 白名单，严格过滤，没有就返回空，不会回退 |
| `country` | `JP` | `countries` 的单值别名 |
| `exclude` | `US,RU` | 排除这些国家 |
| `slots` | `3,7,11-15` | 只取指定槽位，支持区间 |
| `shuffle` | `true` | 截断前打乱，避免每次都拿到同几个 |
| `group` | `country` | `country`（默认）/ `auto` / `fallback` / `none` |
| `sort` | `quality` | `slot` / `country` / `name` / `stable` / `quality` |
| `preset` | `stable` | `stable` 或 `quality`；未显式给 `count` 时自动 `count=20` |

### 严格国家过滤

```bash
# 只要 JP 和 KR，最多 12 个，按质量排序
/sub/TOKEN/v2ray?countries=JP,KR&count=12&sort=quality

# 只要 JP；没有就返回 503，不会拿别的国家凑数
/sub/TOKEN/v2ray?countries=JP
```

`countries` 的过滤发生在候选选择阶段，不在渲染阶段，所以分组数量、`count`、`shuffle` 都只作用于过滤后的集合。

### 预设

```mermaid
flowchart LR
  A[全部 ready 节点] --> B{countries / exclude}
  B --> C{slots}
  C --> D{preset}
  D -- stable --> E[失败少 → 成功多 → 延迟低]
  D -- quality --> F[质量分高 → 延迟低 → 带宽高]
  D -- 无 --> G[按槽位号]
  E --> H[shuffle?]
  F --> H
  G --> H
  H --> I[count 截断]
  I --> J[渲染]
```

- `stable`：`failCount` 升序 → `successCount` 降序 → `pingMs` 升序。
- `quality`：`qualityScore` 降序 → `pingMs` 升序 → `speedBps` 降序。
- 两者都会把最近失败的节点排到后面，但不会剔除；`healthy` 和 `state=ready` 是硬条件。

## 分组

| group | Clash / sing-box 行为 |
| --- | --- |
| `country`（默认） | `ProxyGate` 里放 `Auto`、`PG-Fallback` 和每个国家分组 |
| `auto` | `ProxyGate` 只放 `Auto` 和 `PG-Fallback` |
| `fallback` | 同上，但顺序反过来 |
| `none` | 只有单个节点，不建自动分组 |

`Auto` 是 `url-test`（`generate_204`，300s，容差 50ms），`PG-Fallback` 是 `fallback`。

## 客户端预设

给每个指纹实例一个独立 token，可以固定国家、数量和分组，客户端不用记参数：

```bash
curl -s -b cookie.txt -X PUT -H 'Content-Type: application/json' \
  -d '{"clients":[
    {"name":"profile-jp","token":"tok-jp-12345678","count":6,"countries":["JP"],"preset":"quality","group":"none"},
    {"name":"profile-kr","token":"tok-kr-12345678","count":6,"countries":["KR","TH"],"shuffle":true}
  ]}' \
  http://HOST:8080/api/subscription/clients
```

之后直接访问：

```
/sub/tok-jp-12345678          # 按 UA 自动选格式
/sub/tok-jp-12345678/clash
```

删除某个 client 后，它的 token 立即失效；已建立的连接不会立刻断，但下一次握手会失败。

## 响应头

| 头 | 含义 |
| --- | --- |
| `X-ProxyGate-Nodes` | 本次返回的节点数 |
| `X-ProxyGate-Format` | 实际使用的格式 |
| `X-ProxyGate-Preset` | 生效的 preset |
| `X-ProxyGate-Slots` | 显式 `slots` 过滤时的数量 |
| `Profile-Update-Interval: 6` | 建议客户端 6 小时刷新 |
| `Retry-After: 60` | 空结果时建议 60 秒后重试 |
| `Cache-Control: no-store` | 不缓存 |

## 空结果与缓存

- 没有任何 `ready + healthy + exitIp` 的槽位时，服务端会返回**上一次非空节点集**（如果存在）。
- 缓存里的密码由 token + 槽位号推导，重建槽位后仍然有效。
- 过滤后为空且无缓存：`503`，正文 `no ready slots match the current filter`。
- 完整池为空：`503`，正文 `no ready slots yet`。

## 分享上限

`matrix.shareLimit` 会同时作用于订阅、分享页和本地接入包：

- `0`：导出全部就绪节点。
- `N`：最多导出 N 个，按「就绪优先、槽位号升序」截断。

```bash
# 改完立即生效，不重启
curl -s -b cookie.txt http://HOST:8080/api/config > cfg.json
jq '.matrix.shareLimit = 12' cfg.json > cfg2.json
curl -s -b cookie.txt -X PUT -H 'Content-Type: application/json' \
  -d "{\"config\":$(cat cfg2.json),\"newPassword\":\"\"}" \
  http://HOST:8080/api/config
```
