package relay

import (
	"path/filepath"
	"testing"
	"time"
)

func TestPersistentRoutesSurviveRestart(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "routes.json")
	m, err := NewPersistent(100, stateFile)
	if err != nil {
		t.Fatal(err)
	}
	want := route{UserChatID: 200, UserMessageID: 7, ReplyToOriginal: true, CreatedAt: time.Now()}
	m.remember(want, 10, 11)

	reloaded, err := NewPersistent(100, stateFile)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reloaded.lookup(11)
	if !ok || got.UserChatID != want.UserChatID || got.UserMessageID != want.UserMessageID {
		t.Fatalf("reloaded route = %#v, %v; want %#v, true", got, ok, want)
	}
}

func TestHeaderAndMessageRoutesKeepDifferentReplyBehavior(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "routes.json")
	m, err := NewPersistent(100, stateFile)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	m.remember(route{UserChatID: 200, CreatedAt: now}, 10)
	m.remember(route{UserChatID: 200, UserMessageID: 7, ReplyToOriginal: true, CreatedAt: now}, 11)

	reloaded, err := NewPersistent(100, stateFile)
	if err != nil {
		t.Fatal(err)
	}
	headerRoute, ok := reloaded.lookup(10)
	if !ok || headerRoute.ReplyToOriginal || headerRoute.UserMessageID != 0 {
		t.Fatalf("header route = %#v, %v; want no replied-to message", headerRoute, ok)
	}
	messageRoute, ok := reloaded.lookup(11)
	if !ok || !messageRoute.ReplyToOriginal || messageRoute.UserMessageID != 7 {
		t.Fatalf("message route = %#v, %v; want user message 7", messageRoute, ok)
	}
}

func TestPersistentRoutesExpireAfterThirtyDays(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "routes.json")
	m, err := NewPersistent(100, stateFile)
	if err != nil {
		t.Fatal(err)
	}
	m.remember(route{UserChatID: 200, UserMessageID: 7, CreatedAt: time.Now().Add(-routeTTL - time.Hour)}, 10)

	reloaded, err := NewPersistent(100, stateFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.lookup(10); ok {
		t.Fatal("expired route was loaded")
	}
}

func TestPersistentRoutesAreScopedToAdmin(t *testing.T) {
	stateFile := filepath.Join(t.TempDir(), "routes.json")
	m, err := NewPersistent(100, stateFile)
	if err != nil {
		t.Fatal(err)
	}
	m.remember(route{UserChatID: 200, UserMessageID: 7, CreatedAt: time.Now()}, 10)

	reloaded, err := NewPersistent(101, stateFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reloaded.lookup(10); ok {
		t.Fatal("route from another administrator was loaded")
	}
}
