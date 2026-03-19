// Package logger 提供基于 slog 的结构化文件日志。
//
// 日志文件按日期和大小（10MB）轮转，写入系统临时目录的 netsgo/ 子目录：
//   - Unix/macOS: /tmp/netsgo/netsgo-server-2026-03-19-000.log
//   - Windows:    %TEMP%\netsgo\netsgo-server-2026-03-19-000.log
//
// 同一日期内序号持续累加，重启进程会从已有最大序号继续。
//
// 用法：
//
//	logger.Init("server")          // 初始化，传入角色
//	defer logger.Close()
//	// 之后所有 log.Printf / slog.Info 等调用自动双写到 stderr + 日志文件
package logger

import (
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxFileSize int64 = 10 << 20 // 10 MB

// RotatingWriter 实现按日期+大小轮转的 io.Writer。
// 文件命名: netsgo-{role}-{date}-{seq:03d}.log
type RotatingWriter struct {
	mu      sync.Mutex
	dir     string
	role    string
	file    *os.File
	date    string // 当前日期 "2006-01-02"
	seq     int    // 当前序号
	written int64  // 当前文件已写入字节数
}

func newRotatingWriter(dir, role string) (*RotatingWriter, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("创建日志目录失败: %w", err)
	}

	w := &RotatingWriter{dir: dir, role: role}
	w.date = time.Now().Format("2006-01-02")

	// 扫描已有文件，找到今天最大的序号
	maxSeq := w.scanMaxSeq()
	if maxSeq < 0 {
		w.seq = 0 // 今天还没有日志文件
	} else {
		// 检查最新文件是否还能继续写
		path := w.filePath(maxSeq)
		if info, err := os.Stat(path); err == nil && info.Size() < maxFileSize {
			w.seq = maxSeq // 继续写入已有文件
		} else {
			w.seq = maxSeq + 1 // 已有文件已满，开新文件
		}
	}

	if err := w.openFile(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *RotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	today := time.Now().Format("2006-01-02")
	needRotate := false

	if today != w.date {
		// 日期变更 → 切到新日期
		w.date = today
		maxSeq := w.scanMaxSeq()
		if maxSeq < 0 {
			w.seq = 0
		} else {
			w.seq = maxSeq + 1
		}
		needRotate = true
	} else if w.written >= maxFileSize {
		// 文件大小超限 → 递增序号
		w.seq++
		needRotate = true
	}

	if needRotate {
		if err := w.openFile(); err != nil {
			return 0, err
		}
	}

	n, err := w.file.Write(p)
	w.written += int64(n)
	return n, err
}

// Close 关闭当前日志文件。
func (w *RotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		return w.file.Close()
	}
	return nil
}

func (w *RotatingWriter) filePath(seq int) string {
	return filepath.Join(w.dir, fmt.Sprintf("netsgo-%s-%s-%03d.log", w.role, w.date, seq))
}

func (w *RotatingWriter) openFile() error {
	if w.file != nil {
		w.file.Close()
	}

	path := w.filePath(w.seq)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("打开日志文件失败 (%s): %w", path, err)
	}

	info, err := f.Stat()
	if err != nil {
		f.Close()
		return fmt.Errorf("获取日志文件信息失败: %w", err)
	}

	w.file = f
	w.written = info.Size()
	return nil
}

// scanMaxSeq 扫描当前日期的日志文件，返回最大序号。无文件时返回 -1。
func (w *RotatingWriter) scanMaxSeq() int {
	prefix := fmt.Sprintf("netsgo-%s-%s-", w.role, w.date)
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return -1
	}

	maxSeq := -1
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".log") {
			continue
		}
		seqStr := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".log")
		if seq, err := strconv.Atoi(seqStr); err == nil && seq > maxSeq {
			maxSeq = seq
		}
	}
	return maxSeq
}

// --- 全局 API ---

var globalWriter *RotatingWriter

// Init 初始化日志系统。
// role 用于区分日志来源（"server" 或 "client"）。
// 调用后，所有 log.Printf 和 slog.* 调用都会同时输出到 stderr 和日志文件。
func Init(role string) error {
	dir := filepath.Join(os.TempDir(), "netsgo")

	w, err := newRotatingWriter(dir, role)
	if err != nil {
		return fmt.Errorf("初始化日志失败: %w", err)
	}
	globalWriter = w

	// 双写: stderr + 日志文件
	multi := io.MultiWriter(os.Stderr, w)

	handler := slog.NewTextHandler(multi, &slog.HandlerOptions{
		Level: slog.LevelDebug,
	})
	slog.SetDefault(slog.New(handler))

	// slog.SetDefault 会将标准 log 包的输出重定向到 slog handler，
	// 同时自动调用 log.SetFlags(0)，避免重复格式化。
	// 所以现有的 log.Printf 调用无需修改，会自动走 slog → 双写。

	log.Printf("📝 日志文件目录: %s", dir)
	return nil
}

// Close 关闭日志系统，释放文件句柄。
func Close() {
	if globalWriter != nil {
		globalWriter.Close()
	}
}

// Dir 返回日志目录路径。
func Dir() string {
	if globalWriter != nil {
		return globalWriter.dir
	}
	return ""
}
