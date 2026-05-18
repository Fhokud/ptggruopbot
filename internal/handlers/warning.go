package handlers

import (
	"context"
	"fmt"
	"time"

	"github.com/Fhokud/tg_Verify_Bot/internal/telegram"
	"github.com/Fhokud/tg_Verify_Bot/internal/utils"
	"github.com/go-telegram/bot"
	tgmodels "github.com/go-telegram/bot/models"
)

func sendAutoDeleteWarning(ctx context.Context, b *bot.Bot, chatID, userID int64, userName, reason string) {
	warnText := fmt.Sprintf("<a href=\"tg://user?id=%d\">%s</a>：%s", userID, userName, reason)
	warnMsg, err := b.SendMessage(ctx, &bot.SendMessageParams{
		ChatID:    chatID,
		Text:      warnText,
		ParseMode: tgmodels.ParseModeHTML,
	})
	if err != nil {
		return
	}

	time.AfterFunc(5*time.Second, func() {
		utils.SafeGo(func() {
			_ = telegram.DeleteMessageWithRetry(ctx, b, chatID, warnMsg.ID)
		})
	})
}
