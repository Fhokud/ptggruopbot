package utils

import (
	"context"
	"log"
	"os"
	"strings"
	"time"

	"github.com/Fhokud/tg_Verify_Bot/internal/models"
	"github.com/zeebo/xxh3"
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

const (
	defaultDuplicateMessageWindow = 48 * time.Hour
	duplicateMessageWindowEnv     = "DUPLICATE_MESSAGE_WINDOW"
)

func DuplicateMessageWindow() time.Duration {
	raw := strings.TrimSpace(os.Getenv(duplicateMessageWindowEnv))
	if raw == "" {
		return defaultDuplicateMessageWindow
	}

	if d, err := time.ParseDuration(strings.ToLower(raw)); err == nil && d > 0 {
		return d
	}

	if hours, err := time.ParseDuration(raw + "h"); err == nil && hours > 0 {
		return hours
	}

	log.Printf("环境变量 %s=%q 无效，使用默认重复消息匹配时段 %s", duplicateMessageWindowEnv, raw, defaultDuplicateMessageWindow)
	return defaultDuplicateMessageWindow
}

func NormalizeMessage(text string) string {
	text = strings.ToValidUTF8(text, "")
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return strings.ToLower(strings.Join(strings.Fields(text), " "))
}

func IsDuplicateMessage(userID int64, text string, chatID int64, messageID int, window time.Duration) ([]int64, []int, bool) {
	normalized := NormalizeMessage(text)
	if normalized == "" {
		return nil, nil, false
	}

	now := time.Now()
	nowUnix := now.UnixNano()
	hash := xxh3.HashString(normalized)
	expiresAt := now.Add(window).UnixNano()
	record := &models.LastUserMessage{
		Hash:      hash,
		ChatID:    chatID,
		MessageID: messageID,
		ExpiresAt: expiresAt,
	}

	for {
		actual, loaded := models.LastUserMessages.LoadOrStore(userID, record)
		if !loaded {
			return nil, nil, false
		}

		previous := actual.(*models.LastUserMessage)
		if previous.ExpiresAt <= nowUnix {
			if models.LastUserMessages.CompareAndSwap(userID, previous, record) {
				return nil, nil, false
			}
			continue
		}

		if previous.Hash == hash {
			if models.LastUserMessages.CompareAndSwap(userID, previous, record) {
				return []int64{previous.ChatID, chatID}, []int{previous.MessageID, messageID}, true
			}
			continue
		}

		if models.LastUserMessages.CompareAndSwap(userID, previous, record) {
			return nil, nil, false
		}
	}
}

func StartMessageCacheCleaner(ctx context.Context, window time.Duration) {
	SafeGo(func() {
		interval := window / 24
		if interval < time.Minute {
			interval = time.Minute
		}
		if interval > 10*time.Minute {
			interval = 10 * time.Minute
		}

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return

			case <-ticker.C:
				now := time.Now().UnixNano()
				models.LastUserMessages.Range(func(key, value any) bool {
					msg := value.(*models.LastUserMessage)
					if msg.ExpiresAt <= now {
						models.LastUserMessages.CompareAndDelete(key, msg)
					}
					return true
				})
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
