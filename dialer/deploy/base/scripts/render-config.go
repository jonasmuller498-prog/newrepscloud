package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	e164RE  = regexp.MustCompile(`^\+1[0-9]{10}$`)
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

func trunkBlock(enabled bool, callerID string) string {
	if !enabled {
		return "; Trunk intentionally absent while DIALER_TRUNK_ENABLED=false"
	}
	mode := required("DIALER_TRUNK_AUTH_MODE")
	if mode != "digest" && mode != "ip" {
		panic("DIALER_TRUNK_AUTH_MODE must be exactly digest or ip")
	}
	uri := required("DIALER_TRUNK_SIP_URI")
	if !uriRE.MatchString(uri) || strings.Contains(uri, ".invalid:") {
		panic("DIALER_TRUNK_SIP_URI must be an exact sip:[user@]host:port target")
	}
	authLine, authSection := "", ""
	if mode == "digest" {
		user := configValue(required("DIALER_TRUNK_USERNAME"))
		password := configValue(required("DIALER_TRUNK_PASSWORD"))
		realm := configValue(required("DIALER_TRUNK_REALM"))
		authLine = "outbound_auth=voxbone-auth\n"
		authSection = fmt.Sprintf(`
[voxbone-auth]
type=auth
auth_type=userpass
username=%s
password=%s
realm=%s
`, user, password, realm)
	}
	return fmt.Sprintf(`[voxbone-aor]
type=aor
contact=%s
qualify_frequency=60
qualify_timeout=3.0
max_contacts=1

[voxbone]
type=endpoint
transport=transport-udp
context=reject-inbound
disallow=all
allow=ulaw
aors=voxbone-aor
%sdtmf_mode=rfc4733
direct_media=no
force_rport=yes
rewrite_contact=yes
rtp_symmetric=yes
media_encryption=no
send_pai=yes
send_rpid=no
trust_id_outbound=yes
from_user=%s
callerid=%s <%s>
timers=yes
%s`, configValue(uri), authLine, callerID, callerID, callerID, authSection)
}

func main() {
	enabledText := os.Getenv("DIALER_TRUNK_ENABLED")
	if enabledText != "true" && enabledText != "false" {
		panic("DIALER_TRUNK_ENABLED must be exactly true or false")
	}
	ariUser := required("ARI_USERNAME")
	if !nameRE.MatchString(ariUser) {
		panic("ARI_USERNAME has unsupported characters")
	}
	ariPassword := required("ARI_PASSWORD")
	if !hex64RE.MatchString(ariPassword) {
		panic("ARI_PASSWORD must be a generated 64-character hex value")
	}
	originToken := required("DIALER_ORIGIN_TOKEN")
	if !hex64RE.MatchString(originToken) {
		panic("DIALER_ORIGIN_TOKEN must be a generated 64-character hex value")
	}
	callerID := required("DIALER_CALLER_ID")
	if !e164RE.MatchString(callerID) {
		panic("DIALER_CALLER_ID must be US E.164 (+1 plus ten digits)")
	}
	replacements := map[string]string{
		"@@ARI_USERNAME@@":  ariUser,
		"@@ARI_PASSWORD@@":  ariPassword,
		"@@ORIGIN_TOKEN@@":  originToken,
		"@@CALLER_ID@@":     callerID,
		"@@TRUNK_ENABLED@@": enabledText,
		"@@TRUNK_BLOCK@@":   trunkBlock(enabledText == "true", callerID),
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
