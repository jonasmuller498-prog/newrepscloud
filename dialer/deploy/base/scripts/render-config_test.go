package main

import (
	"strings"
	"testing"
)

func setTrunkProfile(t *testing.T) {
	t.Setenv("DIALER_TRUNK_TRANSPORT", "udp")
	t.Setenv("DIALER_TRUNK_MEDIA_ENCRYPTION", "sdes")
}

func TestTrunkBlockUsesPrimaryThenSecondary(t *testing.T) {
	setTrunkProfile(t)
	t.Setenv("DIALER_TRUNK_AUTH_MODE", "ip")
	t.Setenv("DIALER_TRUNK_SIP_URI_PRIMARY", "sip:account@192.0.2.10:5060")
	t.Setenv("DIALER_TRUNK_SIP_URI_SECONDARY", "sip:account@192.0.2.11:5060")
	t.Setenv("TRUNK_SIGNAL_CIDR_PRIMARY", "192.0.2.10/32")
	t.Setenv("TRUNK_SIGNAL_CIDR_SECONDARY", "192.0.2.11/32")

	body := trunkBlock(true)
	required := []string{
		"[outbound-primary-aor]",
		"contact=sip:account@192.0.2.10:5060",
		"[outbound-secondary-aor]",
		"contact=sip:account@192.0.2.11:5060",
		"[outbound-primary](outbound-template)",
		"aors=outbound-primary-aor",
		"[outbound-secondary](outbound-template)",
		"aors=outbound-secondary-aor",
		"media_encryption=sdes",
		"qualify_2xx_only=yes",
	}
	for _, value := range required {
		if !strings.Contains(body, value) {
			t.Fatalf("rendered trunk is missing %q", value)
		}
	}
	if strings.Count(body, "max_contacts=1") != 2 {
		t.Fatal("each carrier AOR must be limited to one contact")
	}
	if strings.Count(body, "qualify_2xx_only=yes") != 2 {
		t.Fatal("both carrier contacts must require successful OPTIONS")
	}
	if strings.Contains(body, "aors=outbound-primary,outbound-secondary") {
		t.Fatal("both SBCs were assigned to one endpoint")
	}
	if strings.Index(body, "[outbound-primary-aor]") >
		strings.Index(body, "[outbound-secondary-aor]") {
		t.Fatal("secondary AOR precedes the primary AOR")
	}
}

func TestTrunkBlockRejectsDuplicateSBCs(t *testing.T) {
	setTrunkProfile(t)
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

func TestTrunkBlockRejectsMismatchedSBCNetwork(t *testing.T) {
	setTrunkProfile(t)
	t.Setenv("DIALER_TRUNK_AUTH_MODE", "ip")
	t.Setenv("DIALER_TRUNK_SIP_URI_PRIMARY", "sip:192.0.2.10:5060")
	t.Setenv("DIALER_TRUNK_SIP_URI_SECONDARY", "sip:192.0.2.12:5060")
	t.Setenv("TRUNK_SIGNAL_CIDR_PRIMARY", "192.0.2.10/32")
	t.Setenv("TRUNK_SIGNAL_CIDR_SECONDARY", "192.0.2.11/32")

	defer func() {
		if recover() == nil {
			t.Fatal("SBC URI outside its paired signaling /32 was accepted")
		}
	}()
	trunkBlock(true)
}

func TestTrunkBlockRejectsUnsupportedTransport(t *testing.T) {
	t.Setenv("DIALER_TRUNK_AUTH_MODE", "ip")
	t.Setenv("DIALER_TRUNK_TRANSPORT", "tls")
	t.Setenv("DIALER_TRUNK_MEDIA_ENCRYPTION", "sdes")

	defer func() {
		if recover() == nil {
			t.Fatal("unsupported TLS transport was accepted")
		}
	}()
	trunkBlock(true)
}

func TestTrunkBlockDisabledNeedsNoCarrierInput(t *testing.T) {
	if body := trunkBlock(false); !strings.Contains(body, "intentionally absent") {
		t.Fatalf("unexpected disabled trunk output: %q", body)
	}
}
