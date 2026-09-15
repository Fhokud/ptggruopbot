package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	appmodels "github.com/Fhokud/tg_Verify_Bot/internal/models"
	"github.com/Fhokud/tg_Verify_Bot/internal/telegram"
	"github.com/go-telegram/bot"
	tgmodels "github.com/go-telegram/bot/models"
)

type whitelistEntry struct {
	ChatID int64 `json:"chat_id"`
	UserID int64 `json:"user_id"`
}

type whitelistStore struct {
	mu      sync.RWMutex
	admins  map[int64]bool
	entries map[whitelistEntry]bool
	path    string
}

var groupWhitelist = &whitelistStore{}

func ConfigureAdminCommands(ctx context.Context, b *bot.Bot) error {
	ok, err := b.SetMyCommands(ctx, &bot.SetMyCommandsParams{
		Scope: &tgmodels.BotCommandScopeAllChatAdministrators{},
		Commands: []tgmodels.BotCommand{
			{Command: "help", Description: "查看管理员命令菜单"},
			{Command: "whitelist_add", Description: "添加本群白名单（回复消息或填写用户 ID）"},
			{Command: "whitelist_remove", Description: "移除本群白名单（回复消息或填写用户 ID）"},
		},
	})
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("Telegram 未确认命令菜单设置")
	}
	return nil
}

func isGroupMessageExempt(ctx context.Context, b *bot.Bot, msg *tgmodels.Message) bool {
	if msg.Chat.Type != tgmodels.ChatTypeGroup && msg.Chat.Type != tgmodels.ChatTypeSupergroup {
		return false
	}
	// Anonymous administrators speak on behalf of this group. Other sender
	// chats (including linked-channel automatic forwards) are not exempt.
	if msg.SenderChat != nil {
		return msg.SenderChat.ID == msg.Chat.ID
	}
	if msg.From == nil {
		return false
	}
	if groupWhitelist.contains(msg.Chat.ID, msg.From.ID) {
		return true
	}
	member, err := b.GetChatMember(ctx, &bot.GetChatMemberParams{ChatID: msg.Chat.ID, UserID: msg.From.ID})
	if err != nil {
		log.Printf("查询群 %d 用户 %d 管理员身份失败: %v", msg.Chat.ID, msg.From.ID, err)
		return false
	}
	return member != nil && (member.Type == tgmodels.ChatMemberTypeOwner || member.Type == tgmodels.ChatMemberTypeAdministrator)
}

// ConfigureWhitelist must be called before processing updates.
func ConfigureWhitelist(adminIDs, stateFile string) error {
	s := &whitelistStore{admins: make(map[int64]bool), entries: make(map[whitelistEntry]bool), path: stateFile}
	if strings.TrimSpace(adminIDs) != "" {
		for _, raw := range strings.Split(adminIDs, ",") {
			id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
			if err != nil || id <= 0 {
				return fmt.Errorf("TELEGRAM_WHITELIST_ADMIN_IDS 必须是逗号分隔的正整数用户 ID")
			}
			s.admins[id] = true
		}
	}
	data, err := os.ReadFile(stateFile)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err == nil {
		var entries []whitelistEntry
		if err := json.Unmarshal(data, &entries); err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.ChatID >= 0 || entry.UserID <= 0 {
				return fmt.Errorf("白名单文件包含无效的群或用户 ID")
			}
			s.entries[entry] = true
		}
	}
	groupWhitelist = s
	return nil
}

func (s *whitelistStore) contains(chatID, userID int64) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.entries[whitelistEntry{chatID, userID}]
}

func (s *whitelistStore) set(chatID, userID int64, add bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := whitelistEntry{chatID, userID}
	entries := make([]whitelistEntry, 0, len(s.entries)+1)
	for entry := range s.entries {
		if entry != key {
			entries = append(entries, entry)
		}
	}
	if add {
		entries = append(entries, key)
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".whitelist-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), s.path); err != nil {
		return err
	}
	if add {
		s.entries[key] = true
	} else {
		delete(s.entries, key)
	}
	appmodels.LastUserMessages.Delete(appmodels.LastUserMessageKey{ChatID: chatID, UserID: userID})
	return nil
}

