package server

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type trackingJSONBody struct {
	data        string
	readOffset  int
	readCalls   int
	closed      bool
	readPastCap bool
}

func (b *trackingJSONBody) Read(p []byte) (int, error) {
	b.readCalls++
	if b.readOffset >= len(b.data) {
		return 0, io.EOF
	}
	n := copy(p, b.data[b.readOffset:])
	b.readOffset += n
	if b.readOffset > int(jsonRequestBodyLimitBytes)+1 {
		b.readPastCap = true
	}
	return n, nil
}

func (b *trackingJSONBody) Close() error {
	b.closed = true
	return nil
}

func TestDecodeJSONRequestBodyLimitAndTrailingToken(t *testing.T) {
	t.Run("accepts exactly 32KB", func(t *testing.T) {
		payload := `{"v":"` + strings.Repeat("a", int(jsonRequestBodyLimitBytes)-8) + `"}`
		if int64(len(payload)) != jsonRequestBodyLimitBytes {
			t.Fatalf("test payload length: got %d, want %d", len(payload), jsonRequestBodyLimitBytes)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/test", strings.NewReader(payload))
		var dst struct {
			V string `json:"v"`
		}
		if err := decodeJSONRequestBody(req, &dst); err != nil {
			t.Fatalf("exact-limit JSON should decode: %v", err)
		}
	})

	t.Run("rejects 32KB plus one", func(t *testing.T) {
		payload := `{"v":"` + strings.Repeat("a", int(jsonRequestBodyLimitBytes)-7) + `"}`
		if int64(len(payload)) != jsonRequestBodyLimitBytes+1 {
			t.Fatalf("test payload length: got %d, want %d", len(payload), jsonRequestBodyLimitBytes+1)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/test", strings.NewReader(payload))
		var dst map[string]string
		err := decodeJSONRequestBody(req, &dst)
		if !errors.Is(err, errJSONRequestBodyTooLarge) {
			t.Fatalf("oversize JSON should return errJSONRequestBodyTooLarge, got %v", err)
		}
	})

	t.Run("oversize body is closed without draining the tail", func(t *testing.T) {
		body := &trackingJSONBody{data: strings.Repeat("x", int(jsonRequestBodyLimitBytes)+32)}
		req := httptest.NewRequest(http.MethodPost, "/api/test", body)
		var dst map[string]string
		err := decodeJSONRequestBody(req, &dst)
		if !errors.Is(err, errJSONRequestBodyTooLarge) {
			t.Fatalf("oversize JSON should return errJSONRequestBodyTooLarge, got %v", err)
		}
		if !body.closed {
			t.Fatal("oversize body should still be closed")
		}
		if body.readPastCap {
			t.Fatalf("oversize body should not be drained after the cap, read offset=%d", body.readOffset)
		}
	})

	t.Run("rejects trailing token", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/test", strings.NewReader(`{"ok":true} {"extra":true}`))
		var dst map[string]bool
		if err := decodeJSONRequestBody(req, &dst); err == nil {
			t.Fatal("trailing JSON value should be rejected")
		}
	})

	t.Run("optional empty body remains allowed", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodDelete, "/api/test", http.NoBody)
		var dst map[string]string
		if err := decodeOptionalJSONRequestBody(req, &dst); err != nil {
			t.Fatalf("optional empty body should decode as zero value: %v", err)
		}
	})

	t.Run("passkey requests accept exactly 128KB", func(t *testing.T) {
		payload := `{"v":"` + strings.Repeat("a", int(passkeyJSONRequestBodyLimitBytes)-8) + `"}`
		if int64(len(payload)) != passkeyJSONRequestBodyLimitBytes {
			t.Fatalf("test payload length: got %d, want %d", len(payload), passkeyJSONRequestBodyLimitBytes)
		}
		req := httptest.NewRequest(http.MethodPost, "/api/auth/passkey/finish", strings.NewReader(payload))
		var dst struct {
			V string `json:"v"`
		}
		if err := decodePasskeyJSONRequestBody(req, &dst); err != nil {
			t.Fatalf("exact passkey-limit JSON should decode: %v", err)
		}
	})
}

func TestWriteJSONRequestDecodeErrorStatusCodes(t *testing.T) {
	w := httptest.NewRecorder()
	writeJSONRequestDecodeError(w, errJSONRequestBodyTooLarge)
	if w.Code != http.StatusRequestEntityTooLarge || !bytes.Contains(w.Body.Bytes(), []byte("request_body_too_large")) {
		t.Fatalf("oversize response: code=%d body=%s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	writeJSONRequestDecodeError(w, errors.New("malformed"))
	if w.Code != http.StatusBadRequest || !bytes.Contains(w.Body.Bytes(), []byte("invalid_request_body")) {
		t.Fatalf("malformed response: code=%d body=%s", w.Code, w.Body.String())
	}
}
