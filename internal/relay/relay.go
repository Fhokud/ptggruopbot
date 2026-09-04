package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/go-telegram/bot"
	tgmodels "github.com/go-telegram/bot/models"
)

const routeTTL = 30 * 24 * time.Hour

type route struct {
	UserChatID      int64     `json:"user_chat_id"`
	UserMessageID   int       `json:"user_message_id,omitempty"`
	ReplyToOriginal bool      `json:"reply_to_original,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
}

type Manager struct {
	adminChatID int64
	stateFile   string
	mu          sync.Mutex
	routes      map[int]route
}

type persistedState struct {
	AdminChatID int64         `json:"admin_chat_id"`
	Routes      map[int]route `json:"routes"`
}

func New(adminChatID int64) *Manager {
	return &Manager{adminChatID: adminChatID, routes: make(map[int]route)}
}

func NewPersistent(adminChatID int64, stateFile string) (*Manager, error) {
	m := &Manager{
		adminChatID: adminChatID,
		stateFile:   stateFile,
		routes:      make(map[int]route),
	}
	if err := m.load(); err != nil {
		return nil, err
	}
	return m, nil
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

	now := time.Now()
	// Replying to the header starts a normal message. Replying to the copied
	// user message preserves Telegram's reply/quote relationship.
	m.remember(route{UserChatID: msg.Chat.ID, CreatedAt: now}, header.ID)
	m.remember(route{UserChatID: msg.Chat.ID, UserMessageID: msg.ID, ReplyToOriginal: true, CreatedAt: now}, copied.ID)
}

func (m *Manager) handleAdminReply(ctx context.Context, b *bot.Bot, msg *tgmodels.Message) {
	if msg.ReplyToMessage == nil {
		return
	}
	r, ok := m.lookup(msg.ReplyToMessage.ID)
	if !ok {
		return
	}

	params := &bot.CopyMessageParams{
		ChatID:     r.UserChatID,
		FromChatID: m.adminChatID,
		MessageID:  msg.ID,
	}
	if r.ReplyToOriginal && r.UserMessageID != 0 {
		params.ReplyParameters = &tgmodels.ReplyParameters{
			MessageID: r.UserMessageID,
		}
	}
	_, err := b.CopyMessage(ctx, params)
	if err != nil {
		log.Printf("回复用户 %d 失败: %v", r.UserChatID, err)
		_, _ = b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID: m.adminChatID,
			Text:   fmt.Sprintf("⚠️ 回复用户 %d 失败：%v", r.UserChatID, err),
		})
	}
}

func (m *Manager) remember(r route, messageIDs ...int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removeExpiredLocked(time.Now())
	for _, messageID := range messageIDs {
		m.routes[messageID] = r
	}
	if err := m.saveLocked(); err != nil {
		log.Printf("持久化私聊消息路由失败: %v", err)
	}
}

func (m *Manager) lookup(messageID int) (route, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.routes[messageID]
	if ok && time.Since(r.CreatedAt) > routeTTL {
		delete(m.routes, messageID)
		if err := m.saveLocked(); err != nil {
			log.Printf("持久化私聊消息路由清理结果失败: %v", err)
		}
		return route{}, false
	}
	return r, ok
}

func (m *Manager) load() error {
	if m.stateFile == "" {
		return nil
	}
	data, err := os.ReadFile(m.stateFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("读取路由状态文件 %q: %w", m.stateFile, err)
	}
	if len(data) != 0 {
		var state persistedState
		if err := json.Unmarshal(data, &state); err != nil {
			return fmt.Errorf("解析路由状态文件 %q: %w", m.stateFile, err)
		}
		// Message IDs are scoped to the administrator chat. Never reuse routes
		// created for another administrator, even when the same file is used.
		if state.AdminChatID == m.adminChatID {
			m.routes = state.Routes
		}
	}
	if m.routes == nil {
		m.routes = make(map[int]route)
	}
	if m.removeExpiredLocked(time.Now()) {
		return m.saveLocked()
	}
	return nil
}

func (m *Manager) removeExpiredLocked(now time.Time) bool {
	changed := false
	for id, old := range m.routes {
		if now.Sub(old.CreatedAt) > routeTTL {
			delete(m.routes, id)
			changed = true
		}
	}
	return changed
}

func (m *Manager) saveLocked() error {
	if m.stateFile == "" {
		return nil
	}
	dir := filepath.Dir(m.stateFile)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(persistedState{AdminChatID: m.adminChatID, Routes: m.routes})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".relay-routes-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, m.stateFile); err != nil {
		// Windows cannot replace an existing file with os.Rename. Linux takes
		// the atomic path above; this fallback keeps local development working.
		if removeErr := os.Remove(m.stateFile); removeErr != nil && !os.IsNotExist(removeErr) {
			return err
		}
		if renameErr := os.Rename(tmpName, m.stateFile); renameErr != nil {
			return renameErr
		}
	}
	return nil
}
