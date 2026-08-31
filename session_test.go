package main

import (
	"testing"

	"cha0s-sim/internal/proxy"
)

func TestValidateSessionName(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"chaos", true},
		{"security", true},
		{"unknown", false},
		{"", false},
		{"Chaos", false},
		{"chaos ", false},
	}
	for _, c := range cases {
		if got := validateSessionName(c.name); got != c.want {
			t.Errorf("validateSessionName(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestSessionPorts(t *testing.T) {
	if sessionPorts[sessionChaos] != 8081 {
		t.Errorf("chaos port = %d, want 8081", sessionPorts[sessionChaos])
	}
	if sessionPorts[sessionSecurity] != 8082 {
		t.Errorf("security port = %d, want 8082", sessionPorts[sessionSecurity])
	}
}

func TestModeForSession(t *testing.T) {
	cases := []struct {
		sn   sessionName
		want proxy.PipelineMode
	}{
		{sessionChaos, proxy.PipelineChaosOnly},
		{sessionSecurity, proxy.PipelineSecurityOnly},
		{sessionName("unknown"), proxy.PipelineFull},
	}
	for _, c := range cases {
		if got := modeForSession(c.sn); got != c.want {
			t.Errorf("modeForSession(%q) = %v, want %v", c.sn, got, c.want)
		}
	}
}
