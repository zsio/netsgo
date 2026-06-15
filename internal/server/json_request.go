package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const jsonRequestBodyLimitBytes int64 = 128 << 10

var errJSONRequestBodyTooLarge = errors.New("json request body too large")

func decodeJSONRequestBody(r *http.Request, dst any) error {
	return decodeJSONRequestBodyWithPolicy(r, dst, false)
}

func decodeOptionalJSONRequestBody(r *http.Request, dst any) error {
	return decodeJSONRequestBodyWithPolicy(r, dst, true)
}

func decodeJSONRequestBodyWithPolicy(r *http.Request, dst any, allowEmpty bool) error {
	if r.Body == nil || r.Body == http.NoBody {
		if allowEmpty {
			return nil
		}
		return io.ErrUnexpectedEOF
	}
	defer func() {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()
	}()

	limited := io.LimitReader(r.Body, jsonRequestBodyLimitBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return err
	}
	if int64(len(body)) > jsonRequestBodyLimitBytes {
		return errJSONRequestBodyTooLarge
	}
	if allowEmpty && strings.TrimSpace(string(body)) == "" {
		return nil
	}

	decoder := json.NewDecoder(strings.NewReader(string(body)))
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("json request body must contain a single JSON value")
		}
		return err
	}
	return nil
}

func writeJSONRequestDecodeError(w http.ResponseWriter, err error) {
	if errors.Is(err, errJSONRequestBodyTooLarge) {
		writeAPIError(w, http.StatusRequestEntityTooLarge, "request_body_too_large", "request body too large")
		return
	}
	writeAPIError(w, http.StatusBadRequest, "invalid_request_body", "invalid request body")
}
