package driver

import (
	"testing"

	"github.com/coralsearesorts/hims/internal/domain"
)

// stubDriver is a configurable test double.
type stubDriver struct {
	name string
	tmpl string
	fn   func(Probe) Match
}

func (s stubDriver) Name() string              { return s.name }
func (s stubDriver) Template() string          { return s.tmpl }
func (s stubDriver) Fingerprint(p Probe) Match { return s.fn(p) }

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()
	d := stubDriver{name: "x", tmpl: "switch", fn: func(Probe) Match { return NoMatch }}
	r.Register(d)
	if r.Get("x") == nil {
		t.Fatal("Get should return the registered driver")
	}
	if r.Get("missing") != nil {
		t.Fatal("Get should return nil for an unknown name")
	}
	if names := r.Names(); len(names) != 1 || names[0] != "x" {
		t.Fatalf("Names = %v, want [x]", names)
	}
}

func TestRegistry_DuplicatePanics(t *testing.T) {
	r := NewRegistry()
	d := stubDriver{name: "dup", fn: func(Probe) Match { return NoMatch }}
	r.Register(d)
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on duplicate registration")
		}
	}()
	r.Register(d)
}

func TestRegistry_BestPicksHighestConfidence(t *testing.T) {
	r := NewRegistry()
	r.Register(stubDriver{name: "weak", tmpl: "switch", fn: func(Probe) Match {
		return Match{Confidence: 40, Category: domain.CatSwitch}
	}})
	r.Register(stubDriver{name: "strong", tmpl: "switch", fn: func(Probe) Match {
		return Match{Confidence: 90, Category: domain.CatSwitch}
	}})
	r.Register(stubDriver{name: "none", fn: func(Probe) Match { return NoMatch }})

	d, m := r.Best(Probe{})
	if d == nil || d.Name() != "strong" {
		t.Fatalf("Best should pick 'strong', got %v", d)
	}
	if m.Confidence != 90 || m.Category != domain.CatSwitch {
		t.Fatalf("unexpected match: %+v", m)
	}
}

func TestRegistry_BestNoMatch(t *testing.T) {
	r := NewRegistry()
	r.Register(stubDriver{name: "none", fn: func(Probe) Match { return NoMatch }})
	d, m := r.Best(Probe{})
	if d != nil || m.Confidence != 0 {
		t.Fatalf("expected no match, got %v / %+v", d, m)
	}
}

func TestRegistry_BestTieBreaksByName(t *testing.T) {
	r := NewRegistry()
	r.Register(stubDriver{name: "bbb", fn: func(Probe) Match { return Match{Confidence: 50, Category: domain.CatSwitch} }})
	r.Register(stubDriver{name: "aaa", fn: func(Probe) Match { return Match{Confidence: 50, Category: domain.CatSwitch} }})
	d, _ := r.Best(Probe{})
	if d.Name() != "aaa" {
		t.Fatalf("tie should break to 'aaa', got %s", d.Name())
	}
}

func TestProbe_HasTCPPort(t *testing.T) {
	p := Probe{OpenTCPPorts: []int{22, 161, 443}}
	if !p.HasTCPPort(161) || p.HasTCPPort(80) {
		t.Fatal("HasTCPPort mismatch")
	}
}
