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

type LastMessage struct {
	Text      string
	Time      time.Time
	MessageID int
	ChatID    int64
}

var (
	PendingPolls = make(map[int64]*PendingPoll)
	PollsMu      sync.RWMutex
	Wg           sync.WaitGroup
	AcMatcher    atomic.Value

	LastMessages = make(map[int64][]LastMessage)
	LastMsgMu    sync.Mutex
)
