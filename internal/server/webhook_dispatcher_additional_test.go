package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

type webhookRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn webhookRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestWebhookCompleteAttemptStatusMatrix(t *testing.T) {
	tests := []struct {
		name          string
		statusCode    int
		err           error
		retryAfter    time.Duration
		retryAfterSet bool
		wantStatus    WebhookDeliveryStatus
		wantDelay     time.Duration
		wantError     string
	}{
		{name: "200 succeeds", statusCode: 200, wantStatus: WebhookDeliverySuccess},
		{name: "299 succeeds", statusCode: 299, wantStatus: WebhookDeliverySuccess},
		{name: "400 is terminal", statusCode: 400, wantStatus: WebhookDeliveryFailed, wantError: "HTTP 400"},
		{name: "404 is terminal", statusCode: 404, wantStatus: WebhookDeliveryFailed, wantError: "HTTP 404"},
		{name: "408 retries", statusCode: 408, wantStatus: WebhookDeliveryRetrying, wantDelay: 5 * time.Second, wantError: "HTTP 408"},
		{name: "429 honors retry after", statusCode: 429, retryAfter: 45 * time.Second, retryAfterSet: true, wantStatus: WebhookDeliveryRetrying, wantDelay: 45 * time.Second, wantError: "HTTP 429"},
		{name: "500 retries", statusCode: 500, wantStatus: WebhookDeliveryRetrying, wantDelay: 5 * time.Second, wantError: "HTTP 500"},
		{name: "network error retries", err: errors.New("connection reset"), wantStatus: WebhookDeliveryRetrying, wantDelay: 5 * time.Second, wantError: "connection reset"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, webhookStore, owner := newWebhookStoreFixture(t)
			now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
			webhookStore.now = func() time.Time { return now }
			delivery, err := webhookStore.EnqueueTest(owner.ID, testWebhookInput("wh_status_matrix"), "client.online")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := webhookStore.db.Exec(`UPDATE activity_webhook_deliveries SET max_attempts = 3 WHERE id = ?`, delivery.ID); err != nil {
				t.Fatal(err)
			}
			claimed, ok, err := webhookStore.ClaimDue(owner.ID, now)
			if err != nil || !ok {
				t.Fatalf("claim delivery = %v, %v", ok, err)
			}
			completedAt := now.Add(time.Second)
			status, err := webhookStore.CompleteAttempt(claimed, webhookAttemptResult{
				CompletedAt: completedAt, Duration: 1250 * time.Millisecond, StatusCode: test.statusCode,
				Headers: map[string]string{"X-Result": "captured"}, Body: "response",
				RetryAfter: test.retryAfter, RetryAfterSet: test.retryAfterSet, Err: test.err,
			})
			if err != nil || status != test.wantStatus {
				t.Fatalf("complete status = %q, %v, want %q", status, err, test.wantStatus)
			}
			stored, err := webhookStore.GetDelivery(owner.ID, delivery.ID)
			if err != nil {
				t.Fatal(err)
			}
			if stored.Status != test.wantStatus || stored.Error != test.wantError || stored.DurationMS == nil || *stored.DurationMS != 1250 {
				t.Fatalf("stored result = %+v", stored)
			}
			if stored.ResponseHeaders["X-Result"] != "captured" || stored.ResponseBody != "response" || len(stored.Attempts) != 1 {
				t.Fatalf("stored response/attempt = %+v", stored)
			}
			if stored.Attempts[0].Status != map[bool]string{true: "success", false: "failed"}[test.wantStatus == WebhookDeliverySuccess] {
				t.Fatalf("attempt status = %+v", stored.Attempts[0])
			}
			if test.wantStatus == WebhookDeliveryRetrying {
				if stored.NextAttemptAt == nil {
					t.Fatal("retrying delivery has no next attempt")
				}
				next, parseErr := time.Parse(time.RFC3339Nano, *stored.NextAttemptAt)
				if parseErr != nil || next.Sub(completedAt) != test.wantDelay {
					t.Fatalf("next attempt = %v, %v, want delay %v", next, parseErr, test.wantDelay)
				}
			} else if stored.NextAttemptAt != nil {
				t.Fatalf("terminal delivery next attempt = %v", *stored.NextAttemptAt)
			}
		})
	}
}

