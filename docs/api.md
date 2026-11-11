# HTTP API

面板和程序共用同一个 8080 监听，但认证方式不同：

```mermaid
flowchart TD
  A[请求] --> B{路径以 /api/v1/ 开头?}
  B -- 是 --> C{Authorization: Bearer API_KEY}
  C -- 匹配 --> OK[进入池接口]
  C -- 不匹配 --> E401[401 invalid API key]
  B -- 否 --> D{有面板会话 Cookie?}
  D -- 是 --> OK2[进入会话接口]
  D -- 否 --> E401b[401 unauthorized]
  A --> V[GET /api/version 公开]
  A --> L[POST /api/login 公开]
```

- 程序接口：`Authorization: Bearer <apiKey>`，apiKey 在 `config.json` 的 `matrix.apiKey`。
- 面板接口：先 `POST /api/login` 拿 Cookie，之后的请求带 Cookie。
- 所有错误响应：

```json
{ "error": "slot range is outside the configured pool" }
```

---

## 登录

```mermaid
sequenceDiagram
    participant C as curl / 浏览器
    participant S as ProxyGate
    C->>S: POST /api/login {"username","password"}
    S-->>C: 200 + Set-Cookie: session=...
    C->>S: GET /api/matrix/slots (Cookie)
    S-->>C: 200 槽位列表
```

```bash
curl -s -c cookie.txt -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"YOUR_PASSWORD"}' \
  http://HOST:8080/api/login
```

---

## 池接口（API key）

| 方法 | 路径 | 作用 |
| --- | --- | --- |
| GET | `/api/v1/pool/slots` | 列出全部槽位和 SOCKS5 URL |
| POST | `/api/v1/pool/allocate` | 按国家/模式分配一个槽位 |
| POST | `/api/v1/pool/rotate/{slot_id}` | 轮换指定槽位 |
| GET | `/api/v1/pool/nodes/stats` | 节点库统计 |

### GET /api/v1/pool/slots

```bash
curl -s -H "Authorization: Bearer $API_KEY" \
  http://HOST:8080/api/v1/pool/slots
```

```json
{
  "enabled": true,
  "maxSlots": 32,
  "available": 8,
  "slots": [
    {
      "slotId": 7,
      "username": "pg07xxxx",
      "port": 10807,
      "mode": "sticky",
      "protocol": "tcp",
      "country": "Japan",
      "countryShort": "JP",
      "state": "ready",
      "exitIp": "203.0.113.7",
      "asn": "AS2516",
      "asnOrg": "KDDI CORPORATION",
      "qualityScore": 72.5,
      "healthy": true,
      "socks5": "socks5://pg07xxxx:PASSWORD@127.0.0.1:10807"
    }
  ]
}
```

`socks5` 的 host 取 `matrix.publicSocksHost`；未配置时是 `127.0.0.1`，只适合本机使用。

### POST /api/v1/pool/allocate

请求体（全部字段可选）：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `country` | string | 两位国家代码，如 `JP`；空表示不限 |
| `protocol` | string | `tcp` / `udp` / 空；空按节点支持情况选 |
| `mode` | string | `sticky`（默认）/ `rotating` |
| `slot_id` | int | 指定槽位；0 表示让调度器挑 |

```bash
curl -s -X POST -H "Authorization: Bearer $API_KEY" \
  -H 'Content-Type: application/json' \
  -d '{"country":"JP","mode":"sticky"}' \
  http://HOST:8080/api/v1/pool/allocate
```

返回 `201` 表示新建槽位，`200` 表示复用已有槽位：

```json
{ "created": true, "slot": { "slotId": 7, "state": "switching", "...": "..." } }
```

分配是异步的：返回时槽位可能还在 `switching`，等健康检查通过后变 `ready` 并带 `exitIp`。

### POST /api/v1/pool/rotate/{slot_id}

```bash
curl -s -X POST -H "Authorization: Bearer $API_KEY" \
  http://HOST:8080/api/v1/pool/rotate/7
```

- `202`：已接受，槽位进入 `switching`。
- `404`：槽位不存在。
- `409`：槽位正忙（正在切换）。

---

## 会话接口（面板 Cookie）

| 方法 | 路径 | 作用 |
| --- | --- | --- |
| GET | `/api/status` | 面板状态摘要 |
| GET | `/api/config` | 读取完整配置（敏感字段会被抹掉） |
| PUT | `/api/config` | 保存配置，`{"config":{...},"newPassword":""}` |
| GET | `/api/matrix/slots` | 槽位列表 + 国家统计 |
| POST | `/api/matrix/slots/country` | 批量给连续槽位分配国家 |
| POST | `/api/matrix/slots/{slot_id}/configure` | 配置单个槽位 |
| POST | `/api/matrix/slots/{slot_id}/rotate` | 轮换单个槽位 |
| GET | `/api/matrix/dialstats` | 拨号闸门计数与参数 |
| GET/PUT | `/api/matrix/front-proxy` | 前置代理设置 |
| GET | `/api/share/pool` | 分享页节点池（含 SOCKS/V2Ray 链接） |
| GET | `/api/local/plan` | 本地接入端口计划 |
| GET | `/api/local/shardx` | 纯文本 SOCKS5 列表 |
| GET | `/api/local/singbox` | sing-box 配置 JSON |
| GET | `/api/local/bundle` | zip：配置 + 脚本 + 说明 |
| GET | `/api/subscription` | 订阅总览与客户端 URL |
| PUT | `/api/subscription/clients` | 替换客户端 token 列表 |
| GET | `/api/nodes` | 节点库列表 |
| GET | `/api/services` | 上游服务状态 |
| POST | `/api/refresh` | 触发节点池刷新 |
| POST | `/api/restart` | 重启服务（容器内） |

