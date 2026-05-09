package handlers

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/Fhokud/tg_Verify_Bot/internal/keywords"
	appmodels "github.com/Fhokud/tg_Verify_Bot/internal/models"
	"github.com/Fhokud/tg_Verify_Bot/internal/poll"
	"github.com/Fhokud/tg_Verify_Bot/internal/telegram"
	"github.com/Fhokud/tg_Verify_Bot/internal/utils"
	"github.com/go-telegram/bot"
	tgmodels "github.com/go-telegram/bot/models"
)

func DefaultHandler(ctx context.Context, b *bot.Bot, update *tgmodels.Update) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[handler panic recovered] %v", r)
		}
	}()

	if update.Message != nil {
		msg := update.Message
		chatID := msg.Chat.ID

		var userID int64
		var userName string

		if msg.From != nil {
			userID = msg.From.ID
			userName = msg.From.FirstName
			if msg.From.LastName != "" {
				userName += " " + msg.From.LastName
			}
		} else {
			userID = chatID
			userName = fmt.Sprintf("%d", chatID)
		}

		forwardDetected := false

		if msg.ForwardOrigin != nil {
			switch msg.ForwardOrigin.Type {
			case "user", "hidden_user", "chat", "channel":
				forwardDetected = true
			}
		}

		if msg.IsAutomaticForward {
			forwardDetected = true
		}

		if forwardDetected {
			_ = telegram.DeleteMessageWithRetry(ctx, b, chatID, msg.ID)
			log.Printf("🚫 已删除用户 %d(%s) 的转发消息", userID, userName)

			warnText := fmt.Sprintf("<a href=\"tg://user?id=%d\">%s</a>：请注意，禁止批量转发消息。", userID, userName)
			warnMsg, err := b.SendMessage(ctx, &bot.SendMessageParams{
				ChatID:    chatID,
				Text:      warnText,
				ParseMode: tgmodels.ParseModeHTML,
			})
			if err == nil {
				time.AfterFunc(60*time.Second, func() {
					utils.SafeGo(func() {
						_ = telegram.DeleteMessageWithRetry(ctx, b, chatID, warnMsg.ID)
					})
				})
			}
			return
		}

		content := keywords.ExtractTextFromMessage(msg)

		chatIDs, msgIDs, isDup := utils.IsDuplicateMessage(userID, content, chatID, msg.ID, 12*time.Hour)
		if isDup {
			for i, mid := range msgIDs {
				cid := chatIDs[i]
				_ = telegram.DeleteMessageWithRetry(ctx, b, cid, mid)
			}
			log.Printf("🚫 已删除用户 %d(%s) 的重复消息", userID, userName)
			return
		}

		if keywords.ContainsAnyKeywordAC(content) {
			_ = telegram.DeleteMessageWithRetry(ctx, b, chatID, msg.ID)
			log.Printf("🚫 已删除用户 %d(%s) 的敏感关键词消息", userID, userName)
			return
		}
	}
	if update.ChatJoinRequest != nil {
		req := update.ChatJoinRequest
		chatID := req.Chat.ID
		userID := req.From.ID
		username := req.From.Username

		if username == "" {
			_, _ = b.DeclineChatJoinRequest(ctx, &bot.DeclineChatJoinRequestParams{
				ChatID: chatID,
				UserID: userID,
			})
			log.Printf("🚫 已拒绝无用户名用户 %d(%s)", userID, username)
			return
		}

		ok, err := b.ApproveChatJoinRequest(ctx, &bot.ApproveChatJoinRequestParams{
			ChatID: chatID,
			UserID: userID,
		})
		if err != nil || !ok {
			log.Printf("批准用户 %d(%s) 失败: %v", userID, username, err)
			return
		}

		_ = telegram.RestrictUser(ctx, b, chatID, userID)

		noticeMsg, err := b.SendMessage(ctx, &bot.SendMessageParams{
			ChatID:    chatID,
			Text:      fmt.Sprintf("<a href=\"tg://user?id=%d\">%s</a>请进行验证（60秒内），如果验证失败可以稍后重试", userID, username),
			ParseMode: tgmodels.ParseModeHTML,
		})
		if err != nil {
			return
		}

		options := []tgmodels.InputPollOption{
			{Text: "✅ 验证"},
			{Text: "❌ 拒绝"},
		}

		pollMsg, err := telegram.SendPollWithRetry(ctx, b, chatID, "请选择验证选项", options, false, 60)
		if err != nil {
			_ = telegram.DeleteMessageWithRetry(ctx, b, chatID, noticeMsg.ID)
			return
		}

		p := &appmodels.PendingPoll{
			UserID:          userID,
			ChatID:          chatID,
			Username:        username,
			PollMessageID:   pollMsg.ID,
			NoticeMessageID: noticeMsg.ID,
		}
		poll.SetPending(p)

		timer := time.AfterFunc(60*time.Second, func() {
			utils.SafeGo(func() {
				pending, ok := poll.GetPending(userID)
				if !ok || pending.Voted {
					return
				}
				_ = telegram.BanUserWithRetry(ctx, b, pending.ChatID, pending.UserID, 1*time.Minute)
				poll.CleanupPending(ctx, b, pending)
			})
		})

		appmodels.PollsMu.Lock()
		if p2, ok := appmodels.PendingPolls[userID]; ok {
			p2.Timer = timer
			appmodels.PendingPolls[userID] = p2
		}
		appmodels.PollsMu.Unlock()
	}

	if update.PollAnswer != nil {
		answer := update.PollAnswer
		user := answer.User
		if user == nil {
			return
		}

		pollUserID := user.ID
		p, ok := poll.GetPending(pollUserID)
		if !ok {
			return
		}

		chosenAccept := false
		for _, optID := range answer.OptionIDs {
			if optID == 0 {
				chosenAccept = true
				break
			}
		}

		if chosenAccept {
			appmodels.PollsMu.Lock()
			if p.Timer != nil {
				p.Timer.Stop()
				p.Timer = nil
			}
			p.Voted = true
			appmodels.PollsMu.Unlock()

			utils.SafeGo(func() {
				poll.CleanupPending(ctx, b, p)
				_ = telegram.UnrestrictUser(ctx, b, p.ChatID, p.UserID)
				log.Printf("✅ 用户 %s(%d) 验证通过", p.Username, p.UserID)
			})
			return
		}

		for _, optID := range answer.OptionIDs {
			if optID == 1 {
				appmodels.PollsMu.Lock()
				if p.Timer != nil {
					p.Timer.Stop()
					p.Timer = nil
				}
				appmodels.PollsMu.Unlock()

				utils.SafeGo(func() {
					_ = telegram.BanUserWithRetry(ctx, b, p.ChatID, p.UserID, 1*time.Minute)
					poll.CleanupPending(ctx, b, p)
					log.Printf("❌ 用户 %s(%d) 投票拒绝，已封禁 1 分钟", p.Username, p.UserID)
				})
				return
			}
		}
	}
}
