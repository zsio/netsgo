// Package netidentity identifies a shared local network namespace when the OS
// provides evidence. An empty result means unknown, never a guessed match.
package netidentity

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"runtime"
	"strings"
	"sync"
)

// Current does not expose machine IDs or depend on hostnames/public IPs.
// Linux containers with separate network namespaces deliberately differ.
var current = sync.OnceValue(func() string {
	if runtime.GOOS != "linux" {
		return ""
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil || strings.TrimSpace(string(boot)) == "" {
		return ""
	}
	namespace, err := os.Readlink("/proc/self/ns/net")
	if err != nil || namespace == "" {
		return ""
	}
	digest := sha256.Sum256([]byte("netsgo-netns-v1\x00" + strings.TrimSpace(string(boot)) + "\x00" + namespace))
	return hex.EncodeToString(digest[:])
})

func Current() string { return current() }
