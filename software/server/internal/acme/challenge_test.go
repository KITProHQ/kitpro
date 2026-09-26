package acmelifecycle

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChallengeResponderRequiresExactHostPathAndToken(t *testing.T) {
	responder := NewChallengeResponder()
	token := "Abcdefghijklmnop_0123456789"
	value := token + ".thumbprint"
	if err := responder.Present("app.example.com", token, value); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://app.example.com"+ChallengePathPrefix+token, nil)
	recorder := httptest.NewRecorder()
	responder.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK || recorder.Body.String() != value || recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unexpected challenge response: %d %q", recorder.Code, recorder.Body.String())
	}
	for _, mutation := range []func(*http.Request){
		func(r *http.Request) { r.Host = "other.example.com" },
		func(r *http.Request) { r.Method = http.MethodPost },
		func(r *http.Request) { r.URL.Path += "/extra" },
	} {
		request = httptest.NewRequest(http.MethodGet, "http://app.example.com"+ChallengePathPrefix+token, nil)
		mutation(request)
		recorder = httptest.NewRecorder()
		responder.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("confused challenge request returned %d", recorder.Code)
		}
	}
	responder.Clear("app.example.com", token)
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "http://app.example.com"+ChallengePathPrefix+token, nil)
	responder.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatal("cleared challenge remained available")
	}
}

func TestChallengeResponderRejectsInjection(t *testing.T) {
	responder := NewChallengeResponder()
	for _, token := range []string{"short", "../../../../etc/passwd", strings.Repeat("a", 257), "line-break-invalid\n"} {
		if err := responder.Present("app.example.com", token, token+".thumbprint"); err == nil {
			t.Fatalf("accepted invalid token %q", token)
		}
	}
	if err := responder.Present("wrong host", "Abcdefghijklmnop", "Abcdefghijklmnop.thumbprint"); err == nil {
		t.Fatal("accepted invalid hostname")
	}
}
