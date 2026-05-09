package keywords

import (
	"bufio"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"

	appmodels "github.com/Fhokud/tg_Verify_Bot/internal/models"
	"github.com/cloudflare/ahocorasick"
	"github.com/fsnotify/fsnotify"
	tgmodels "github.com/go-telegram/bot/models"
)

func LoadKeywordsFolderHot(folder string) []string {
	info, err := os.Stat(folder)
	if err != nil || !info.IsDir() {
		log.Printf("⚠️ 无法访问文件夹 %s: %v", folder, err)
		return nil
	}

	var keywords []string
	filepath.WalkDir(folder, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".txt") {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			log.Printf("⚠️ 打开文件 %s 失败: %v", path, err)
			return nil
		}
		defer f.Close()

		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line != "" {
				keywords = append(keywords, strings.ToLower(line))
			}
		}
		if err := scanner.Err(); err != nil {
			log.Printf("⚠️ 读取文件 %s 失败: %v", path, err)
		}
		return nil
	})

	if len(keywords) == 0 {
		log.Printf("⚠️ 文件夹 %s 下没有读取到任何关键词", folder)
	} else {
		log.Printf("🔑 已加载 %d 个关键词", len(keywords))
	}

	return keywords
}

func ExtractTextFromMessage(msg *tgmodels.Message) string {
	var parts []string
	if msg == nil {
		return ""
	}
	if msg.Text != "" {
		parts = append(parts, msg.Text)
	}
	if msg.Caption != "" {
		parts = append(parts, msg.Caption)
	}
	return strings.Join(parts, "\n")
}

func NormalizeForKeyword(s string) string {
	s = strings.ToLower(s)

	var b strings.Builder
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' ||
			r == '\u200b' || r == '\u200c' || r == '\u200d' || r == '\ufeff' {
			continue
		}

		if strings.ContainsRune(".,!?;:'\"`~，。！？；：“”‘’、·-_=+*/\\|()[]{}<>《》【】（）", r) {
			continue
		}

		b.WriteRune(r)
	}

	return b.String()
}

func ContainsAnyKeywordAC(text string) bool {
	if text == "" {
		return false
	}
	v := appmodels.AcMatcher.Load()
	if v == nil {
		return false
	}

	matcher := v.(*ahocorasick.Matcher)

	normalized := NormalizeForKeyword(text)
	matches := matcher.Match([]byte(normalized))
	return len(matches) > 0
}

func InitAC() {
	folder := "keywords"

	if _, err := os.Stat(folder); os.IsNotExist(err) {
		log.Printf("⚠️ 文件夹 %s 不存在，自动创建", folder)
		if err := os.MkdirAll(folder, 0755); err != nil {
			log.Fatalf("创建文件夹 %s 失败: %v", folder, err)
		}
	}

	appmodels.AcMatcher.Store(ahocorasick.NewStringMatcher([]string{}))

	keywords := LoadKeywordsFolderHot(folder)
	if len(keywords) > 0 {
		appmodels.AcMatcher.Store(ahocorasick.NewStringMatcher(keywords))
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatalf("fsnotify.NewWatcher failed: %v", err)
	}

	go func() {
		defer watcher.Close()
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove) != 0 {
					keywords := LoadKeywordsFolderHot(folder)
					if len(keywords) > 0 {
						appmodels.AcMatcher.Store(ahocorasick.NewStringMatcher(keywords))
					} else {
						appmodels.AcMatcher.Store(ahocorasick.NewStringMatcher([]string{}))
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Printf("fsnotify error: %v", err)
			}
		}
	}()

	if err := watcher.Add(folder); err != nil {
		log.Fatalf("fsnotify add folder failed: %v", err)
	}
}
