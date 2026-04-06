package svcmgr

import (
	"runtime"
	"testing"
)

func TestUserExists(t *testing.T) {
	exists, err := UserExists("root")
	if err != nil {
		t.Fatalf("UserExists(root) 不应报错: %v", err)
	}
	if !exists {
		t.Fatal("root 用户应存在")
	}

	exists, err = UserExists("netsgo-user-should-not-exist-xyz")
	if err != nil {
		t.Fatalf("UserExists(nonexistent) 不应报错: %v", err)
	}
	if exists {
		t.Fatal("随机不存在用户不应存在")
	}
}

func TestEnsureUserStub(t *testing.T) {
	if runtime.GOOS == "linux" {
		t.Skip("linux 平台下由真实实现覆盖")
	}

	if err := EnsureUser("netsgo"); err != ErrUnsupportedPlatform {
		t.Fatalf("非 linux 平台应返回 ErrUnsupportedPlatform，得到 %v", err)
	}
}