func TestWebhookRetryExhaustionPersistsThreeAttemptsAndFinalFailure(t *testing.T) {
	_, webhookStore, owner := newWebhookStoreFixture(t)
	clock := time.Date(2026, 8, 26, 13, 0, 0, 0, time.UTC)
	webhookStore.now = func() time.Time { return clock }
	delivery, err := webhookStore.EnqueueTest(owner.ID, testWebhookInput("wh_retry_exhaustion"), "client.online")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := webhookStore.db.Exec(`UPDATE activity_webhook_deliveries SET max_attempts = 3 WHERE id = ?`, delivery.ID); err != nil {
		t.Fatal(err)
	}

	wantStatuses := []WebhookDeliveryStatus{WebhookDeliveryRetrying, WebhookDeliveryRetrying, WebhookDeliveryFailed}
	for index, wantStatus := range wantStatuses {
		claimed, ok, err := webhookStore.ClaimDue(owner.ID, clock)
		if err != nil || !ok || claimed.AttemptCount != index+1 {
			t.Fatalf("claim attempt %d = count %d ok %v err %v", index+1, claimed.AttemptCount, ok, err)
		}
		completedAt := clock.Add(time.Second)
		status, err := webhookStore.CompleteAttempt(claimed, webhookAttemptResult{CompletedAt: completedAt, StatusCode: http.StatusServiceUnavailable})
		if err != nil || status != wantStatus {
			t.Fatalf("complete attempt %d = %q, %v, want %q", index+1, status, err, wantStatus)
		}
		switch index {
		case 0:
			clock = completedAt.Add(5 * time.Second)
		case 1:
			clock = completedAt.Add(30 * time.Second)
		}
	}

	stored, err := webhookStore.GetDelivery(owner.ID, delivery.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != WebhookDeliveryFailed || stored.Error != "HTTP 503" || len(stored.Attempts) != 3 {
		t.Fatalf("exhausted delivery = %+v", stored)
	}
	for index, attempt := range stored.Attempts {
		if attempt.Number != index+1 || attempt.Status != "failed" || attempt.StatusCode == nil || *attempt.StatusCode != 503 {
			t.Fatalf("attempt %d = %+v", index+1, attempt)
		}
	}
}

func TestWebhookRetryAndRetryAfterHelpers(t *testing.T) {
	for _, test := range []struct {
		name       string
		statusCode int
		err        error
		want       bool
	}{
		{name: "network", err: errors.New("network"), want: true},
		{name: "zero without error", want: false},
		{name: "request timeout", statusCode: 408, want: true},
		{name: "too many requests", statusCode: 429, want: true},
		{name: "server error", statusCode: 599, want: true},
		{name: "redirect", statusCode: 302, want: false},
		{name: "client error", statusCode: 422, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := webhookRetryable(test.statusCode, test.err); got != test.want {
				t.Fatalf("webhookRetryable(%d, %v) = %v, want %v", test.statusCode, test.err, got, test.want)
			}
		})
	}
	if delay := webhookRetryDelay(1, 0, false); delay != 5*time.Second {
		t.Fatalf("first retry delay = %v", delay)
	}
	if delay := webhookRetryDelay(2, 0, false); delay != 30*time.Second {
		t.Fatalf("later retry delay = %v", delay)
	}
	if delay := webhookRetryDelay(1, 10*time.Minute, true); delay != webhookRetryAfterMax {
		t.Fatalf("Retry-After delay cap = %v", delay)
	}

	now := time.Date(2026, 8, 26, 13, 30, 0, 0, time.UTC)
	if delay, ok := parseWebhookRetryAfter(now.Add(90*time.Second).Format(http.TimeFormat), now); !ok || delay != 90*time.Second {
		t.Fatalf("HTTP-date Retry-After = %v, %v", delay, ok)
	}
	if delay, ok := parseWebhookRetryAfter(now.Add(-time.Minute).Format(http.TimeFormat), now); !ok || delay != 0 {
		t.Fatalf("past Retry-After = %v, %v", delay, ok)
	}
	if delay, ok := parseWebhookRetryAfter(now.Add(10*time.Minute).Format(http.TimeFormat), now); !ok || delay != webhookRetryAfterMax {
		t.Fatalf("capped HTTP-date Retry-After = %v, %v", delay, ok)
	}
}

