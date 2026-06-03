package extreme

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type fakeDoer struct {
	login   string
	devices string
	code    int
}

func (f fakeDoer) Do(req *http.Request) (*http.Response, error) {
	code := f.code
	if code == 0 {
		code = 200
	}
	body := f.devices
	if strings.HasSuffix(req.URL.Path, "/login") {
		body = f.login
	}
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
}

const devicesJSON = `{"total_count":3,"data":[
  {"hostname":"AP-Lobby","device_function":"AP","product_type":"AP305C","mac_address":"AA:BB:CC:00:01","ip_address":"10.0.0.21","connected":true,"active_clients":7},
  {"hostname":"AP-Pool","device_function":"AP","product_type":"AP410C","mac_address":"AA:BB:CC:00:02","ip_address":"10.0.0.22","connected":false,"active_clients":0},
  {"hostname":"SW-Core","device_function":"SWITCH","product_type":"X440","mac_address":"AA:BB:CC:00:03","ip_address":"10.0.0.1","connected":true,"active_clients":0}
]}`

func TestLoginAndListAPs(t *testing.T) {
	c := NewClient("", "admin", "pw", fakeDoer{login: `{"access_token":"tok123"}`, devices: devicesJSON})
	if err := c.Login(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.token != "tok123" {
		t.Fatalf("token not stored: %q", c.token)
	}
	aps, err := c.ListAPs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Only the two AP-function devices, switch filtered out.
	if len(aps) != 2 {
		t.Fatalf("got %d APs; want 2", len(aps))
	}
	if aps[0].Name != "AP-Lobby" || aps[0].Status != "online" || aps[0].ClientCount != 7 || aps[0].Model != "AP305C" {
		t.Fatalf("ap[0] wrong: %+v", aps[0])
	}
	if aps[1].Status != "offline" {
		t.Fatalf("ap[1] should be offline: %+v", aps[1])
	}
}

func TestLogin_NoToken(t *testing.T) {
	c := NewClient("", "u", "p", fakeDoer{login: `{}`})
	if err := c.Login(context.Background()); err == nil {
		t.Fatal("login with no token should error")
	}
}

func TestListAPs_HTTPError(t *testing.T) {
	c := NewClient("", "u", "p", fakeDoer{devices: "{}", code: 500})
	if _, err := c.ListAPs(context.Background()); err == nil {
		t.Fatal("500 should error")
	}
}
