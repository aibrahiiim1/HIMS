package unifi

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type fakeDoer struct {
	loginCode int
	devices   string
	devCode   int
}

func (f fakeDoer) Do(req *http.Request) (*http.Response, error) {
	if strings.Contains(req.URL.Path, "/api/login") {
		code := f.loginCode
		if code == 0 {
			code = 200
		}
		return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader("{}")), Header: make(http.Header)}, nil
	}
	code := f.devCode
	if code == 0 {
		code = 200
	}
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(f.devices)), Header: make(http.Header)}, nil
}

const deviceJSON = `{"data":[
  {"type":"uap","name":"AP-Lobby","model":"U6-Lite","mac":"aa:bb:cc:00:11:22","ip":"10.0.0.21","state":1,"num_sta":14},
  {"type":"uap","name":"AP-Pool","model":"U6-Pro","mac":"aa:bb:cc:00:11:33","ip":"10.0.0.22","state":0,"num_sta":0},
  {"type":"usw","name":"Switch-1","model":"US-24","mac":"aa:bb:cc:00:99:99","state":1}
]}`

func TestListAPs_ParsesAndFiltersToUAP(t *testing.T) {
	c := NewClient("https://unifi", "", "admin", "pw", fakeDoer{devices: deviceJSON})
	if err := c.Login(context.Background()); err != nil {
		t.Fatal(err)
	}
	aps, err := c.ListAPs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(aps) != 2 { // the usw switch is filtered out
		t.Fatalf("got %d APs; want 2 (uap only)", len(aps))
	}
	if aps[0].Name != "AP-Lobby" || aps[0].Status != "online" || aps[0].ClientCount != 14 || aps[0].Model != "U6-Lite" {
		t.Fatalf("AP-Lobby wrong: %+v", aps[0])
	}
	if aps[1].Status != "offline" { // state 0
		t.Fatalf("AP-Pool should be offline: %+v", aps[1])
	}
}

func TestLogin_Non2xxFails(t *testing.T) {
	c := NewClient("https://unifi", "", "admin", "bad", fakeDoer{loginCode: 401})
	if err := c.Login(context.Background()); err == nil {
		t.Fatal("expected login failure on 401")
	}
}

func TestListAPs_DeviceErrorFails(t *testing.T) {
	c := NewClient("https://unifi", "", "admin", "pw", fakeDoer{devCode: 500})
	if _, err := c.ListAPs(context.Background()); err == nil {
		t.Fatal("expected error on 500")
	}
}
