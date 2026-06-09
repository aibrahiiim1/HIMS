package classify

import "testing"

func TestSwitchSubtype(t *testing.T) {
	cases := []struct {
		name    string
		sig     SwitchSignals
		want    string
		minConf int
	}{
		{"no inventory → unknown", SwitchSignals{PortsTotal: 0}, "unknown_switch", 1},
		{"leaf access (1 uplink)", SwitchSignals{PortsTotal: 48, PortsUp: 30, VLANs: 4, Uplinks: 1}, "access_switch", 70},
		{"standalone access (0 uplinks)", SwitchSignals{PortsTotal: 24, PortsUp: 10, VLANs: 2, Uplinks: 0}, "access_switch", 60},
		{"distribution (4 uplinks)", SwitchSignals{PortsTotal: 48, PortsUp: 40, VLANs: 12, Uplinks: 4}, "distribution_switch", 60},
		{"core (9 uplinks)", SwitchSignals{PortsTotal: 48, PortsUp: 45, VLANs: 20, Uplinks: 9}, "core_switch", 55},
	}
	for _, c := range cases {
		sub, conf, ev := SwitchSubtype(c.sig)
		if sub != c.want {
			t.Errorf("%s: subtype = %q; want %q", c.name, sub, c.want)
		}
		if conf < c.minConf {
			t.Errorf("%s: confidence = %d; want >= %d", c.name, conf, c.minConf)
		}
		if len(ev) == 0 {
			t.Errorf("%s: expected an evidence trail", c.name)
		}
		// The decisive evidence entry must name the chosen subtype.
		named := false
		for _, e := range ev {
			if e.Subtype == sub {
				named = true
			}
		}
		if !named {
			t.Errorf("%s: no evidence entry names subtype %q", c.name, sub)
		}
	}
}