func handleWhitelistCommand(ctx context.Context, b *bot.Bot, msg *tgmodels.Message) bool {
	parts := strings.Fields(msg.Text)
	if len(parts) == 0 {
		return false
	}
	command, targetBot, addressed := strings.Cut(parts[0], "@")
	mention := false
	for _, part := range parts {
		if strings.HasPrefix(part, "@") {
			me, err := b.GetMe(ctx)
			if err == nil && me != nil && strings.EqualFold(part, "@"+me.Username) {
				mention = true
				break
			}
		}
	}
	if command != "/whitelist_add" && command != "/whitelist_remove" && command != "/help" && !mention {
		return false
	}
	if addressed {
		me, err := b.GetMe(ctx)
		if err != nil || me == nil || !strings.EqualFold(targetBot, me.Username) {
			return true
		}
	}
	reply := func(text string) *tgmodels.Message {
		sent, err := b.SendMessage(ctx, &bot.SendMessageParams{ChatID: msg.Chat.ID, Text: text})
		if err != nil {
			log.Printf("发送白名单操作结果失败: %v", err)
			return nil
		}
		return sent
	}
	if msg.From == nil || msg.SenderChat != nil {
		reply("仅有权限的群管理员可以管理白名单，请使用个人身份发送命令。")
		return true
	}
	member, err := b.GetChatMember(ctx, &bot.GetChatMemberParams{ChatID: msg.Chat.ID, UserID: msg.From.ID})
	if err != nil || member == nil || (member.Type != tgmodels.ChatMemberTypeOwner && member.Type != tgmodels.ChatMemberTypeAdministrator) {
		reply("无法确认你的群管理员权限，操作未执行。")
		return true
	}
	if mention || command == "/help" {
		menu := reply("管理员命令菜单：\n/whitelist_add 用户ID — 添加本群白名单\n/whitelist_remove 用户ID — 移除本群白名单\n也可回复成员消息发送上述命令，无需填写 ID。\n/help — 查看此菜单\n群管理员默认豁免消息检查。")
		if menu != nil {
			chatID, messageID := msg.Chat.ID, menu.ID
			time.AfterFunc(30*time.Second, func() {
				if err := telegram.DeleteMessageWithRetry(ctx, b, chatID, messageID); err != nil {
					log.Printf("删除群 %d 管理员命令菜单失败: %v", chatID, err)
				}
			})
		}
		return true
	}
	var userID int64
	if len(groupWhitelist.admins) > 0 && !groupWhitelist.admins[msg.From.ID] {
		reply("当前配置仅允许指定管理员修改白名单。")
		return true
	}
	if len(parts) == 2 {
		userID, _ = strconv.ParseInt(parts[1], 10, 64)
	}
	if len(parts) == 1 && msg.ReplyToMessage != nil && msg.ReplyToMessage.From != nil && msg.ReplyToMessage.SenderChat == nil {
		userID = msg.ReplyToMessage.From.ID
	}
	if userID <= 0 {
		reply("请回复目标成员的消息发送 " + command + "，或使用 " + command + " 用户数字ID。")
		return true
	}
	add := command == "/whitelist_add"
	if err := groupWhitelist.set(msg.Chat.ID, userID, add); err != nil {
		log.Printf("保存群 %d 白名单失败: %v", msg.Chat.ID, err)
		reply("白名单保存失败，操作未生效，请检查日志。")
		return true
	}
	action := "移出"
	if add {
		action = "加入"
	}
	log.Printf("管理员 %d 将用户 %d %s群 %d 白名单", msg.From.ID, userID, action, msg.Chat.ID)
	reply(fmt.Sprintf("已将用户 %d %s本群消息白名单。", userID, action))
	return true
}