func TestWebhookResponseCaptureBoundsBodyAndHeaders(t *testing.T) {
	if body, truncated, err := readWebhookResponseBody(nil); body != "" || truncated || err != nil {
		t.Fatalf("nil body = %q, %v, %v", body, truncated, err)
	}
	if body, truncated, err := readWebhookResponseBody(strings.NewReader("short")); body != "short" || truncated || err != nil {
		t.Fatalf("short body = %q, %v, %v", body, truncated, err)
	}
	large := strings.Repeat("x", webhookResponseBodyMaxBytes+1)
	if body, truncated, err := readWebhookResponseBody(strings.NewReader(large)); len(body) != webhookResponseBodyMaxBytes || !truncated || err != nil {
		t.Fatalf("large body length = %d, truncated %v, err %v", len(body), truncated, err)
	}
	if _, _, err := readWebhookResponseBody(errWebhookReader{}); err == nil {
		t.Fatal("body read error must surface")
	}

	headers := http.Header{"X-Multi": []string{"one", "two"}, "X-Other": []string{"value"}}
	captured := captureWebhookHeaders(headers, 1024)
	if captured["X-Multi"] != "one, two" || captured["X-Other"] != "value" {
		t.Fatalf("captured headers = %#v", captured)
	}
	if captured := captureWebhookHeaders(headers, 0); len(captured) != 0 {
		t.Fatalf("zero-byte captured headers = %#v", captured)
	}
}

type errWebhookReader struct{}

func (errWebhookReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

func TestWebhookDispatcherSendsPOSTAndGETRequestContracts(t *testing.T) {
	t.Run("POST", func(t *testing.T) {
		_, webhookStore, owner := newWebhookStoreFixture(t)
		type receivedRequest struct {
			method, path, query string
			headers             http.Header
			body                []byte
		}
		received := make(chan receivedRequest, 1)
		receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			body, _ := io.ReadAll(request.Body)
			received <- receivedRequest{method: request.Method, path: request.URL.Path, query: request.URL.RawQuery, headers: request.Header.Clone(), body: body}
			w.Header().Add("X-Receiver", "one")
			w.Header().Add("X-Receiver", "two")
			w.WriteHeader(http.StatusAccepted)
			_, _ = w.Write([]byte(`{"accepted":true}`))
		}))
		defer receiver.Close()

		input := testWebhookInput("wh_post_contract")
		input.Events = []string{"client.online"}
		input.URL = receiver.URL + "/hook?event={{event.name.en-US}}&delivery={{delivery.id}}"
		input.Headers = []WebhookHeader{{Key: "Content-Type", Value: "application/json"}, {Key: "X-Event-Name", Value: "{{event.name.en-US}}"}}
		delivery, err := webhookStore.EnqueueTest(owner.ID, input, "client.online")
		if err != nil {
			t.Fatal(err)
		}
		claimed, ok, err := webhookStore.ClaimDue(owner.ID, time.Now().UTC())
		if err != nil || !ok {
			t.Fatalf("claim POST delivery = %v, %v", ok, err)
		}
		dispatcher := newWebhookDispatcher(webhookStore, nil)
		defer dispatcher.cancel()
		dispatcher.execute(claimed)

		request := <-received
		if request.method != http.MethodPost || request.path != "/hook" || !strings.Contains(request.query, "event=Client%20online") || !strings.Contains(request.query, "delivery="+delivery.ID) {
			t.Fatalf("received POST target = %s %s?%s", request.method, request.path, request.query)
		}
		if request.headers.Get("X-NetsGo-Delivery") != delivery.ID || request.headers.Get("X-NetsGo-Event") == "" || request.headers.Get("X-NetsGo-Attempt") != "1" || request.headers.Get("X-Event-Name") != "Client online" {
			t.Fatalf("received POST headers = %#v", request.headers)
		}
		var body map[string]any
		if err := json.Unmarshal(request.body, &body); err != nil {
			t.Fatalf("decode POST body: %v\n%s", err, request.body)
		}
		if body["schema_version"] != float64(1) {
			t.Fatalf("POST body = %#v", body)
		}
		stored, err := webhookStore.GetDelivery(owner.ID, delivery.ID)
		if err != nil || stored.Status != WebhookDeliverySuccess || stored.StatusCode == nil || *stored.StatusCode != http.StatusAccepted || stored.ResponseBody != `{"accepted":true}` || stored.ResponseHeaders["X-Receiver"] != "one, two" {
			t.Fatalf("stored POST delivery = %+v, %v", stored, err)
		}
	})

	t.Run("GET", func(t *testing.T) {
		_, webhookStore, owner := newWebhookStoreFixture(t)
		bodyLength := make(chan int, 1)
		receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			body, _ := io.ReadAll(request.Body)
			bodyLength <- len(body)
			if request.Method != http.MethodGet {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}))
		defer receiver.Close()
		input := testWebhookInput("wh_get_contract")
		input.Method, input.URL, input.Body = WebhookMethodGET, receiver.URL+"/hook", "invalid JSON is ignored for GET"
		delivery, err := webhookStore.EnqueueTest(owner.ID, input, "client.online")
		if err != nil {
			t.Fatal(err)
		}
		claimed, ok, err := webhookStore.ClaimDue(owner.ID, time.Now().UTC())
		if err != nil || !ok || claimed.RequestBody != nil {
			t.Fatalf("claim GET delivery = body %v ok %v err %v", claimed.RequestBody, ok, err)
		}
		dispatcher := newWebhookDispatcher(webhookStore, nil)
		defer dispatcher.cancel()
		dispatcher.execute(claimed)
		if length := <-bodyLength; length != 0 {
			t.Fatalf("GET request body length = %d", length)
		}
		stored, err := webhookStore.GetDelivery(owner.ID, delivery.ID)
		if err != nil || stored.Status != WebhookDeliverySuccess || stored.RequestBody != nil {
			t.Fatalf("stored GET delivery = %+v, %v", stored, err)
		}
	})
}

