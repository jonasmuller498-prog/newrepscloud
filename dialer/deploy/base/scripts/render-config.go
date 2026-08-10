package main

import (
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	hex64RE = regexp.MustCompile(`^[A-Fa-f0-9]{64}$`)
	nameRE  = regexp.MustCompile(`^[A-Za-z0-9_.-]{3,64}$`)
	uriRE   = regexp.MustCompile(`^sip:([A-Za-z0-9+_.%-]+@)?[A-Za-z0-9.-]+:[0-9]{2,5}$`)
	tokenRE = regexp.MustCompile(`@@[A-Z0-9_]+@@`)
)

func required(name string) string {
	value, ok := os.LookupEnv(name)
	if !ok || value == "" || strings.Contains(value, "REQUIRED_") {
		panic(name + " must be supplied with a non-placeholder value")
	}
	if strings.ContainsAny(value, "\r\n\x00") {
		panic(name + " contains a forbidden control character")
	}
	return value
}

func configValue(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, ";", `\;`)
}

func sipTarget(uri string) netip.Addr {
	if !uriRE.MatchString(uri) {
		panic("outbound SBC URIs must be exact sip:[user@]IPv4:port targets")
	}
	target := strings.TrimPrefix(uri, "sip:")
	if index := strings.LastIndex(target, "@"); index >= 0 {
		target = target[index+1:]
	}
	host, portText, err := net.SplitHostPort(target)
	if err != nil {
		panic("outbound SBC URIs must contain valid IPv4 targets and ports")
	}
	address, addressErr := netip.ParseAddr(host)
	port, portErr := strconv.Atoi(portText)
	if addressErr != nil || !address.Is4() || portErr != nil || port < 1 || port > 65535 {
		panic("outbound SBC URIs must contain valid IPv4 targets and ports")
	}
	return address
}

func signalTarget(name string) netip.Addr {
	prefix, err := netip.ParsePrefix(required(name))
	if err != nil || !prefix.Addr().Is4() || prefix.Bits() != 32 {
		panic(name + " must be an exact IPv4 /32")
	}
	return prefix.Addr()
}

func trunkBlock(enabled bool) string {
	if !enabled {
		return "; Trunk intentionally absent while DIALER_TRUNK_ENABLED=false"
	}
	mode := required("DIALER_TRUNK_AUTH_MODE")
	if mode != "digest" && mode != "ip" {
		panic("DIALER_TRUNK_AUTH_MODE must be exactly digest or ip")
	}
	uris := []string{
		required("DIALER_TRUNK_SIP_URI_PRIMARY"),
		required("DIALER_TRUNK_SIP_URI_SECONDARY"),
	}
	if uris[0] == uris[1] {
		panic("outbound SBC URIs must be distinct")
	}
	signals := []netip.Addr{
		signalTarget("TRUNK_SIGNAL_CIDR_PRIMARY"),
		signalTarget("TRUNK_SIGNAL_CIDR_SECONDARY"),
	}
	for index, uri := range uris {
		if sipTarget(uri) != signals[index] {
			panic("each outbound SBC URI must match its paired signaling /32")
		}
	}
	authLine, authSection := "", ""
	if mode == "digest" {
		user := configValue(required("DIALER_TRUNK_USERNAME"))
		password := configValue(required("DIALER_TRUNK_PASSWORD"))
		realm := configValue(required("DIALER_TRUNK_REALM"))
		authLine = "outbound_auth=outbound-auth\n"
		authSection = fmt.Sprintf(`
[outbound-auth]
type=auth
auth_type=userpass
username=%s
password=%s
realm=%s
`, user, password, realm)
	}
	return fmt.Sprintf(`[outbound-primary-aor]
type=aor
contact=%s
qualify_timeout=3.0
qualify_frequency=30
max_contacts=1

[outbound-secondary-aor]
type=aor
contact=%s
qualify_timeout=3.0
qualify_frequency=30
max_contacts=1

[outbound-template](!)
type=endpoint
transport=transport-udp
context=reject-inbound
disallow=all
allow=ulaw,alaw
%sdtmf_mode=rfc4733
direct_media=no
force_rport=yes
rewrite_contact=yes
rtp_symmetric=yes
media_encryption=no
send_pai=yes
send_rpid=no
trust_id_outbound=yes
timers=yes

[outbound-primary](outbound-template)
aors=outbound-primary-aor

[outbound-secondary](outbound-template)
aors=outbound-secondary-aor
%s`, configValue(uris[0]), configValue(uris[1]), authLine, authSection)
}

func main() {
	enabledText := os.Getenv("DIALER_TRUNK_ENABLED")
	if enabledText != "true" && enabledText != "false" {
		panic("DIALER_TRUNK_ENABLED must be exactly true or false")
	}
	ariUser := required("ARI_USER")
	if !nameRE.MatchString(ariUser) {
		panic("ARI_USER has unsupported characters")
	}
	ariPassword := required("ARI_PASSWORD")
	if !hex64RE.MatchString(ariPassword) {
		panic("ARI_PASSWORD must be a generated 64-character hex value")
	}
	replacements := map[string]string{
		"@@ARI_USER@@":     ariUser,
		"@@ARI_PASSWORD@@": ariPassword,
		"@@TRUNK_BLOCK@@":  trunkBlock(enabledText == "true"),
	}
	if err := os.MkdirAll("/rendered", 0750); err != nil {
		panic(err)
	}
	entries, err := os.ReadDir("/templates")
	if err != nil {
		panic(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".conf" {
			continue
		}
		input, err := os.ReadFile(filepath.Join("/templates", entry.Name()))
		if err != nil {
			panic(err)
		}
		output := string(input)
		for old, value := range replacements {
			output = strings.ReplaceAll(output, old, value)
		}
		if tokenRE.MatchString(output) {
			panic(entry.Name() + " contains an unresolved template token")
		}
		path := filepath.Join("/rendered", entry.Name())
		if err := os.WriteFile(path, []byte(output), 0440); err != nil {
			panic(err)
		}
	}
}
