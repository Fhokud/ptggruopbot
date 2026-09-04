package relay

import (
	"context"
	"fmt"
	"html"
	"log"
	"sync"
	"time"

	"github.com/go-telegram/bot"
	tgmodels "github.com/go-telegram/bot/models"
)

const routeTTL = 30 * 24 * time.Hour

type route struct {
	userChatID    int64
	userMessageID int
	createdAt     time.Time
}

type Manager struct {
	adminChatID int64
	mu          sync.Mutex
	routes      map[int]route
}

func New(adminChatID int64) *Manager {
	return &Manager{adminChatID: adminChatID, routes: make(map[int]route)}
}

func (m *Manager) Enabled() bool {
	return m != nil && m.adminChatID != 0
}

// HandlePrivate handles private-chat messages. It returns true when the message
// belongs to the relay flow (including admin messages that are not replies).
func (m *Manager) HandlePrivate(ctx context.Context, b *bot.Bot, msg *tgmodels.Message) bool {
	if !m.Enabled() || msg == nil || msg.Chat.Type != tgmodels.ChatTypePrivate {
		return false
	}

	if msg.Chat.ID == m.adminChatID {
		m.handleAdminReply(ctx, b, msg)
		return true
	}

	m.forwardToAdmin(ctx, b, msg)
	return true
}

func (m *Manager) forwardToAdmin(ctx context.Context, b *bot.Bot, msg *tgmodels.Message) {
	name := "未知用户"
	username := ""
	if msg.From != nil {
		name = msg.From.FirstName
		if msg.From.LastName != "" {
			name += " " + msg.From.LastName
		}
		if msg.From.Username != "" {
			username = " (@" + msg.From.Username + ")"
		}
	}

	header, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:    m.adminChatID,
		Text:      fmt.Sprintf("📨 来自 <a href=\"tg://user?id=%d\">%s</a>%s（<code>%d</code>）\n回复下方消息即可回信。", msg.Chat.ID, html.EscapeString(name), html.EscapeString(username), msg.Chat.ID),
		ParseMode: tgmodels.ParseModeHTML,
	})
	if err != nil {
		log.Printf("转发用户 %d 的消息标题失败: %v", msg.Chat.ID, err)
		return
	}

	copied, err := b.CopyMessage(ctx, &bot.CopyMessageParams{
		ChatID:     m.adminChatID,
		FromChatID: msg.Chat.ID,
		MessageID:  msg.ID,
		ReplyParameters: &tgmodels.ReplyParameters{
			MessageID: header.ID,
		},
	})
	if err != nil {
		log.Printf("复制用户 %d 的消息失败: %v", msg.Chat.ID, err)
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: m.adminChatID,
			Text:   fmt.Sprintf("⚠️ 无法复制该用户的消息：%v", err),
		})
		return
	}

	r := route{userChatID: msg.Chat.ID, userMessageID: msg.ID, createdAt: time.Now()}
	m.remember(header.ID, r)
	m.remember(copied.ID, r)
}

func (m *Manager) handleAdminReply(ctx context.Context, b *bot.Bot, msg *tgmodels.Message) {
	if msg.ReplyToMessage == nil {
		return
	}
	r, ok := m.lookup(msg.ReplyToMessage.ID)
	if !ok {
		return
	}

	_, err := b.CopyMessage(ctx, &bot.CopyMessageParams{
		ChatID:     r.userChatID,
		FromChatID: m.adminChatID,
		MessageID:  msg.ID,
		ReplyParameters: &tgmodels.ReplyParameters{
			MessageID: r.userMessageID,
		},
	})
	if err != nil {
		log.Printf("回复用户 %d 失败: %v", r.userChatID, err)
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: m.adminChatID,
			Text:   fmt.Sprintf("⚠️ 回复用户 %d 失败：%v", r.userChatID, err),
		})
	}
}

func (m *Manager) remember(messageID int, r route) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	for id, old := range m.routes {
		if now.Sub(old.createdAt) > routeTTL {
			delete(m.routes, id)
		}
	}
	m.routes[messageID] = r
}

func (m *Manager) lookup(messageID int) (route, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.routes[messageID]
	if ok && time.Since(r.createdAt) > routeTTL {
		delete(m.routes, messageID)
		return route{}, false
	}
	return r, ok
}
