package aruba

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type fakeDoer struct {
	loginBody string
	loginCode int
	showBody  string
	showCode  int
}

func (f fakeDoer) Do(req *http.Request) (*http.Response, error) {
	body, code := f.showBody, f.showCode
	if strings.Contains(req.URL.Path, "/api/login") {
		body, code = f.loginBody, f.loginCode
	}
	if code == 0 {
		code = 200
	}
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
}

const loginOK = `{"_global_result":{"status":"0","status_str":"You've logged in successfully.","UIDARUBA":"tok-123"}}`

func TestLogin_CapturesToken(t *testing.T) {
	c := NewClient("https://aruba:4343", "admin", "pw", fakeDoer{loginBody: loginOK})
	if err := c.Login(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.token != "tok-123" {
		t.Fatalf("token not captured: %q", c.token)
	}
}

func TestLogin_RejectedStatus(t *testing.T) {
	if _, err := parseLogin([]byte(`{"_global_result":{"status":"1","status_str":"bad","UIDARUBA":""}}`)); err == nil {
		t.Fatal("expected rejection on non-zero status")
	}
}

// Fixture mirrors GET /v1/configuration/showcommand?command=show+ap+database+long.
const apDatabaseJSON = `{"AP Database":[
  {"Name":"AP-Lobby","Group":"default","AP Type":"535","IP Address":"10.0.7.21","Status":"Up 3d:4h:10m","Serial #":"CN0001"},
  {"Name":"AP-Roof","Group":"outdoor","AP Type":"577","IP Address":"10.0.7.22","Status":"Down","Serial #":"CN0002"}
]}`

func TestParseAPDatabase(t *testing.T) {
	aps, err := parseAPDatabase([]byte(apDatabaseJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(aps) != 2 {
		t.Fatalf("got %d APs; want 2", len(aps))
	}
	if aps[0].Name != "AP-Lobby" || aps[0].Status != "online" || aps[0].Model != "535" || aps[0].Serial != "CN0001" {
		t.Fatalf("AP-Lobby wrong: %+v", aps[0])
	}
	if aps[1].Status != "offline" {
		t.Fatalf("AP-Roof should be offline: %+v", aps[1])
	}
}

// Fixture mirrors GET /v1/configuration/showcommand?command=show+user-table.
const userTableJSON = `{"Users":[
  {"IP":"10.0.7.50","MAC":"de:ad:00:00:00:01","Name":"laptop","Role":"authenticated","AP name":"AP-Lobby","Essid/Bssid/Phy":"Corp/00:11:22:33:44:55/a-VHT-80"},
  {"IP":"10.0.7.51","MAC":"de:ad:00:00:00:02","Name":"phone","Role":"guest","AP name":"AP-Lobby","Essid/Bssid/Phy":"Guest/00:11:22:33:44:66/g-HT-20"}
]}`

func TestParseUserTable(t *testing.T) {
	cl, err := parseUserTable([]byte(userTableJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(cl) != 2 {
		t.Fatalf("got %d clients; want 2", len(cl))
	}
	if cl[0].Hostname != "laptop" || cl[0].SSID != "Corp" || cl[0].Band != "5" || cl[0].APName != "AP-Lobby" {
		t.Fatalf("client0 wrong: %+v", cl[0])
	}
	if cl[1].SSID != "Guest" || cl[1].Band != "2.4" {
		t.Fatalf("client1 wrong: %+v", cl[1])
	}
}

// Fixture mirrors GET showcommand?command=show+version (_data line array).
const showVersionJSON = `{"_data":[
  "Aruba Operating System Software.",
  "ArubaOS (MODEL: Aruba7210), Version 8.10.0.4",
  "Website: http://www.arubanetworks.com"
]}`

func TestParseVersion(t *testing.T) {
	if v := parseVersion([]byte(showVersionJSON)); v != "8.10.0.4" {
		t.Fatalf("version = %q; want 8.10.0.4", v)
	}
	if v := parseVersion([]byte(`{"_data":["no version here"]}`)); v != "" {
		t.Fatalf("no-version payload should yield empty, got %q", v)
	}
}

// Fixture mirrors GET showcommand?command=show+ap+bss-table.
const bssTableJSON = `{"Aruba AP BSS Table":[
  {"bss":"00:11:22:33:44:50","ess":"Corp","ap name":"AP-Lobby","ch":"6","phy":"g-HT-20"},
  {"bss":"00:11:22:33:44:51","ess":"Guest","ap name":"AP-Lobby","ch":"6","phy":"g-HT-20"},
  {"bss":"00:11:22:33:44:60","ess":"Corp","ap name":"AP-Lobby","ch":"44","phy":"a-VHT-80"}
]}`

func TestParseBSSTable(t *testing.T) {
	radios, err := parseBSSTable([]byte(bssTableJSON))
	if err != nil {
		t.Fatal(err)
	}
	// Lobby has 2 distinct radios (2.4 + 5), deduped across the 3 BSS rows.
	if len(radios) != 2 {
		t.Fatalf("got %d radios; want 2 (deduped per band)", len(radios))
	}
	if radios[0].APName != "AP-Lobby" || radios[0].Band != "2.4" || radios[0].Channel == nil || *radios[0].Channel != 6 {
		t.Fatalf("radio0 wrong: %+v", radios[0])
	}
	if radios[1].Band != "5" || radios[1].Channel == nil || *radios[1].Channel != 44 {
		t.Fatalf("radio1 wrong: %+v", radios[1])
	}
}

func TestListAPs_EndToEnd(t *testing.T) {
	c := NewClient("https://aruba:4343", "admin", "pw", fakeDoer{loginBody: loginOK, showBody: apDatabaseJSON})
	if err := c.Login(context.Background()); err != nil {
		t.Fatal(err)
	}
	aps, err := c.ListAPs(context.Background())
	if err != nil || len(aps) != 2 {
		t.Fatalf("ListAPs → %v, %v", aps, err)
	}
}
