package fileutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicWriteFile_Basic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.json")

	data := []byte(`{"key": "value"}`)
	if err := AtomicWriteFile(path, data, 0o600); err != nil {
		t.Fatalf("AtomicWriteFile 失败: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取文件失败: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("文件内容不一致: got %q, want %q", got, data)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("获取文件信息失败: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("文件权限不正确: got %o, want %o", info.Mode().Perm(), 0o600)
	}
}

func TestAtomicWriteFile_OverwriteExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.json")

	// 写入初始内容
	if err := AtomicWriteFile(path, []byte(`{"v": 1}`), 0o600); err != nil {
		t.Fatalf("初次写入失败: %v", err)
	}

	// 覆盖写入
	newData := []byte(`{"v": 2, "extra": true}`)
	if err := AtomicWriteFile(path, newData, 0o600); err != nil {
		t.Fatalf("覆盖写入失败: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取文件失败: %v", err)
	}
	if string(got) != string(newData) {
		t.Errorf("文件内容不一致: got %q, want %q", got, newData)
	}
}

func TestAtomicWriteFile_NoTmpLeftOnSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.json")

	if err := AtomicWriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("读取目录失败: %v", err)
	}
	if len(entries) != 1 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("目录中应该只有 1 个文件, 实际有 %d: %v", len(entries), names)
	}
}

func TestAtomicWriteFile_InvalidDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent", "sub", "test.json")
	err := AtomicWriteFile(path, []byte(`{}`), 0o600)
	if err == nil {
		t.Fatal("预期写入不存在的目录应失败")
	}
}

func TestAtomicWriteFile_EmptyData(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.json")

	if err := AtomicWriteFile(path, []byte{}, 0o600); err != nil {
		t.Fatalf("写入空数据失败: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取文件失败: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("期望空文件, 实际有 %d 字节", len(got))
	}
}
