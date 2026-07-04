package classify

import (
	"testing"

	"github.com/coralsearesorts/hims/internal/domain"
)

func TestOUIClassify(t *testing.T) {
	cases := []struct {
		name string
		mac  string
		vend string
		cat  domain.DeviceCategory
	}{
		// Real observed devices in the 150.0.0.170-190 POS range.
		{"epson receipt printer (50:57:9C)", "50:57:9c:0a:2e:25", "Epson", domain.CatPrinter},
		{"posiflex POS terminal (00:19:17)", "00:19:17:05:5f:af", "Posiflex", domain.CatPOS},
		{"epson another OUI", "64:eb:8c:11:22:33", "Epson", domain.CatPrinter},
		// Dash-separated + bare-hex forms normalise the same.
		{"dash form", "00-19-17-05-5F-AF", "Posiflex", domain.CatPOS},
		// Multi-category / unknown vendors are intentionally NOT category-mapped, so a
		// real IP phone (Alcatel) or a random host keeps its port-based classification.
		{"alcatel phone (unmapped)", "00:80:9f:12:34:56", "", ""},
		{"unknown oui", "aa:bb:cc:dd:ee:ff", "", ""},
		{"empty", "", "", ""},
		{"too short", "00:19", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v, cat, conf := OUIClassify(c.mac)
			if v != c.vend || cat != c.cat {
				t.Fatalf("OUIClassify(%q) = (%q,%q), want (%q,%q)", c.mac, v, cat, c.vend, c.cat)
			}
			if c.cat != "" && conf <= 55 {
				t.Errorf("a mapped OUI must outrank the bare-5060 ip_phone guess (55); got conf %d", conf)
			}
			if c.cat != "" && conf >= 78 {
				t.Errorf("OUI confidence must stay below authenticated SNMP/driver (78); got %d", conf)
			}
		})
	}
}

// TestOUIClassify_NeverOverridesStrong documents the precedence contract the scan relies
// on: the OUI category confidence sits strictly between the weak port guesses (45-55) and
// an authenticated classification (>=78), so it corrects the former and never the latter.
func TestOUIClassify_NeverOverridesStrong(t *testing.T) {
	_, _, conf := OUIClassify("00:19:17:00:00:01") // Posiflex → pos
	if conf <= 55 || conf >= 78 {
		t.Fatalf("pos OUI confidence %d must be in (55,78)", conf)
	}
}
