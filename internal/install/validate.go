package install

import (
	"errors"
	"fmt"
	"os"
	"os/user"
	"strconv"
	"strings"
	"syscall"

	"netsgo/internal/clientaddr"
	"netsgo/internal/server"
)

func validateInstallClientServerURL(raw string) error {
	_, err := clientaddr.Normalize(raw, clientaddr.ModeManagedInstall)
	if err == nil {
		return nil
	}
	return errors.New(chineseServiceAddressError(err))
}

func chineseServiceAddressError(err error) string {
	msg := err.Error()
	switch {
	case msg == "service address cannot be empty":
		return "服务地址不能为空"
	case msg == "service address cannot contain whitespace":
		return "服务地址不能包含空白字符"
	case msg == "service address must include a scheme: http://, https://, ws://, or wss://":
		return "服务地址必须包含协议：http://、https://、ws:// 或 wss://"
	case strings.HasPrefix(msg, "service address must be a valid URL"):
		return "服务地址必须是有效 URL"
	case msg == "service address scheme must be http, https, ws, or wss":
		return "服务地址协议必须是 http、https、ws 或 wss"
	case msg == "service address must include a host":
		return "服务地址必须包含 host"
	case msg == "service address port is invalid":
		return "服务地址端口无效"
	case msg == "service address must not include user info":
		return "服务地址不能包含用户信息"
	case msg == "service address must not include a path":
		return "服务地址不能包含路径"
	case msg == "service address must not include a query or fragment":
		return "服务地址不能包含 query 或 fragment"
	default:
		return msg
	}
}

func validateInstallServerAddr(raw string) error {
	err := server.ValidateServerAddr(raw)
	if err == nil {
		return nil
	}
	return errors.New(chineseServerAddrError(err))
}

func chineseServerAddrError(err error) string {
	msg := err.Error()
	switch {
	case msg == "server_addr cannot be empty":
		return "Server 外部访问地址不能为空"
	case msg == "server_addr must be a complete http:// or https:// URL":
		return "Server 外部访问地址必须是完整的 http:// 或 https:// URL"
	case msg == "server_addr only supports http:// or https://":
		return "Server 外部访问地址只支持 http:// 或 https://"
	case msg == "server_addr must include a hostname":
		return "Server 外部访问地址必须包含 hostname"
	case msg == "server_addr cannot contain user info":
		return "Server 外部访问地址不能包含用户信息"
	case msg == "server_addr cannot contain query parameters or fragment":
		return "Server 外部访问地址不能包含 query 参数或 fragment"
	case msg == "server_addr cannot contain a path":
		return "Server 外部访问地址不能包含路径"
	case strings.HasPrefix(msg, "server_addr hostname is invalid"):
		return "Server 外部访问地址的 hostname 无效"
	case msg == "server_addr port is invalid":
		return "Server 外部访问地址端口无效"
	default:
		return msg
	}
}

func validateReadableCustomTLSFiles(certPath, keyPath, runUser string) error {
	if err := validateReadableCustomTLSFile(certPath, "certificate", runUser); err != nil {
		return err
	}
	return validateReadableCustomTLSFile(keyPath, "private key", runUser)
}

func validateReadableCustomTLSFile(path, label, runUser string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("TLS %s 文件无效：%w", label, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("TLS %s 路径必须是普通文件", label)
	}
	readable, err := isReadableByUser(info, runUser)
	if err != nil {
		return fmt.Errorf("无法验证 TLS %s 可读性：%w", label, err)
	}
	if !readable {
		return fmt.Errorf("TLS %s 文件必须可被 %s 读取", label, runUser)
	}
	return nil
}

func isReadableByUser(info os.FileInfo, username string) (bool, error) {
	account, err := user.Lookup(username)
	if err != nil {
		return false, err
	}
	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		return false, err
	}
	if uid == 0 {
		return true, nil
	}
	gids, err := account.GroupIds()
	if err != nil {
		return false, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false, fmt.Errorf("不支持的文件 stat 类型")
	}
	mode := info.Mode().Perm()
	if int(stat.Uid) == uid && mode&0o400 != 0 {
		return true, nil
	}
	for _, gid := range gids {
		parsed, err := strconv.Atoi(gid)
		if err != nil {
			continue
		}
		if int(stat.Gid) == parsed && mode&0o040 != 0 {
			return true, nil
		}
	}
	return mode&0o004 != 0, nil
}
