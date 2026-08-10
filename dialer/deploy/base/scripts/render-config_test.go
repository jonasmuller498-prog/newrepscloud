package main

import (
	"strings"
	"testing"
)

func TestTrunkBlockUsesPrimaryThenSecondary(t *testing.T) {
	t.Setenv("DIALER_TRUNK_AUTH_MODE", "ip")
	t.Setenv("DIALER_TRUNK_SIP_URI_PRIMARY", "sip:account@sbc-a.example.net:5060")
	t.Setenv("DIALER_TRUNK_SIP_URI_SECONDARY", "sip:account@sbc-b.example.net:5060")

	body := trunkBlock(true)
	required := []string{
		"[outbound-primary]",
		"contact=sip:account@sbc-a.example.net:5060",
		"[outbound-secondary]",
		"contact=sip:account@sbc-b.example.net:5060",
		"aors=outbound-primary,outbound-secondary",
	}
	for _, value := range required {
		if !strings.Contains(body, value) {
			t.Fatalf("rendered trunk is missing %q", value)
		}
	}
	if strings.Count(body, "max_contacts=1") != 2 {
		t.Fatal("each carrier AOR must be limited to one contact")
	}
	if strings.Index(body, "[outbound-primary]") > strings.Index(body, "[outbound-secondary]") {
		t.Fatal("secondary AOR precedes the primary AOR")
	}
}

func TestTrunkBlockRejectsDuplicateSBCs(t *testing.T) {
	t.Setenv("DIALER_TRUNK_AUTH_MODE", "ip")
	t.Setenv("DIALER_TRUNK_SIP_URI_PRIMARY", "sip:sbc.example.net:5060")
	t.Setenv("DIALER_TRUNK_SIP_URI_SECONDARY", "sip:sbc.example.net:5060")

	defer func() {
		if recover() == nil {
			t.Fatal("duplicate SBC URIs were accepted")
		}
	}()
	trunkBlock(true)
}

func TestTrunkBlockDisabledNeedsNoCarrierInput(t *testing.T) {
	if body := trunkBlock(false); !strings.Contains(body, "intentionally absent") {
		t.Fatalf("unexpected disabled trunk output: %q", body)
	}
}
