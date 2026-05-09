package utils

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/Fhokud/tg_Verify_Bot/internal/models"
)

func SafeGo(f func()) {
	models.Wg.Add(1)
	go func() {
		defer models.Wg.Done()
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[panic recovered] %v", r)
			}
		}()
		f()
	}()
}

func BoolPtr(b bool) *bool { return &b }

func IsDuplicateMessage(userID int64, text string, chatID int64, messageID int, window time.Duration) ([]int64, []int, bool) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, nil, false
	}

	now := time.Now()

	models.LastMsgMu.Lock()
	defer models.LastMsgMu.Unlock()

	msgs, ok := models.LastMessages[userID]
	if !ok {
		msgs = []models.LastMessage{}
	}

	var toDeleteChatIDs []int64
	var toDeleteMessageIDs []int
	var newMsgs []models.LastMessage
	isDuplicate := false

	for _, m := range msgs {
		if now.Sub(m.Time) <= window {
			if m.Text == text {
				toDeleteChatIDs = append(toDeleteChatIDs, m.ChatID)
				toDeleteMessageIDs = append(toDeleteMessageIDs, m.MessageID)
				isDuplicate = true
			} else {
				newMsgs = append(newMsgs, m)
			}
		} // else discard old
	}

	if isDuplicate {
		toDeleteChatIDs = append(toDeleteChatIDs, chatID)
		toDeleteMessageIDs = append(toDeleteMessageIDs, messageID)
		models.LastMessages[userID] = newMsgs // remove the duplicates
		return toDeleteChatIDs, toDeleteMessageIDs, true
	} else {
		newMsgs = append(newMsgs, models.LastMessage{Text: text, Time: now, MessageID: messageID, ChatID: chatID})
		models.LastMessages[userID] = newMsgs
		return nil, nil, false
	}
}

func StartMessageCacheCleaner(ctx context.Context) {
	SafeGo(func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return

			case <-ticker.C:
				now := time.Now()

				models.LastMsgMu.Lock()
				for uid, msgs := range models.LastMessages {
					var newMsgs []models.LastMessage
					for _, msg := range msgs {
						if now.Sub(msg.Time) <= 12*time.Hour {
							newMsgs = append(newMsgs, msg)
						}
					}
					if len(newMsgs) == 0 {
						delete(models.LastMessages, uid)
					} else {
						models.LastMessages[uid] = newMsgs
					}
				}
				models.LastMsgMu.Unlock()
			}
		}
	})
}

func Retry(attempts int, initialSleep time.Duration, fn func() error) error {
	sleep := initialSleep
	for i := 0; i < attempts; i++ {
		if err := fn(); err == nil {
			return nil
		} else if i == attempts-1 {
			return err
		}
		time.Sleep(sleep)
		sleep *= 2
	}
	return nil
}
