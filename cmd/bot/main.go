package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/Fhokud/tg_Verify_Bot/internal/handlers"
	"github.com/Fhokud/tg_Verify_Bot/internal/keywords"
	"github.com/Fhokud/tg_Verify_Bot/internal/models"
	"github.com/Fhokud/tg_Verify_Bot/internal/telegram"
	"github.com/Fhokud/tg_Verify_Bot/internal/utils"
	"github.com/go-telegram/bot"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		log.Fatal("请先设置环境变量 TELEGRAM_BOT_TOKEN")
	}

	webhookURL := os.Getenv("TELEGRAM_WEBHOOK_URL")
	if webhookURL == "" {
		log.Fatal("请先设置环境变量 TELEGRAM_WEBHOOK_URL，例如 https://bot.example.com/webhook")
	}

	secretToken := os.Getenv("TELEGRAM_WEBHOOK_SECRET")

	listenAddr := os.Getenv("TELEGRAM_WEBHOOK_LISTEN")
	if listenAddr == "" {
		listenAddr = "127.0.0.1:898"
	}

	webhookPath := os.Getenv("TELEGRAM_WEBHOOK_PATH")
	if webhookPath == "" {
		webhookPath = "/webhook"
	}
	if !strings.HasPrefix(webhookPath, "/") {
		webhookPath = "/" + webhookPath
	}

	duplicateWindow := utils.DuplicateMessageWindow()

	b, err := bot.New(token, bot.WithDefaultHandler(handlers.NewDefaultHandler(duplicateWindow)))
	if err != nil {
		log.Fatalf("bot.New error: %v", err)
	}

	keywords.InitAC()
	utils.StartMessageCacheCleaner(ctx, duplicateWindow)
	log.Printf("重复消息匹配时段: %s", duplicateWindow)

	log.Println("正在删除旧 webhook，并丢弃 pending updates...")
	if err := telegram.DeleteWebhook(token); err != nil {
		log.Fatalf("deleteWebhook failed: %v", err)
	}
	log.Println("✅ 旧 webhook 已删除")

	log.Printf("正在设置 webhook: %s", webhookURL)
	if err := telegram.SetupWebhook(token, webhookURL, secretToken); err != nil {
		log.Fatalf("setWebhook failed: %v", err)
	}
	log.Println("✅ webhook 已设置")

	mux := http.NewServeMux()
	mux.HandleFunc(webhookPath, telegram.WebhookHandler(ctx, b, secretToken))

	server := &http.Server{
		Addr:              listenAddr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("Bot webhook 已启动，监听 http://%s%s", listenAddr, webhookPath)

	utils.SafeGo(func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	})

	<-ctx.Done()

	log.Println("收到退出信号，正在关闭 HTTP 服务...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	_ = server.Shutdown(shutdownCtx)

	log.Println("等待异步任务完成...")
	models.Wg.Wait()
	log.Println("已干净退出")
}
