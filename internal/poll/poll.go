package poll

import (
	"context"

	"github.com/Fhokud/tg_Verify_Bot/internal/models"
	"github.com/Fhokud/tg_Verify_Bot/internal/telegram"
	"github.com/go-telegram/bot"
)

func SetPending(p *models.PendingPoll) {
	models.PollsMu.Lock()
	defer models.PollsMu.Unlock()
	models.PendingPolls[p.UserID] = p
}

func GetPending(userID int64) (*models.PendingPoll, bool) {
	models.PollsMu.RLock()
	defer models.PollsMu.RUnlock()
	p, ok := models.PendingPolls[userID]
	return p, ok
}

func DeletePending(userID int64) {
	models.PollsMu.Lock()
	p, ok := models.PendingPolls[userID]
	if ok {
		if p.Timer != nil {
			p.Timer.Stop()
			p.Timer = nil
		}
		delete(models.PendingPolls, userID)
	}
	models.PollsMu.Unlock()
}

func CleanupPending(ctx context.Context, b *bot.Bot, p *models.PendingPoll) {
	if p.PollMessageID != 0 {
		_ = telegram.DeleteMessageWithRetry(ctx, b, p.ChatID, p.PollMessageID)
	}
	if p.NoticeMessageID != 0 {
		_ = telegram.DeleteMessageWithRetry(ctx, b, p.ChatID, p.NoticeMessageID)
	}
	DeletePending(p.UserID)
}
