// Vendored from ai-native-cli-spec template/go/aicli. Do not edit in tool repos; run scripts/sync-spec.js.
// SPDX-License-Identifier: MIT
// Copyright (c) 2026 Sean Guo

package aicli

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"strings"
	"time"
)

// Token format (CLI-055): "ct2_" + the 26-char base32 part of the operation id + "_"
// + 32 lowercase hex chars = the first 16 bytes of
// HMAC-SHA256(key, "aicli-confirm/2\x00" + canonical claims). Only this exact form is
// accepted; single use is keyed by the operation id, so no re-encoding can replay.

const (
	tokenPrefix   = "ct2_"
	tokenIDLen    = 26
	tokenMACLen   = 32
	tokenLen      = len(tokenPrefix) + tokenIDLen + 1 + tokenMACLen
	macDomain     = "aicli-confirm/2\x00"
	tokenLifetime = 10 * time.Minute
)

var idEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// claims are what a token binds (BLUEPRINT 5.3). They are stored in the operation
// record, never in the token; the token carries only their MAC.
type claims struct {
	V           int               `json:"v"`
	Tool        string            `json:"tool"`
	ToolVersion string            `json:"tool_version"`
	Op          string            `json:"op"`
	Cmd         string            `json:"cmd"`
	Inv         string            `json:"inv"`
	Inputs      map[string]string `json:"inputs"`
	Account     string            `json:"account"`
	Host        string            `json:"host"`
	Target      string            `json:"target"`
	Scope       string            `json:"scope"`
	Base        string            `json:"base"`
	Idem        string            `json:"idem"`
	Exp         string            `json:"exp"`
}

// canonical is the exact byte string the MAC covers.
func (c claims) canonical() ([]byte, error) {
	inputs := c.Inputs
	if inputs == nil {
		inputs = map[string]string{}
	}
	return canonicalJSON(map[string]any{
		"v": c.V, "tool": c.Tool, "tool_version": c.ToolVersion, "op": c.Op, "cmd": c.Cmd, "inv": c.Inv,
		"inputs": inputs, "account": c.Account, "host": c.Host, "target": c.Target, "scope": c.Scope,
		"base": c.Base, "idem": c.Idem, "exp": c.Exp,
	})
}

// newOperationID returns "op_" (or "opb_" for a batch) + 26 lowercase base32 chars of
// 16 random bytes.
func newOperationID(batch bool) (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", err
	}
	s := strings.ToLower(idEncoding.EncodeToString(buf[:]))
	if batch {
		return "opb_" + s, nil
	}
	return "op_" + s, nil
}

// idPart strips the op_/opb_ prefix.
func idPart(id string) string {
	if i := strings.IndexByte(id, '_'); i >= 0 {
		return id[i+1:]
	}
	return id
}

// validOperationID reports whether id has the exact op_/opb_ format.
func validOperationID(id string) bool {
	switch {
	case strings.HasPrefix(id, "op_"):
		return isIDPart(id[3:])
	case strings.HasPrefix(id, "opb_"):
		return isIDPart(id[4:])
	}
	return false
}

func isIDPart(s string) bool {
	if len(s) != tokenIDLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < '2' || c > '7') {
			return false
		}
	}
	return true
}

// mac computes the token MAC over the canonical claims.
func (c claims) mac(key []byte) ([]byte, error) {
	body, err := c.canonical()
	if err != nil {
		return nil, err
	}
	h := hmac.New(sha256.New, key)
	h.Write([]byte(macDomain))
	h.Write(body)
	return h.Sum(nil)[:tokenMACLen/2], nil
}

// mintToken issues the confirm token for claims.
func mintToken(key []byte, c claims) (string, error) {
	sum, err := c.mac(key)
	if err != nil {
		return "", err
	}
	return tokenPrefix + idPart(c.Op) + "_" + hex.EncodeToString(sum), nil
}

// parseToken accepts only the canonical token form and returns its id part and MAC.
// Upper case, padding, whitespace, extra or missing characters are all rejected.
func parseToken(s string) (part string, macHex string, ok bool) {
	if len(s) != tokenLen || !strings.HasPrefix(s, tokenPrefix) || s[len(tokenPrefix)+tokenIDLen] != '_' {
		return "", "", false
	}
	part = s[len(tokenPrefix) : len(tokenPrefix)+tokenIDLen]
	macHex = s[len(tokenPrefix)+tokenIDLen+1:]
	if !isIDPart(part) {
		return "", "", false
	}
	for i := 0; i < len(macHex); i++ {
		c := macHex[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", "", false
		}
	}
	return part, macHex, true
}

// verifyMAC checks a token MAC against stored claims in constant time.
func verifyMAC(key []byte, c claims, macHex string) bool {
	got, err := hex.DecodeString(macHex)
	if err != nil || len(got) != tokenMACLen/2 {
		return false
	}
	want, err := c.mac(key)
	if err != nil {
		return false
	}
	return hmac.Equal(got, want)
}
