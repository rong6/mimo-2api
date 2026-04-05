# MiMo 2API

把 [Xiaomi MiMo Studio](https://aistudio.xiaomimimo.com/) 网页端对话 API 转为标准 OpenAI Chat Completions 格式，支持多账号负载均衡。

## 免责声明
- 本项目仅供学习和研究使用，请勿用于商业用途。
- 使用前请确保你已阅读并同意小米的服务条款。
- 本项目不提供任何形式的保证或支持，使用风险自负。

## 编译

```bash
cd mimo-2api
go build -o mimo-2api .
```

## Docker 部署

直接使用 Docker：

```bash
# 构建镜像
docker build -t mimo-2api .

# 运行容器
docker run -d --name mimo-2api -p 8090:8090 \
  mimo-2api \
  -cookie 'serviceToken=xxx; userId=6861418446; xiaomichatbot_ph=xxx' \
  -apikey 'your-key' \
  -save
```

或者使用 Docker Compose：

1. 编辑项目下的 `docker-compose.yml`，填入你自己的 cookie 及相关参数。
2. 启动服务：

```bash
docker-compose up -d
```

## 获取 Cookie

1. 浏览器打开 https://aistudio.xiaomimimo.com/ 并登录小米账号
2. F12 → Network → 随便发一条消息 → 点击 `/open-apis/bot/chat` 请求
3. 在 Request Headers 里找到 `Cookie:` 那行，复制**整行的值**

## 运行

```bash
# 单账号
./mimo-2api -cookie 'serviceToken=xxx; userId=123; xiaomichatbot_ph=xxx'

# 启用 APIKey 和保存历史会话
./mimo-2api -cookie '...' -apikey 'your-key' -save

# 多账号负载均衡（每次请求随机选一个）
./mimo-2api \
  -cookie  'serviceToken=aaa; userId=111; xiaomichatbot_ph=xxx' \
  -cookie2 'serviceToken=bbb; userId=222; xiaomichatbot_ph=yyy' \
  -cookie3 'serviceToken=ccc; userId=333; xiaomichatbot_ph=zzz'
```

### 参数

| 参数 | 说明 |
|------|------|
| `-listen :8090` | 监听地址 |
| `-apikey` | API Key 认证（可选） |
| `-save` | 保存当前对话到网页端的历史记录（可选） |
| `-cookie` | 第1个账号 Cookie（必需） |
| `-cookie2` | 第2个账号 Cookie（可选） |
| `-cookie3` | 第3个账号 Cookie（可选） |
| `-cookie4` | 第4个账号 Cookie（可选） |
| `-cookie5` | 第5个账号 Cookie（可选） |

## API

### `POST /v1/chat/completions`

标准 OpenAI 格式，支持 `stream`。如果启用了 `-apikey`，请带上 `Authorization` header。

```bash
curl -X POST http://localhost:8090/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer your-key" \
  -d '{"model":"mimo-v2-flash","messages":[{"role":"user","content":"你好"}],"stream":true}'
```

### `GET /v1/models`

### `GET /health`

## 模型

| ID | 说明 |
|------|------|
| mimo-v2-flash | 极速推理轻量级大模型 |
| mimo-v2-pro | 开源性能旗舰模型 |
| mimo-v2-omni | 极速Flash版的多模态模型 |

## 多账号说明

- 每次请求（每次 `/v1/chat/completions` 调用）随机选取一个 cookie
- 适合多个小米账号分担请求量
- 思维链通过 `reasoning_content` 字段返回（流式）

## LICENSE
[MIT License](LICENSE)