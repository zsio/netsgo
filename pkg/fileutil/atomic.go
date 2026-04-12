// Package fileutil 提供文件操作工具函数。
package fileutil

import (
	"fmt"
	"os"
	"path/filepath"
)

// AtomicWriteFile 原子写入文件：先写入同目录临时文件，再 rename 替换目标文件。
// 在同一文件系统上，os.Rename 是原子操作（POSIX 保证），
// 因此即使进程在写入过程中崩溃，目标文件也不会被截断或损坏。
func AtomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)

	// 在同目录下创建临时文件，确保与目标文件在同一文件系统
	tmp, err := os.CreateTemp(dir, base+".tmp.*")
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpPath := tmp.Name()

	// 确保失败时清理临时文件
	success := false
	defer func() {
		if !success {
			_ = os.Remove(tmpPath)
		}
	}()

	// 写入数据
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("写入临时文件失败: %w", err)
	}

	// 刷盘：确保数据到达磁盘，而不是留在 OS 缓冲区
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("fsync 临时文件失败: %w", err)
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("关闭临时文件失败: %w", err)
	}

	// 设置权限（CreateTemp 默认 0600，这里显式确保）
	if err := os.Chmod(tmpPath, perm); err != nil {
		return fmt.Errorf("设置文件权限失败: %w", err)
	}

	// 原子替换：在同一文件系统上，Rename 是原子操作
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("原子替换文件失败: %w", err)
	}

	success = true
	return nil
}
