package utils

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/Fhokud/tg_Verify_Bot/internal/models"
	tgmodels "github.com/go-telegram/bot/models"
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

func MessageFingerprint(text string, photos []tgmodels.PhotoSize) string {
	photoUniqueID := largestPhotoUniqueID(photos)
	if photoUniqueID != "" {
		return "photo:" + photoUniqueID
	}

	normalized := NormalizeMessage(text)
	if normalized != "" {
		return fmt.Sprintf("text:%016x", xxh3.HashString(normalized))
	}

	return ""
}

func largestPhotoUniqueID(photos []tgmodels.PhotoSize) string {
	var best tgmodels.PhotoSize
	for _, photo := range photos {
		if photo.FileUniqueID == "" {
			continue
		}
		if photo.Width*photo.Height > best.Width*best.Height {
			best = photo
		}
	}
	return best.FileUniqueID
}

func IsDuplicateMessage(userID int64, fingerprint string, chatID int64, messageID int, window time.Duration) ([]int, bool) {
	if fingerprint == "" {
		return nil, false
	}

	now := time.Now()
	nowUnix := now.UnixNano()
	expiresAt := now.Add(window).UnixNano()
	key := models.LastUserMessageKey{
		ChatID: chatID,
		UserID: userID,
	}
	record := &models.LastUserMessage{
		Fingerprint: fingerprint,
		MessageID:   messageID,
		ExpiresAt:   expiresAt,
	}

	for {
		actual, loaded := models.LastUserMessages.LoadOrStore(key, record)
		if !loaded {
			return nil, false
		}

		previous := actual.(*models.LastUserMessage)
		if previous.ExpiresAt <= nowUnix {
			if models.LastUserMessages.CompareAndSwap(key, previous, record) {
				return nil, false
			}
			continue
		}

		if previous.Fingerprint == fingerprint {
			if models.LastUserMessages.CompareAndSwap(key, previous, record) {
				return []int{previous.MessageID, messageID}, true
			}
			continue
		}

		if models.LastUserMessages.CompareAndSwap(key, previous, record) {
			return nil, false
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