func TestWebhookDispatcherHandlesNetworkErrorAndTimeout(t *testing.T) {
	t.Run("network error", func(t *testing.T) {
		_, webhookStore, owner := newWebhookStoreFixture(t)
		delivery, err := webhookStore.EnqueueTest(owner.ID, testWebhookInput("wh_network_error"), "client.online")
		if err != nil {
			t.Fatal(err)
		}
		claimed, ok, err := webhookStore.ClaimDue(owner.ID, time.Now().UTC())
		if err != nil || !ok {
			t.Fatalf("claim network delivery = %v, %v", ok, err)
		}
		dispatcher := newWebhookDispatcher(webhookStore, nil)
		defer dispatcher.cancel()
		dispatcher.client.Transport = webhookRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("dial refused")
		})
		dispatcher.execute(claimed)
		stored, err := webhookStore.GetDelivery(owner.ID, delivery.ID)
		if err != nil || stored.Status != WebhookDeliveryFailed || !strings.Contains(stored.Error, "dial refused") {
			t.Fatalf("network failure delivery = %+v, %v", stored, err)
		}
	})

	t.Run("client timeout", func(t *testing.T) {
		_, webhookStore, owner := newWebhookStoreFixture(t)
		receiver := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
			select {
			case <-request.Context().Done():
			case <-time.After(200 * time.Millisecond):
			}
		}))
		defer receiver.Close()
		input := testWebhookInput("wh_timeout")
		input.URL = receiver.URL
		delivery, err := webhookStore.EnqueueTest(owner.ID, input, "client.online")
		if err != nil {
			t.Fatal(err)
		}
		claimed, ok, err := webhookStore.ClaimDue(owner.ID, time.Now().UTC())
		if err != nil || !ok {
			t.Fatalf("claim timeout delivery = %v, %v", ok, err)
		}
		dispatcher := newWebhookDispatcher(webhookStore, nil)
		defer dispatcher.cancel()
		dispatcher.client.Timeout = 25 * time.Millisecond
		dispatcher.execute(claimed)
		stored, err := webhookStore.GetDelivery(owner.ID, delivery.ID)
		// The HTTP client can report either its timeout or the request context's
		// deadline, depending on which cancellation path wins.
		timeoutError := strings.Contains(strings.ToLower(stored.Error), "timeout") || strings.Contains(stored.Error, context.DeadlineExceeded.Error())
		if err != nil || stored.Status != WebhookDeliveryFailed || !timeoutError {
			t.Fatalf("timeout delivery = %+v, %v", stored, err)
		}
	})
}

