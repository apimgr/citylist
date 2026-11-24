package i2p

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/base32"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

var i2pBase64 = base64.NewEncoding("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-~")

type samReply struct {
	topic  string
	action string
	values map[string]string
}

func parseSAMReply(line string) samReply {
	line = strings.TrimSpace(line)
	reply := samReply{values: make(map[string]string)}
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return reply
	}
	reply.topic, reply.action = fields[0], fields[1]
	for _, field := range fields[2:] {
		key, value, ok := strings.Cut(field, "=")
		if ok {
			reply.values[key] = strings.Trim(value, `"`)
		}
	}
	return reply
}

func samCommand(conn net.Conn, reader *bufio.Reader, timeout time.Duration, command string) (samReply, error) {
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return samReply{}, fmt.Errorf("set SAM deadline: %w", err)
	}
	defer func() { _ = conn.SetDeadline(time.Time{}) }()
	if _, err := fmt.Fprintln(conn, command); err != nil {
		return samReply{}, fmt.Errorf("SAM write: %w", err)
	}
	line, err := reader.ReadString('\n')
	if err != nil {
		return samReply{}, fmt.Errorf("SAM read: %w", err)
	}
	return parseSAMReply(line), nil
}

func dialSAM(ctx context.Context, address string) (net.Conn, *bufio.Reader, error) {
	if _, _, err := net.SplitHostPort(address); err != nil {
		return nil, nil, errors.New("invalid SAM bridge address")
	}
	dialer := net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return nil, nil, err
	}
	stopOnCancel := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopOnCancel()
	reader := bufio.NewReader(conn)
	reply, err := samCommand(conn, reader, 10*time.Second, "HELLO VERSION MIN=3.1 MAX=3.3")
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	if reply.topic != "HELLO" || reply.action != "REPLY" || reply.values["RESULT"] != "OK" || !samVersionSupported(reply.values["VERSION"]) {
		_ = conn.Close()
		return nil, nil, errors.New("SAM bridge does not support version 3.1 or newer")
	}
	return conn, reader, nil
}

func samVersionSupported(version string) bool {
	parts := strings.SplitN(version, ".", 3)
	if len(parts) < 2 {
		return false
	}
	major, minor := 0, 0
	if _, err := fmt.Sscanf(parts[0], "%d", &major); err != nil {
		return false
	}
	if _, err := fmt.Sscanf(parts[1], "%d", &minor); err != nil {
		return false
	}
	return major > 3 || major == 3 && minor >= 1
}

func probeSAM(ctx context.Context, address string) error {
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, reader, err := dialSAM(probeCtx, address)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	reply, err := samCommand(conn, reader, 5*time.Second, "QUIT")
	if err != nil {
		return err
	}
	if reply.topic != "QUIT" || reply.action != "REPLY" || reply.values["RESULT"] != "OK" {
		return errors.New("SAM liveness probe failed")
	}
	return nil
}

func loadOrCreateDestination(_ context.Context, conn net.Conn, reader *bufio.Reader, keyPath string, signatureType int) (string, error) {
	priv, err := readDestination(keyPath)
	if err == nil {
		return priv, nil
	}
	if !errors.Is(err, errMissingDestination) {
		return "", err
	}
	reply, err := samCommand(conn, reader, 10*time.Second, fmt.Sprintf("DEST GENERATE SIGNATURE_TYPE=%d", signatureType))
	if err != nil {
		return "", fmt.Errorf("DEST GENERATE failed: %w", err)
	}
	if reply.topic != "DEST" || reply.action != "REPLY" {
		return "", errors.New("DEST GENERATE failed")
	}
	priv = reply.values["PRIV"]
	if _, err := destinationBytes(priv); err != nil {
		return "", errors.New("DEST GENERATE returned an invalid destination")
	}
	if err := writeDestinationAtomic(keyPath, priv); err != nil {
		return "", fmt.Errorf("persist destination key: %w", err)
	}
	return priv, nil
}

func destinationBytes(priv string) ([]byte, error) {
	if priv == "" || strings.ContainsAny(priv, "\r\n \t") {
		return nil, errors.New("invalid destination key")
	}
	raw, err := i2pBase64.DecodeString(priv)
	if err != nil || len(raw) < 387 {
		return nil, errors.New("invalid destination key")
	}
	length := 387 + int(binary.BigEndian.Uint16(raw[385:387]))
	if length > len(raw) {
		return nil, errors.New("truncated destination key")
	}
	return raw[:length], nil
}

func b32Address(destination []byte) string {
	hash := sha256.Sum256(destination)
	encoding := base32.StdEncoding.WithPadding(base32.NoPadding)
	return strings.ToLower(encoding.EncodeToString(hash[:])) + ".b32.i2p"
}
