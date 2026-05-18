package models

import (
	"sync"
	"sync/atomic"
	"time"
)

type PendingPoll struct {
	UserID          int64
	ChatID          int64
	Username        string
	PollMessageID   int
	NoticeMessageID int
	Timer           *time.Timer
	Voted           bool
}

type LastUserMessage struct {
	Hash      uint64
	MessageID int
	ExpiresAt int64
}

type LastUserMessageKey struct {
	ChatID int64
	UserID int64
}

var (
	PendingPolls = make(map[int64]*PendingPoll)
	PollsMu      sync.RWMutex
	Wg           sync.WaitGroup
	AcMatcher    atomic.Value

	LastUserMessages sync.Map
)
