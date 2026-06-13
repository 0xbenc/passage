package totp

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultPeriod = 30
	DefaultDigits = 6
)

type Code struct {
	Value      string `json:"value"`
	Pretty     string `json:"pretty"`
	Period     int    `json:"period"`
	Remaining  int    `json:"remaining"`
	ValidUntil int64  `json:"valid_until"`
}

func FromPassContent(content []byte, now time.Time) (Code, error) {
	secret, err := SecretFromPassContent(content)
	if err != nil {
		return Code{}, err
	}
	return Generate(secret, now, DefaultPeriod, DefaultDigits)
}

func SecretFromPassContent(content []byte) (string, error) {
	text := strings.ReplaceAll(string(content), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	first := ""
	restHasContent := false
	for i, line := range lines {
		if i == 0 {
			first = line
			continue
		}
		if strings.TrimSpace(line) != "" {
			restHasContent = true
		}
	}
	if restHasContent {
		return "", errors.New("MFA entry must contain exactly one non-empty line")
	}
	secret := strings.Join(strings.Fields(first), "")
	if secret == "" {
		return "", errors.New("MFA secret is empty")
	}
	if strings.HasPrefix(strings.ToLower(secret), "otpauth://") {
		return "", errors.New("otpauth URIs are not supported; store the base32 secret only")
	}
	return secret, nil
}

func Generate(secret string, now time.Time, period int, digits int) (Code, error) {
	if period <= 0 {
		period = DefaultPeriod
	}
	if digits <= 0 {
		digits = DefaultDigits
	}
	key, err := decodeBase32(secret)
	if err != nil {
		return Code{}, err
	}
	nowUnix := now.Unix()
	counter := uint64(nowUnix / int64(period))
	value := hotp(key, counter, digits)
	remaining := period - int(nowUnix%int64(period))
	if remaining == 0 {
		remaining = period
	}
	return Code{
		Value:      value,
		Pretty:     Pretty(value),
		Period:     period,
		Remaining:  remaining,
		ValidUntil: nowUnix + int64(remaining),
	}, nil
}

func Pretty(value string) string {
	if len(value) <= 3 {
		return value
	}
	var b strings.Builder
	for i, r := range value {
		if i > 0 && i%3 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func hotp(key []byte, counter uint64, digits int) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	mac := hmac.New(sha1.New, key)
	_, _ = mac.Write(msg[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	bin := (uint32(sum[offset])&0x7f)<<24 |
		(uint32(sum[offset+1])&0xff)<<16 |
		(uint32(sum[offset+2])&0xff)<<8 |
		(uint32(sum[offset+3]) & 0xff)
	mod := uint32(math.Pow10(digits))
	code := bin % mod
	return fmt.Sprintf("%0"+strconv.Itoa(digits)+"d", code)
}

func decodeBase32(secret string) ([]byte, error) {
	normalized := strings.ToUpper(strings.Join(strings.Fields(secret), ""))
	normalized = strings.TrimRight(normalized, "=")
	if rem := len(normalized) % 8; rem != 0 {
		normalized += strings.Repeat("=", 8-rem)
	}
	decoded, err := base32.StdEncoding.DecodeString(normalized)
	if err != nil {
		return nil, fmt.Errorf("invalid base32 MFA secret: %w", err)
	}
	return decoded, nil
}
