package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/Fhokud/tg_Verify_Bot/internal/utils"
	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

func RestrictUser(ctx context.Context, b *bot.Bot, chatID, userID int64) error {
	_, err := b.RestrictChatMember(ctx, &bot.RestrictChatMemberParams{
		ChatID: chatID,
		UserID: userID,
		Permissions: &models.ChatPermissions{
			CanSendMessages:       false,
			CanSendAudios:         false,
			CanSendDocuments:      false,
			CanSendPhotos:         false,
			CanSendVideos:         false,
			CanSendVideoNotes:     false,
			CanSendVoiceNotes:     false,
			CanSendPolls:          false,
			CanSendOtherMessages:  false,
			CanAddWebPagePreviews: false,
			CanChangeInfo:         false,
			CanInviteUsers:        false,
			CanPinMessages:        false,
			CanManageTopics:       false,
		},
	})
	return err
}

func UnrestrictUser(ctx context.Context, b *bot.Bot, chatID, userID int64) error {
	_, err := b.RestrictChatMember(ctx, &bot.RestrictChatMemberParams{
		ChatID: chatID,
		UserID: userID,
		Permissions: &models.ChatPermissions{
			CanSendMessages:       true,
			CanSendAudios:         true,
			CanSendDocuments:      true,
			CanSendPhotos:         true,
			CanSendVideos:         true,
			CanSendVideoNotes:     true,
			CanSendVoiceNotes:     true,
			CanSendPolls:          false,
			CanSendOtherMessages:  true,
			CanAddWebPagePreviews: false,
			CanChangeInfo:         false,
			CanInviteUsers:        false,
			CanPinMessages:        false,
			CanManageTopics:       false,
		},
	})
	return err
}

func BanUserWithRetry(ctx context.Context, b *bot.Bot, chatID, userID int64, duration time.Duration) error {
	return utils.Retry(3, 500*time.Millisecond, func() error {
		_, err := b.BanChatMember(ctx, &bot.BanChatMemberParams{
			ChatID:         chatID,
			UserID:         userID,
			UntilDate:      int(time.Now().Add(duration).Unix()),
			RevokeMessages: true,
		})
		return err
	})
}

func DeleteMessageWithRetry(ctx context.Context, b *bot.Bot, chatID int64, messageID int) error {
	return utils.Retry(3, 300*time.Millisecond, func() error {
		_, err := b.DeleteMessage(ctx, &bot.DeleteMessageParams{
			ChatID:    chatID,
			MessageID: messageID,
		})
		return err
	})
}

func DeleteMessagesWithRetry(ctx context.Context, b *bot.Bot, chatID int64, messageIDs []int) error {
	const maxDeleteMessages = 100

	for start := 0; start < len(messageIDs); start += maxDeleteMessages {
		end := start + maxDeleteMessages
		if end > len(messageIDs) {
			end = len(messageIDs)
		}

		ids := messageIDs[start:end]
		if err := utils.Retry(3, 300*time.Millisecond, func() error {
			_, err := b.DeleteMessages(ctx, &bot.DeleteMessagesParams{
				ChatID:     chatID,
				MessageIDs: ids,
			})
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}

func SendPollWithRetry(ctx context.Context, b *bot.Bot, chatID int64, question string, options []models.InputPollOption, anonymous bool, openPeriod int) (*models.Message, error) {
	var msg *models.Message
	err := utils.Retry(3, 300*time.Millisecond, func() error {
		m, err := b.SendPoll(ctx, &bot.SendPollParams{
			ChatID:      chatID,
			Question:    question,
			Options:     options,
			IsAnonymous: utils.BoolPtr(anonymous),
			OpenPeriod:  openPeriod,
		})
		if err == nil {
			msg = m
		}
		return err
	})
	return msg, err
}

func TelegramAPI(token, method string, payload map[string]any) error {
	apiURL := fmt.Sprintf("https://api.telegram.org/bot%s/%s", token, method)

	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequest(http.MethodPost, apiURL, bytes.NewBuffer(body))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("telegram %s failed: status=%s body=%s", method, resp.Status, string(respBody))
	}

	var result struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}

	if err := json.Unmarshal(respBody, &result); err == nil {
		if !result.OK {
			return fmt.Errorf("telegram %s failed: %s", method, result.Description)
		}
	}

	return nil
}

func DeleteWebhook(token string) error {
	return TelegramAPI(token, "deleteWebhook", map[string]any{
		"drop_pending_updates": true,
	})
}

func SetupWebhook(token, webhookURL, secretToken string) error {
	payload := map[string]any{
		"url": webhookURL,
		"allowed_updates": []string{
			"chat_join_request",
			"poll_answer",
			"poll",
			"message",
		},
	}

	if secretToken != "" {
		payload["secret_token"] = secretToken
	}

	return TelegramAPI(token, "setWebhook", payload)
}

func WebhookHandler(ctx context.Context, b *bot.Bot, secretToken string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if secretToken != "" {
			got := r.Header.Get("X-Telegram-Bot-Api-Secret-Token")
			if got != secretToken {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}

		defer r.Body.Close()

		var update models.Update
		if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			log.Printf("decode webhook update failed: %v", err)
			return
		}

		b.ProcessUpdate(ctx, &update)

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	}
}
