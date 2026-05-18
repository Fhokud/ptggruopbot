package handlers

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/Fhokud/tg_Verify_Bot/internal/telegram"
	"github.com/Fhokud/tg_Verify_Bot/internal/utils"
	"github.com/go-telegram/bot"
	tgmodels "github.com/go-telegram/bot/models"
)

const forwardBatchDelay = 2 * time.Second

type forwardBatchKey struct {
	ChatID int64
	UserID int64
}

type forwardMessageBatch struct {
	mu         sync.Mutex
	messageIDs []int
	userName   string
	timer      *time.Timer
	closed     bool
}

var forwardBatches sync.Map

func queueForwardMessage(ctx context.Context, b *bot.Bot, chatID, userID int64, userName string, messageID int) {
	key := forwardBatchKey{ChatID: chatID, UserID: userID}

	for {
		value, loaded := forwardBatches.LoadOrStore(key, &forwardMessageBatch{
			messageIDs: []int{messageID},
			userName:   userName,
		})
		batch := value.(*forwardMessageBatch)

		batch.mu.Lock()
		if batch.closed {
			batch.mu.Unlock()
			forwardBatches.CompareAndDelete(key, batch)
			continue
		}

		if loaded {
			batch.messageIDs = append(batch.messageIDs, messageID)
			batch.userName = userName
		}

		if batch.timer == nil {
			batch.timer = time.AfterFunc(forwardBatchDelay, func() {
				utils.SafeGo(func() {
					flushForwardMessageBatch(ctx, b, key, batch)
				})
			})
		} else {
			batch.timer.Reset(forwardBatchDelay)
		}
		batch.mu.Unlock()
		return
	}
}

func flushForwardMessageBatch(ctx context.Context, b *bot.Bot, key forwardBatchKey, batch *forwardMessageBatch) {
	batch.mu.Lock()
	if batch.closed {
		batch.mu.Unlock()
		return
	}
	batch.closed = true
	messageIDs := append([]int(nil), batch.messageIDs...)
	userName := batch.userName
	batch.mu.Unlock()

	forwardBatches.CompareAndDelete(key, batch)

	if len(messageIDs) == 0 {
		return
	}

	_ = telegram.DeleteMessagesWithRetry(ctx, b, key.ChatID, messageIDs)
	log.Printf("🚫 已批量删除用户 %d(%s) 的 %d 条转发消息", key.UserID, userName, len(messageIDs))

	warnText := fmt.Sprintf("<a href=\"tg://user?id=%d\">%s</a>：请注意，禁止批量转发消息。", key.UserID, userName)
	warnMsg, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:    key.ChatID,
		Text:      warnText,
		ParseMode: tgmodels.ParseModeHTML,
	})
	if err != nil {
		return
	}

	time.AfterFunc(60*time.Second, func() {
		utils.SafeGo(func() {
			_ = telegram.DeleteMessageWithRetry(ctx, b, key.ChatID, warnMsg.ID)
		})
	})
}