### GET /api/matrix/slots

```json
{
  "enabled": true,
  "maxSlots": 32,
  "available": 6,
  "ready": 26,
  "healthy": 24,
  "slots": [{ "slotId": 1, "countryShort": "JP", "state": "ready", "healthy": true }],
  "countries": [{ "code": "JP", "total": 11 }, { "code": "KR", "total": 6 }]
}
```

`countries` 是**节点库供给**统计，不是槽位分配统计。国家供给少的时候，槽位会反复进入 `error`。

### POST /api/matrix/slots/country

批量分配，适合「把 21–32 号槽位全给 JP/KR/TH」这种操作。

```bash
curl -s -b cookie.txt -H 'Content-Type: application/json' \
  -d '{"country":"JP","from":21,"count":4,"mode":"sticky"}' \
  http://HOST:8080/api/matrix/slots/country
```

```json
{ "updated": 4, "failed": 0, "failures": [], "country": "JP", "from": 21, "count": 4 }
```

`from + count - 1` 不能超过 `matrix.slots`。部分失败时仍返回 `200`，看 `failed` 和 `failures`。

### POST /api/matrix/slots/{slot_id}/configure

请求体同 `AllocateOptions`，`slot_id` 以 URL 为准：

```bash
curl -s -b cookie.txt -H 'Content-Type: application/json' \
  -d '{"country":"KR","mode":"sticky"}' \
  http://HOST:8080/api/matrix/slots/9/configure
```

### GET /api/matrix/dialstats

```json
{
  "allowed": 51,
  "blocked": 0,
  "skipped": 12,
  "cooling": 24,
  "portsThisMinute": 3,
  "targetsThisHour": 51,
  "paused": false,
  "dialRate": 1,
  "dialBurst": 3,
  "dialMinMs": 900,
  "dialMaxMs": 1500,
  "portIpPerMinute": 10,
  "globalIpPerHour": 150,
  "failCooldownMinutes": 360,
  "blockedPorts": [999, 6666, 31337]
}
```

`targetsThisHour` 接近 `globalIpPerHour` 时，新槽位会长时间停在 `switching`。

### GET /api/local/plan

```bash
curl -s -b cookie.txt 'http://HOST:8080/api/local/plan?basePort=12001'
```

```json
{
  "basePort": 12001,
  "host": "sub.example.com",
  "path": "/pgtrojan",
  "missingHost": false,
  "total": 2,
  "nodes": [
    { "slotId": 1, "name": "pg-01-JP-KDDI", "country": "JP", "socks5Port": 12001, "socks5": "socks5://127.0.0.1:12001" },
    { "slotId": 7, "name": "pg-07-KR-KT", "country": "KR", "socks5Port": 12007, "socks5": "socks5://127.0.0.1:12007" }
  ]
}
```

未知 `basePort` 会被夹到 `1024..65000`；非法值回退 `12001`。

### PUT /api/config

```bash
curl -s -b cookie.txt http://HOST:8080/api/config > cfg.json
# 修改 matrix.shareLimit 等字段后：
curl -s -b cookie.txt -X PUT -H 'Content-Type: application/json' \
  -d "{\"config\":$(cat cfg.json),\"newPassword\":\"\"}" \
  http://HOST:8080/api/config
```

```json
{ "ok": true, "restartRequired": false }
```

以下字段改动会返回 `restartRequired: true`：

- `databasePath`
- `socks5.listenAddress`
- `web.listenAddress`
- `matrix.listenAddress`
- `matrix.portStart` / `matrix.slots`（实际由启动参数和端口映射决定）

`GET /api/config` 会抹掉 `password`、`apiKey`、`subscriptionToken`，PUT 时服务端用旧值补回，所以直接回写是安全的。

### PUT /api/subscription/clients

```bash
curl -s -b cookie.txt -X PUT -H 'Content-Type: application/json' \
  -d '{"clients":[
        {"name":"fingerprint-a","token":"tok-a-12345678","count":8,"countries":["JP"],"group":"none"},
        {"name":"fingerprint-b","token":"tok-b-12345678","count":8,"countries":["KR","TH"],"preset":"quality"}
      ]}' \
  http://HOST:8080/api/subscription/clients
```

注意：`decodeJSON` 使用 `DisallowUnknownFields`，请求体里出现未定义字段会直接 400。

---

## 错误码

| 状态 | 含义 |
| --- | --- |
| 400 | JSON 非法、字段越界、槽位范围超出 |
| 401 | API key / 会话无效 |
| 404 | 槽位不存在、订阅格式不支持 |
| 409 | 槽位正忙 |
| 503 | 矩阵未启用、无可用节点、公共主机未配置 |
| 500 | 内部错误（看容器日志） |

---

## 最小自动化示例

```bash
BASE=http://HOST:8080
KEY=$PROXYGATE_API_KEY

# 1. 申请一个日本槽位
SLOT=$(curl -s -X POST "$BASE/api/v1/pool/allocate" \
  -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' \
  -d '{"country":"JP"}' | jq -r '.slot.slotId')

# 2. 等它就绪
until curl -s "$BASE/api/v1/pool/slots" -H "Authorization: Bearer $KEY" \
  | jq -e ".slots[] | select(.slotId==$SLOT and .state==\"ready\" and .healthy)" >/dev/null; do
  sleep 5
done

# 3. 用出口验证国家
curl -s "$BASE/api/v1/pool/slots" -H "Authorization: Bearer $KEY" \
  | jq -r ".slots[] | select(.slotId==$SLOT) | .socks5"
```
