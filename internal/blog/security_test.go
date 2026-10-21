package blog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func accountRequest(t *testing.T, app *App, cookie *http.Cookie, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/account", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	app.Handler().ServeHTTP(response, request)
	return response
}

func cookieCSRF(t *testing.T, app *App, cookie *http.Cookie) string {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(cookie)
	claims, err := app.auth.Claims(request)
	if err != nil {
		t.Fatal(err)
	}
	return app.auth.CSRF(claims)
}

func TestPasswordByteLimitAndSessionRevocation(t *testing.T) {
	app := newTestApp(t)
	cookie, _ := loginTestUser(t, app, "admin", "test-password", "")
	otherCookie, _ := loginTestUser(t, app, "admin", "test-password", "")
	for _, password := range []string{strings.Repeat("a", 73), strings.Repeat("密", 25)} {
		form := url.Values{"csrf": {cookieCSRF(t, app, cookie)}, "action": {"password"}, "current_password": {"test-password"}, "new_password": {password}, "password_confirm": {password}}
		response := accountRequest(t, app, cookie, form)
		if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "72 字节") {
			t.Fatalf("long password status=%d body=%s", response.Code, response.Body.String())
		}
	}
	password := strings.Repeat("密", 24)
	form := url.Values{"csrf": {cookieCSRF(t, app, cookie)}, "action": {"password"}, "current_password": {"test-password"}, "new_password": {password}, "password_confirm": {password}}
	response := accountRequest(t, app, cookie, form)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("password status=%d body=%s", response.Code, response.Body.String())
	}
	var refreshed *http.Cookie
	for _, candidate := range response.Result().Cookies() {
		if candidate.Name == authCookieName {
			refreshed = candidate
		}
	}
	if refreshed == nil {
		t.Fatal("password change did not refresh the current session")
	}
	for _, test := range []struct {
		name   string
		cookie *http.Cookie
		want   int
	}{
		{"current old cookie", cookie, http.StatusSeeOther}, {"other device", otherCookie, http.StatusSeeOther}, {"refreshed session", refreshed, http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/account", nil)
			request.AddCookie(test.cookie)
			response := httptest.NewRecorder()
			app.Handler().ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("session status=%d want=%d", response.Code, test.want)
			}
		})
	}
	loginTestUser(t, app, "admin", password, "")
}

func TestRegistrationRejectsPasswordsOver72Bytes(t *testing.T) {
	app := newEmptyTestApp(t)
	for _, password := range []string{strings.Repeat("a", 73), strings.Repeat("密", 25)} {
		page := httptest.NewRecorder()
		app.Handler().ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/register", nil))
		form := url.Values{"csrf": {hiddenInputValue(t, page.Body.String(), "csrf")}, "username": {"owner"}, "nickname": {"Owner"}, "email": {"owner@example.com"}, "password": {password}, "password_confirm": {password}}
		request := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		for _, cookie := range page.Result().Cookies() {
			request.AddCookie(cookie)
		}
		response := httptest.NewRecorder()
		app.Handler().ServeHTTP(response, request)
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("registration status=%d body=%s", response.Code, response.Body.String())
		}
	}
	if count, err := app.repo.UserCount(); err != nil || count != 0 {
		t.Fatalf("invalid registration created user: count=%d err=%v", count, err)
	}
}

func TestCreatorIdentityDoesNotTransferAfterDeletion(t *testing.T) {
	app := newTestApp(t)
	owner, err := app.repo.UserByUsername("admin")
	if err != nil {
		t.Fatal(err)
	}
	reader := addTestUser(t, app, "reader", "reader-password")
	if _, err := app.database.Exec("DELETE FROM users WHERE id=?", owner.ID); err != nil {
		t.Fatal(err)
	}
	if err := app.database.Migrate(context.Background(), app.logger); err != nil {
		t.Fatal(err)
	}
	remaining, err := app.repo.UserByID(reader.ID)
	if err != nil || remaining.IsCreator {
		t.Fatalf("creator identity transferred: user=%#v err=%v", remaining, err)
	}
	newUser := addTestUser(t, app, "later", "later-password")
	if newUser.IsCreator {
		t.Fatal("new registration replaced the deleted creator")
	}
	if _, err := app.database.Exec("DELETE FROM users"); err != nil {
		t.Fatal(err)
	}
	if addTestUser(t, app, "after-empty", "long-password").IsCreator {
		t.Fatal("emptying users reset the creator identity")
	}
}

func TestConcurrentRegistrationsHaveOneCreator(t *testing.T) {
	app := newEmptyTestApp(t)
	hash, err := bcrypt.GenerateFromPassword([]byte("test-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan User, 6)
	errors := make(chan error, 6)
	for _, username := range []string{"one", "two", "three", "four", "five", "six"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			user, err := app.repo.CreateUser(username, string(hash), username, username+"@example.com")
			if err != nil {
				errors <- err
			} else {
				results <- user
			}
		}()
	}
	wg.Wait()
	close(results)
	close(errors)
	for err := range errors {
		t.Error(err)
	}
	creators := 0
	for user := range results {
		if user.IsCreator {
			creators++
		}
	}
	if creators != 1 {
		t.Fatalf("concurrent registrations created %d creators", creators)
	}
}