func TestWebhookDispatcherLoopDeliversCommittedActivityEvent(t *testing.T) {
	adminStore, webhookStore, owner := newWebhookStoreFixture(t)
	client := registerWebhookClient(t, adminStore, owner.ID, "webhook-loop-client", "loop-client")
	type receivedRequest struct {
		headers http.Header
		body    []byte
	}
	received := make(chan receivedRequest, 1)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		received <- receivedRequest{headers: request.Header.Clone(), body: body}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()
	input := testWebhookInput("wh_loop_integration")
	input.Events = []string{"client.online"}
	input.URL = receiver.URL + "/activity"
	created, err := webhookStore.Create(owner.ID, input)
	if err != nil {
		t.Fatal(err)
	}

	dispatcher := newWebhookDispatcher(webhookStore, nil)
	dispatcher.Start()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := dispatcher.Stop(ctx); err != nil {
			t.Errorf("stop dispatcher: %v", err)
		}
	})
	eventID := appendWebhookClientEvent(t, adminStore, owner, client.ID, "online", "webhook-loop-event", time.Now().UTC())
	dispatcher.Wake()

	select {
	case request := <-received:
		if request.headers.Get("X-NetsGo-Event") != strconv.FormatInt(eventID, 10) || request.headers.Get("X-NetsGo-Attempt") != "1" {
			t.Fatalf("loop request headers = %#v", request.headers)
		}
		var body map[string]any
		err := json.Unmarshal(request.body, &body)
		event, _ := body["event"].(map[string]any)
		name, _ := event["name"].(map[string]any)
		if err != nil || name["en-US"] != "Client online" {
			t.Fatalf("loop request body = %#v, %v", body, err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for committed activity Webhook request")
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		page, err := webhookStore.ListDeliveries(owner.ID, created.ID, "", 10, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) == 1 && page.Items[0].Status == WebhookDeliverySuccess {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("delivery did not reach success: %+v", page.Items)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestWebhookDispatcherRestartRecoversLeaseThenRetries(t *testing.T) {
	adminStore, webhookStore, owner := newWebhookStoreFixture(t)
	clock := time.Date(2026, 8, 26, 14, 0, 0, 0, time.UTC)
	webhookStore.now = func() time.Time { return clock }
	client := registerWebhookClient(t, adminStore, owner.ID, "webhook-restart-client", "restart-client")
	attempts := make(chan string, 1)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		attempts <- request.Header.Get("X-NetsGo-Attempt")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()
	input := testWebhookInput("wh_restart_integration")
	input.Events = []string{"client.online"}
	input.URL = receiver.URL
	created, err := webhookStore.Create(owner.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	appendWebhookClientEvent(t, adminStore, owner, client.ID, "online", "webhook-restart-event", clock)
	claimed, ok, err := webhookStore.ClaimDue(owner.ID, clock)
	if err != nil || !ok || claimed.AttemptCount != 1 {
		t.Fatalf("initial claim = attempt %d ok %v err %v", claimed.AttemptCount, ok, err)
	}
	dbPath := adminStore.path
	if err := adminStore.Close(); err != nil {
		t.Fatalf("close store before restart: %v", err)
	}
	reopenedStore, err := NewAdminStore(dbPath)
	if err != nil {
		t.Fatalf("reopen store after restart: %v", err)
	}
	t.Cleanup(func() { _ = reopenedStore.Close() })
	webhookStore = newWebhookStoreWithDB(reopenedStore.db)
	webhookStore.settings = func() WebhookSettings {
		return WebhookSettings{AllowPrivateTargets: true, DailyDeliveryCap: defaultWebhookDailyDeliveryCap}
	}

	restartAt := clock.Add(webhookDeliveryLease + time.Second)
	dispatcher := newWebhookDispatcher(webhookStore, nil)
	dispatcher.now = func() time.Time { return restartAt }
	dispatcher.Start()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := dispatcher.Stop(ctx); err != nil {
			t.Errorf("stop restarted dispatcher: %v", err)
		}
	})
	dispatcher.Wake()

	select {
	case attempt := <-attempts:
		if attempt != "2" {
			t.Fatalf("recovered request attempt header = %q, want 2", attempt)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for recovered Webhook request")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		page, err := webhookStore.ListDeliveries(owner.ID, created.ID, "", 10, "")
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) == 1 && page.Items[0].Status == WebhookDeliverySuccess {
			if len(page.Items[0].Attempts) != 2 || page.Items[0].Attempts[0].Error != "server interrupted the request" || page.Items[0].Attempts[1].Status != "success" {
				t.Fatalf("recovered attempts = %+v", page.Items[0].Attempts)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("recovered delivery did not succeed: %+v", page.Items)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestWebhookEventHealthTracksRetryFailureAndRecovery(t *testing.T) {
	adminStore, webhookStore, owner := newWebhookStoreFixture(t)
	clock := time.Date(2026, 8, 26, 15, 0, 0, 0, time.UTC)
	webhookStore.now = func() time.Time { return clock }
	client := registerWebhookClient(t, adminStore, owner.ID, "webhook-health-client", "health-client")
	input := testWebhookInput("wh_health")
	input.Events = []string{"client.online"}
	created, err := webhookStore.Create(owner.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	appendWebhookClientEvent(t, adminStore, owner, client.ID, "online", "webhook-health-fail", clock)
	claimed, ok, err := webhookStore.ClaimDue(owner.ID, clock)
	if err != nil || !ok {
		t.Fatalf("claim health attempt 1 = %v, %v", ok, err)
	}
	completedAt := clock.Add(time.Second)
	if status, err := webhookStore.CompleteAttempt(claimed, webhookAttemptResult{CompletedAt: completedAt, StatusCode: 503}); err != nil || status != WebhookDeliveryRetrying {
		t.Fatalf("health retry = %s, %v", status, err)
	}
	health, err := webhookStore.Get(owner.ID, created.ID)
	if err != nil || health.LastStatus != "retrying" || health.ConsecutiveFailures != 0 {
		t.Fatalf("retrying health = %+v, %v", health, err)
	}
	clock = completedAt.Add(5 * time.Second)
	claimed, ok, err = webhookStore.ClaimDue(owner.ID, clock)
	if err != nil || !ok {
		t.Fatalf("claim health attempt 2 = %v, %v", ok, err)
	}
	if status, err := webhookStore.CompleteAttempt(claimed, webhookAttemptResult{CompletedAt: clock.Add(time.Second), StatusCode: 400}); err != nil || status != WebhookDeliveryFailed {
		t.Fatalf("health failure = %s, %v", status, err)
	}
	health, err = webhookStore.Get(owner.ID, created.ID)
	if err != nil || health.LastStatus != "failed" || health.ConsecutiveFailures != 1 {
		t.Fatalf("failed health = %+v, %v", health, err)
	}

	clock = clock.Add(3 * time.Second)
	appendWebhookClientEvent(t, adminStore, owner, client.ID, "online", "webhook-health-success", clock)
	claimed, ok, err = webhookStore.ClaimDue(owner.ID, clock)
	if err != nil || !ok {
		t.Fatalf("claim recovery event = %v, %v", ok, err)
	}
	if status, err := webhookStore.CompleteAttempt(claimed, webhookAttemptResult{CompletedAt: clock.Add(time.Second), StatusCode: 204}); err != nil || status != WebhookDeliverySuccess {
		t.Fatalf("health success = %s, %v", status, err)
	}
	health, err = webhookStore.Get(owner.ID, created.ID)
	if err != nil || health.LastStatus != "success" || health.ConsecutiveFailures != 0 || health.Calls24h != 2 {
		t.Fatalf("recovered health = %+v, %v", health, err)
	}
}

func TestWebhookCompleteAttemptRejectsDuplicateCompletion(t *testing.T) {
	_, webhookStore, owner := newWebhookStoreFixture(t)
	now := time.Now().UTC()
	webhookStore.now = func() time.Time { return now }
	_, err := webhookStore.EnqueueTest(owner.ID, testWebhookInput("wh_duplicate_complete"), "client.online")
	if err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := webhookStore.ClaimDue(owner.ID, now)
	if err != nil || !ok {
		t.Fatalf("claim delivery = %v, %v", ok, err)
	}
	result := webhookAttemptResult{CompletedAt: now.Add(time.Second), StatusCode: 204}
	if _, err := webhookStore.CompleteAttempt(claimed, result); err != nil {
		t.Fatal(err)
	}
	if _, err := webhookStore.CompleteAttempt(claimed, result); err == nil || !strings.Contains(err.Error(), "no longer pending") {
		t.Fatalf("duplicate completion error = %v", err)
	}
}
