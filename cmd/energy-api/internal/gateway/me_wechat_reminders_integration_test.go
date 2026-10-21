package gateway_test

import (
	"bytes"
	"context"
	"crypto"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/quanlaihe/hvac-web/services/notification-service/pkg/notificationservice"
)

// The mini program keeps count of the WeChat reminders a person allowed (#664,
// docs/specs/miniprogram-wechat.md): the Gateway adds them for the signed-in person in
// Notification Service, and removing WeChat clears them.

const meWeChatRemindersPath = "/api/v1/me/wechat/reminders"

// memoryReminders is Notification Service's reminder count, kept in memory.
type memoryReminders struct {
	mu     sync.Mutex
	counts map[notificationservice.WeChatPerson]int
}

func (memory *memoryReminders) ListInbox(context.Context, string, string, int) ([]notificationservice.InboxItem, error) {
	return nil, nil
}

func (memory *memoryReminders) MarkRead(context.Context, string, string, string, time.Time) (notificationservice.InboxItem, error) {
	return notificationservice.InboxItem{}, notificationservice.ErrNotFound
}

func (memory *memoryReminders) WeChatReminders(_ context.Context, person notificationservice.WeChatPerson) (notificationservice.WeChatReminders, error) {
	memory.mu.Lock()
	defer memory.mu.Unlock()
	return notificationservice.WeChatReminders{Alarm: memory.counts[person]}, nil
}

func (memory *memoryReminders) AddWeChatReminder(ctx context.Context, person notificationservice.WeChatPerson, _ notificationservice.ReminderKind, _ time.Time) (notificationservice.WeChatReminders, error) {
	memory.mu.Lock()
	memory.counts[person]++
	memory.mu.Unlock()
	return memory.WeChatReminders(ctx, person)
}

func (memory *memoryReminders) ClearWeChatReminders(_ context.Context, person notificationservice.WeChatPerson, _ time.Time) error {
	memory.mu.Lock()
	defer memory.mu.Unlock()
	delete(memory.counts, person)
	return nil
}

func (memory *memoryReminders) count(person notificationservice.WeChatPerson) int {
	memory.mu.Lock()
	defer memory.mu.Unlock()
	return memory.counts[person]
}

func remindersOf(t *testing.T, response *http.Response) int {
	t.Helper()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}
	var reminders struct {
		Alarm int `json:"alarm"`
	}
	if err := json.NewDecoder(response.Body).Decode(&reminders); err != nil {
		t.Fatal(err)
	}
	return reminders.Alarm
}

// newWeChatHarness serves Notification Service with its reminder count in memory behind the Gateway.
func newWeChatHarness(t *testing.T) (*authHarness, *memoryReminders) {
	t.Helper()
	reminders := &memoryReminders{counts: map[notificationservice.WeChatPerson]int{}}
	harness := newAuthHarnessWithNotification(t, func(gatewayKey crypto.PublicKey) http.Handler {
		handler, err := notificationservice.NewHTTPHandler(notificationservice.HTTPConfig{Store: reminders, Reminders: reminders, GatewayPublicKey: gatewayKey})
		if err != nil {
			t.Fatal(err)
		}
		return handler
	})
	return harness, reminders
}

func TestMiniProgramAddsWeChatRemindersForTheSignedInPerson(t *testing.T) {
	harness, reminders := newWeChatHarness(t)
	client := &http.Client{Transport: harness.transport}
	token := decodeAppSession(t, postAppJSON(t, client, harness.gatewayURL+"/api/v1/auth/app/sessions", map[string]string{"username": "fixture", "password": "correct-password"}), http.StatusCreated).SessionToken
	url := harness.gatewayURL + meWeChatRemindersPath
	person := notificationservice.WeChatPerson{TenantID: authFixtureTenantID, SubjectIssuer: harness.oidcServer.URL, Subject: "fixture-user"}

	if remindersOf(t, appRequest(t, client, http.MethodGet, url, token, "")) != 0 {
		t.Fatal("reminders before allowing any")
	}
	// The request cannot name the person.
	assertProblemCode(t, appRequest(t, client, http.MethodPost, url, token, `{"kind":"ALARM","subject":"fixture-admin"}`), http.StatusBadRequest, "REQUEST_INVALID")
	if remindersOf(t, appRequest(t, client, http.MethodPost, url, token, `{"kind":"ALARM"}`)) != 1 || reminders.count(person) != 1 {
		t.Fatal("the reminder was not added for the signed-in person")
	}

	// Removing WeChat clears them.
	harness.provider.BindWeChatForTest("fixture-user", "openid-duty")
	if response := appRequest(t, client, http.MethodDelete, harness.gatewayURL+meWeChatPath, token, ""); response.StatusCode != http.StatusNoContent {
		t.Fatalf("remove status = %d", response.StatusCode)
	}
	if reminders.count(person) != 0 {
		t.Fatal("removing WeChat kept the reminders")
	}

	// A Web Session has no WeChat to remind.
	browser := harness.browserClient(t)
	principal, _ := loginAndReadPrincipal(t, browser, harness.gatewayURL, "")
	request, err := http.NewRequest(http.MethodPost, url, bytes.NewReader([]byte(`{"kind":"ALARM"}`)))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", harness.gatewayURL)
	request.Header.Set("X-CSRF-Token", principal.Session.CSRFToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := browser.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	assertProblemCode(t, response, http.StatusForbidden, "WECHAT_MINI_PROGRAM_ONLY")
}
