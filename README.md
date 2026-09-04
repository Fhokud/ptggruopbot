# tg_Verify_Bot

Telegram 群管理机器人，支持：

- 自动审批入群申请
- 新用户验证投票机制
- 拒绝批量转发消息
- 敏感关键词检测与删除
- 重复消息检测（默认 48 小时内同一用户连续相同归一化内容批量删除）
- 私聊双向消息中继（用户私聊 Bot，管理员回复后回传用户）

## 目录结构

- `cmd/bot/` - 主入口程序
- `internal/handlers/` - Telegram 更新处理逻辑
- `internal/telegram/` - Telegram API 与 webhook 封装
- `internal/keywords/` - 关键词加载与 Aho-Corasick 匹配
- `internal/poll/` - 验证投票状态管理
- `internal/utils/` - 通用工具函数
- `internal/models/` - 全局状态数据结构
- `keywords/` - 关键词文本文件目录

## 依赖

- Go 1.25+
- `github.com/go-telegram/bot`
- `github.com/cloudflare/ahocorasick`
- `github.com/fsnotify/fsnotify`

## 环境变量

在运行前，请设置以下环境变量：

- `TELEGRAM_BOT_TOKEN` - Telegram bot token
- `TELEGRAM_WEBHOOK_URL` - 公开可访问的 webhook URL，例如 `https://example.com/webhook`
- `TELEGRAM_WEBHOOK_SECRET` - 可选的 webhook secret token
- `TELEGRAM_WEBHOOK_LISTEN` - 可选监听地址，默认 `127.0.0.1:898`
- `TELEGRAM_WEBHOOK_PATH` - 可选 webhook 路径，默认 `/webhook`
- `DUPLICATE_MESSAGE_WINDOW` - 可选重复消息匹配时段，默认 `48h`；支持 Go duration（如 `30m`、`72h`），也支持纯数字按小时处理
- `TELEGRAM_ADMIN_CHAT_ID` - 管理员的 Telegram 数字用户 ID；设置后启用私聊双向中继
- `TELEGRAM_RELAY_STATE_FILE` - 可选的私聊路由状态文件；未设置或留空时，默认为程序运行目录下的 `relay_routes.json`

## 私聊消息中继

设置 `TELEGRAM_ADMIN_CHAT_ID` 后，用户发给 Bot 的私聊消息会默认复制给该管理员。管理员回复用户信息标题时，Bot 会向对应用户发送普通新消息；管理员回复复制过来的用户原消息时，Bot 会引用该原消息进行回复。复制方式不会暴露管理员账号，也适用于文字、图片、文件和语音等常见消息。

路由关系保留 30 天，并持久化到 `TELEGRAM_RELAY_STATE_FILE` 指定的 JSON 文件；Bot 重启后仍可回复之前收到的消息。请确保运行用户对状态文件所在目录有写权限。

## 构建与运行

```bash
cd d:/Code/tg_Verify_Bot
go build ./cmd/bot
./bot
```

如果想直接指定模块路径：

```bash
go build -o mybot ./cmd/bot
./mybot
```

## 关键词文件

程序会读取根目录下的 `keywords/` 文件夹，支持多个 `.txt` 文件，文件内容按行拆分关键词。

- 每个非空行视为一个关键词
- 修改后会自动热加载

## systemd 示例

```ini
[Unit]
Description=Telegram Verification Bot
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=/opt/mybot
ExecStart=/opt/mybot/mybot
Environment="TELEGRAM_BOT_TOKEN=TOKEN"
Environment="TELEGRAM_WEBHOOK_URL=https://push.exp.com/webhook"
Environment="TELEGRAM_WEBHOOK_SECRET=rand"
Environment="TELEGRAM_ADMIN_CHAT_ID=123456789"
Environment="TELEGRAM_RELAY_STATE_FILE=/var/lib/ptggruopbot/relay_routes.json"
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

## 运行验证

启动后请确保 webhook 服务可访问，并检查日志输出：

- `Bot webhook 已启动，监听 http://...`
- `✅ webhook 已设置`

## 注意

- 程序会自动创建 `keywords/` 文件夹（如果不存在）
- 关键词匹配使用 Aho-Corasick 算法，支持高效批量匹配
- 重复消息判断窗口默认 `48h`，同一用户上一条消息与当前消息的归一化内容相同会一起删除
