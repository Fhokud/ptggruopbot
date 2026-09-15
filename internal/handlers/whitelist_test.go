package handlers

import (
	"context"

	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-telegram/bot"
	tgmodels "github.com/go-telegram/bot/models"
)

func TestDefaultAdminCommandsAndMentionMenu(t *testing.T) {
	old := groupWhitelist
	t.Cleanup(func() { groupWhitelist = old })
	if err := ConfigureWhitelist("", filepath.Join(t.TempDir(), "state.json")); err != nil {
		t.Fatal(err)
	}
	status := "administrator"
	var reply string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			fmt.Fprint(w, `{"ok":true,"result":{"id":1,"is_bot":true,"username":"TestBot"}}`)
		case strings.HasSuffix(r.URL.Path, "/getChatMember"):
			fmt.Fprintf(w, `{"ok":true,"result":{"status":%q,"user":{"id":999}}}`, status)
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Error(err)
			}
			reply = r.FormValue("text")
			fmt.Fprint(w, `{"ok":true,"result":{"message_id":1}}`)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	b, err := bot.New("1:test", bot.WithSkipGetMe(), bot.WithServerURL(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	msg := &tgmodels.Message{Chat: tgmodels.Chat{ID: -100, Type: tgmodels.ChatTypeSupergroup}, From: &tgmodels.User{ID: 999}}
	for _, command := range []string{"@TestBot", "/help@TestBot"} {
		msg.Text = command
		if !handleWhitelistCommand(context.Background(), b, msg) || !strings.Contains(reply, "管理员命令菜单") {
			t.Fatalf("missing menu for %s: %s", command, reply)
		}
	}
	msg.Text = "/whitelist_add 42"
	handleWhitelistCommand(context.Background(), b, msg)
	if !groupWhitelist.contains(-100, 42) {
		t.Fatal("default administrator could not add")
	}
	msg.Text = "/whitelist_remove 42"
	handleWhitelistCommand(context.Background(), b, msg)
	if groupWhitelist.contains(-100, 42) {
		t.Fatal("default administrator could not remove")
	}
	status = "member"
	msg.Text = "/whitelist_add 42"
	handleWhitelistCommand(context.Background(), b, msg)
	if groupWhitelist.contains(-100, 42) {
		t.Fatal("ordinary member added whitelist entry")
	}
}

func TestWhitelistCommandPermissions(t *testing.T) {
	old := groupWhitelist
	t.Cleanup(func() { groupWhitelist = old })
	for _, tc := range []struct {
		name      string
		actor     int64
		status    string
		anonymous bool
		want      bool
	}{
		{"designated admin", 123, "administrator", false, true},
		{"designated owner", 123, "creator", false, true},
		{"undesignated admin", 999, "administrator", false, false},
		{"former admin", 123, "member", false, false},
		{"anonymous admin", 123, "administrator", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ConfigureWhitelist("123", filepath.Join(t.TempDir(), "state.json")); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if strings.HasSuffix(r.URL.Path, "/getChatMember") {
					fmt.Fprintf(w, `{"ok":true,"result":{"status":%q,"user":{"id":123}}}`, tc.status)
					return
				}
				fmt.Fprint(w, `{"ok":true,"result":{"message_id":1,"chat":{"id":-100,"type":"supergroup"}}}`)
			}))
			defer server.Close()
			b, err := bot.New("1:test", bot.WithSkipGetMe(), bot.WithServerURL(server.URL))
			if err != nil {
				t.Fatal(err)
			}
			msg := &tgmodels.Message{Chat: tgmodels.Chat{ID: -100, Type: tgmodels.ChatTypeSupergroup}, From: &tgmodels.User{ID: tc.actor}, Text: "/whitelist_add 42"}
			if tc.anonymous {
				msg.SenderChat = &tgmodels.Chat{ID: -100}
			}
			DefaultHandler(context.Background(), b, &tgmodels.Update{Message: msg}, time.Hour)
			if got := groupWhitelist.contains(-100, 42); got != tc.want {
				t.Fatalf("granted = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWhitelistedMessagesSkipModeration(t *testing.T) {
	old := groupWhitelist
	t.Cleanup(func() { groupWhitelist = old })
	groupWhitelist = &whitelistStore{entries: map[whitelistEntry]bool{{-100, 42}: true}}
	msg := &tgmodels.Message{Chat: tgmodels.Chat{ID: -100, Type: tgmodels.ChatTypeSupergroup}, From: &tgmodels.User{ID: 42}, IsAutomaticForward: true}
	DefaultHandler(context.Background(), nil, &tgmodels.Update{Message: msg}, time.Hour)
	if _, exists := forwardBatches.Load(forwardBatchKey{ChatID: -100, UserID: 42}); exists {
		t.Fatal("whitelisted message queued for deletion")
	}
}

func TestDefaultAdminExemption(t *testing.T) {
	old := groupWhitelist
	t.Cleanup(func() { groupWhitelist = old })
	groupWhitelist = &whitelistStore{}
	for _, tc := range []struct {
		name       string
		status     string
		senderChat int64
		want       bool
	}{
		{"administrator", "administrator", 0, true},
		{"owner", "creator", 0, true},
		{"ordinary or demoted member", "member", 0, false},
		{"lookup failure", "error", 0, false},
		{"anonymous administrator", "", -100, true},
		{"channel sender", "", -200, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if tc.status == "error" {
					fmt.Fprint(w, `{"ok":false,"error_code":400,"description":"lookup failed"}`)
					return
				}
				fmt.Fprintf(w, `{"ok":true,"result":{"status":%q,"user":{"id":42}}}`, tc.status)
			}))
			defer server.Close()
			b, err := bot.New("1:test", bot.WithSkipGetMe(), bot.WithServerURL(server.URL))
			if err != nil {
				t.Fatal(err)
			}
			msg := &tgmodels.Message{Chat: tgmodels.Chat{ID: -100, Type: tgmodels.ChatTypeSupergroup}, From: &tgmodels.User{ID: 42}}
			if tc.senderChat != 0 {
				msg.SenderChat = &tgmodels.Chat{ID: tc.senderChat}
			}
			if got := isGroupMessageExempt(context.Background(), b, msg); got != tc.want {
				t.Fatalf("exempt = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWhitelistPersistenceAndGroupIsolation(t *testing.T) {
	old := groupWhitelist
	t.Cleanup(func() { groupWhitelist = old })
	path := filepath.Join(t.TempDir(), "whitelist.json")
	if err := ConfigureWhitelist("123, 456", path); err != nil {
		t.Fatal(err)
	}
	if !groupWhitelist.admins[123] || groupWhitelist.admins[999] {
		t.Fatal("incorrect administrator permissions")
	}
	if err := groupWhitelist.set(-100, 42, true); err != nil {
		t.Fatal(err)
	}
	if err := ConfigureWhitelist("123", path); err != nil {
		t.Fatal(err)
	}
	if !groupWhitelist.contains(-100, 42) || groupWhitelist.contains(-200, 42) {
		t.Fatal("whitelist must persist and remain scoped to a group")
	}
	if err := groupWhitelist.set(-100, 42, false); err != nil {
		t.Fatal(err)
	}
	if err := ConfigureWhitelist("", path); err != nil {
		t.Fatal(err)
	}
	if groupWhitelist.contains(-100, 42) {
		t.Fatal("removed entry persisted")
	}
}

func TestWhitelistFailedSaveDoesNotGrantExemption(t *testing.T) {
	dir := t.TempDir()
	s := &whitelistStore{path: dir, entries: make(map[whitelistEntry]bool)}
	if err := s.set(-100, 42, true); err == nil {
		t.Fatal("expected rename over directory to fail")
	}
	if s.contains(-100, 42) {
		t.Fatal("failed write granted exemption")
	}
}

func TestWhitelistInvalidConfiguration(t *testing.T) {
	old := groupWhitelist
	t.Cleanup(func() { groupWhitelist = old })
	path := filepath.Join(t.TempDir(), "whitelist.json")
	for _, value := range []string{"0", "-1", "123,", "abc"} {
		if err := ConfigureWhitelist(value, path); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
	if err := os.WriteFile(path, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ConfigureWhitelist("123", path); err == nil {
		t.Fatal("accepted corrupt state")
	}
}
