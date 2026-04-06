# MiMo 网页端原始 API 文档

> 基于浏览器抓包分析，来源: https://aistudio.xiaomimimo.com/

## 认证

所有请求需要带 Cookie：

```
Cookie: serviceToken=xxx; userId=123; xiaomichatbot_ph=xxx
```

`xiaomichatbot_ph` 值在 Query 参数中也需传递：`?xiaomichatbot_ph=xxx`

## 接口列表

### 1. 对话接口（SSE 流式）

`POST /open-apis/bot/chat?xiaomichatbot_ph=<xiaomichatbot_ph值>`

**Headers:**
```
Content-Type: application/json
Accept-Language: system
x-timeZone: Asia/Shanghai
Cookie: xiaomichatbot_ph=<xiaomichatbot_ph值>
```

**Request Body:**
```json
{
  "msgId": "714bb992f4f884c901a3b2b8278506d4",
  "conversationId": "d9d95669b67e64e61d7ee65d194059ee",
  "query": "用户输入的消息文本",
  "isEditedQuery": false,
  "modelConfig": {
    "enableThinking": true,
    "webSearchStatus": "disabled",
    "model": "mimo-v2-pro-studio",
    "temperature": 0.8,
    "topP": 0.95
  },
  "multiMedias": []
}
```

**modelConfig.model 可选值：**

| 值 | 模型 |
|----|------|
| `mimo-v2-flash-studio` | MiMo-V2-Flash（高速推理轻量级） |
| `mimo-v2-pro` | MiMo-V2-Pro（性能旗舰） |
| `mimo-v2-omni` | MiMo-V2-Omni（多模态） |

**Response (SSE):**

```
id:b6df7d9272328a2cad4494b354ee2f09
event:dialogId
data:{"content":"8170797"}

id:b6df7d9272328a2cad4494b354ee2f09
event:message
data:{"type":"text","content":""}

id:b6df7d9272328a2cad4494b354ee2f09
event:message
data:{"type":"text","content":"<think>"}

id:b6df7d9272328a2cad4494b354ee2f09
event:message
data:{"type":"text","content":"First, the user"}

id:b6df7d9272328a2cad4494b354ee2f09
event:message
data:{"type":"text","content":" said"}

...

id:b6df7d9272328a2cad4494b354ee2f09
event:message
data:{"type":"text","content":"</think>Hello!"}

id:b6df7d9272328a2cad4494b354ee2f09
event:message
data:{"type":"text","content":" How can I help you"}

id:b6df7d9272328a2cad4494b354ee2f09
event:message
data:{"type":"text","content":" today?"}

id:b6df7d9272328a2cad4494b354ee2f09
event:usage
data:{"promptTokens":160,"completionTokens":279,"totalTokens":439,"nativeUsage":{"completion_tokens":279,"prompt_tokens":160,"total_tokens":439,"prompt_tokens_details":{"cached_tokens":154},"completion_tokens_details":{"reasoning_tokens":267}}}

id:b6df7d9272328a2cad4494b354ee2f09
event:finish
data:{"content":"[DONE]"}
```

**关键点：**
- `event:dialogId` — 返回对话 ID
- `event:message` — 文本流，`content` 是增量片段，需要拼接
- 思维链包裹在 `<think>…</think>` 标签中，夹在 `content` 片段里
- `event:usage` — token 用量
- `event:finish` — 结束标志，data 为 `[DONE]`

### 2. 保存会话

`POST /open-apis/chat/conversation/save?xiaomichatbot_ph=<xiaomichatbot_ph值>`

```json
{
  "conversationId": "d9d95669b67e64e61d7ee65d194059ee",
  "title": "New conversation",
  "type": "chat"
}
```

### 3. 生成标题

`POST /open-apis/chat/conversation/genTitle?xiaomichatbot_ph=<xiaomichatbot_ph值>`

```json
{
  "conversationId": "d9d95669b67e64e61d7ee65d194059ee",
  "content": "用户问题 + AI回答的前段"
}
```


### 4. 申请文件上传信息 (FDS URL)

`POST /open-apis/resource/upload/genFdsUploadInfo?xiaomichatbot_ph=<xiaomichatbot_ph值>`

**Request Body:**
```json
{
  "fileContentMd5": "md5值",
  "fileName": "image.png"
}
```

**Response:**
包含小米 FDS（对象存储）预签名 URL. 你需要拿到 `uploadUrl` 后直接 `PUT` 文件二进制流到该 URL，成功后再调用解析接口注册该文件资源才能在对话中使用。

```json
{
    "code": 0,
    "msg": "成功",
    "data": {
        "resourceId": "d0eb...",
        "resourceUrl": "https://cnbj3-fusion.fds.api.xiaomi.com/...",
        "uploadUrl": "https://cnbj3-fusion.fds.api.xiaomi.com/....",
        "objectName": "multimedia/..."
    }
}
```

### 5. 上传文件至 FDS

`PUT <从 genFdsUploadInfo 获取的预签名 url>`

**Headers:**
```
Content-Type: application/octet-stream
Content-MD5: <md5值>
```

此阶段文件直接以二进制流存入 `Body`。
成功后会得到 200 OK 响应，接下来需要调用解析接口注册该文件资源才能在对话中使用。

### 6. 解析并注册文件

上传 FDS 成功后必须调用此处向服务端绑定，才能在聊天中使用该对象。

`POST /open-apis/resource/parse?xiaomichatbot_ph=<xiaomichatbot_ph值>&fileUrl=<resourceUrl>&objectName=<objectName>&model=<model>`

**Request Body:**
```json
{}
```

**Response:**
返回 `resourceId`（对话时通过 multiMedias 传入）及该文件预计的 `tokenUsage` 数值。

```json
{
    "code": 0,
    "msg": "成功",
    "data": {
        "id": "bf0f...",
        "collectionName": "bf0f....",
        "bytes": 593360,
        "filename": "f5....",
        "tokenUsage": 593360
    }
}
```