package i2p

import (
	"encoding/binary"
	"strings"
	"testing"
)

func TestSAMReplyAndVersionParsing(t *testing.T) {
	reply := parseSAMReply(`HELLO REPLY RESULT=OK VERSION=3.3 MESSAGE="ready"`)
	if reply.topic != "HELLO" || reply.action != "REPLY" || reply.values["RESULT"] != "OK" || reply.values["VERSION"] != "3.3" || reply.values["MESSAGE"] != "ready" {
		t.Fatalf("SAM reply parsed incorrectly: %+v", reply)
	}
	for _, test := range []struct {
		version string
		want    bool
	}{{"3.0", false}, {"3.1", true}, {"3.3", true}, {"4.0", true}, {"invalid", false}, {"", false}} {
		if got := samVersionSupported(test.version); got != test.want {
			t.Errorf("samVersionSupported(%q) = %t, want %t", test.version, got, test.want)
		}
	}
}

func TestI2PBase64AndKnownDestinationAddress(t *testing.T) {
	raw := make([]byte, 391)
	for index := range raw {
		raw[index] = byte(index)
	}
	binary.BigEndian.PutUint16(raw[385:387], 4)
	encoded := i2pBase64.EncodeToString(raw)
	if strings.ContainsAny(encoded, "+/") {
		t.Fatalf("I2P base64 used standard alphabet: %q", encoded)
	}
	decoded, err := destinationBytes(encoded)
	if err != nil {
		t.Fatalf("decode I2P destination: %v", err)
	}
	if len(decoded) != 391 || decoded[385] != 0 || decoded[386] != 4 {
		t.Fatalf("destination length/certificate parsed incorrectly: %d", len(decoded))
	}
	if got, want := b32Address(decoded), "px5kjmz2jb3yvivyocs46xdsvjiinvzddhdabow6unog5s2nv6ga.b32.i2p"; got != want {
		t.Fatalf("b32Address() = %q, want %q", got, want)
	}
	for _, invalid := range []string{"", "not base64", strings.ReplaceAll(encoded, "-", "+")} {
		if _, err := destinationBytes(invalid); err == nil {
			t.Errorf("invalid I2P destination accepted: %q", invalid)
		}
	}
}
