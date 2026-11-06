// Package tvbox reads TVBox subscription documents and serves the sites this
// player can run. JAR and DEX entries are identified and left disabled.
package tvbox

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

var (
	errNotConfig = errors.New("没有可用的站点或直播列表")
	errDirectory = errors.New("这是订阅仓库（多仓），请填写其中某一个配置的地址")
	errHTML      = errors.New("订阅地址返回了网页，不是配置")
	errBadJSON   = errors.New("配置不是合法的 JSON")
	errEncrypted = errors.New("这份加密配置无法解密")
	errEnvelope  = errors.New("图片订阅里的配置无法解码")
)

// Decode turns a TVBox subscription body into JSON bytes.
// It accepts a UTF-8 BOM, // # and /* */ comments, trailing commas, raw
// control characters inside strings, a base64 document, a JPEG/PNG/GIF/WebP
// file with an eight-character marker and **base64 payload, and the common
// $#key#$ AES-CBC wrapper. A ** sequence inside an already valid JSON string
// is left untouched.
func Decode(data []byte) ([]byte, error) {
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, errBadJSON
	}
	if json.Valid(data) {
		return data, nil
	}
	if isHTML(data) {
		return nil, errHTML
	}
	plain, err := unwrap(data)
	if err != nil {
		return nil, err
	}
	plain = bytes.TrimPrefix(bytes.TrimSpace(plain), []byte{0xEF, 0xBB, 0xBF})
	if isHTML(plain) {
		return nil, errHTML
	}
	if json.Valid(plain) {
		return plain, nil
	}
	normalized, err := normalizeLoose(plain)
	if err != nil {
		return nil, err
	}
	normalized = escapeControls(normalized)
	if !json.Valid(normalized) {
		return nil, errBadJSON
	}
	return normalized, nil
}

func unwrap(data []byte) ([]byte, error) {
	if textConfigStart(data) {
		return data, nil
	}
	if payload, ok := envelopePayload(data); ok {
		return payload, nil
	}
	if looksLikeEnvelope(data) {
		return nil, errEnvelope
	}
	compact := stripSpace(data)
	if bytes.HasPrefix(bytes.ToLower(compact), []byte("2423")) {
		return decryptTVBox(string(compact))
	}
	if decoded, ok := decodeBase64Config(compact); ok {
		return decoded, nil
	}
	if embedded, ok := embeddedJSON(data); ok {
		return embedded, nil
	}
	return data, nil
}

func textConfigStart(data []byte) bool {
	b := firstByte(data)
	return b == '{' || b == '[' || b == '/' || b == '#'
}

func firstByte(data []byte) byte {
	for _, b := range data {
		if b != ' ' && b != '\t' && b != '\n' && b != '\r' {
			return b
		}
	}
	return 0
}

func isHTML(data []byte) bool {
	s := strings.ToLower(strings.TrimSpace(string(data[:min(len(data), 256)])))
	return strings.HasPrefix(s, "<!doctype html") || strings.HasPrefix(s, "<html")
}

func looksLikeEnvelope(data []byte) bool {
	if len(data) < 10 || textConfigStart(data) {
		return false
	}
	return bytes.Contains(data, []byte("**")) && (isImage(data) || bytes.Contains(data, []byte("**\n")) || bytes.Contains(data, []byte("**\r")))
}

func isImage(data []byte) bool {
	if bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}) || bytes.HasPrefix(data, []byte{0x89, 'P', 'N', 'G'}) {
		return true
	}
	if bytes.HasPrefix(data, []byte("GIF87a")) || bytes.HasPrefix(data, []byte("GIF89a")) {
		return true
	}
	return len(data) > 12 && bytes.Equal(data[0:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP"))
}

// envelopePayload finds the last eight-character marker + ** whose tail is
// base64 of a text configuration. Scanning from the end avoids JPEG bytes
// that happen to look like the marker.
func envelopePayload(data []byte) ([]byte, bool) {
	var starts []int
	for i := 8; i < len(data)-1; i++ {
		if data[i] != '*' || data[i+1] != '*' || !allAlphaNum(data[i-8:i]) {
			continue
		}
		starts = append(starts, i+2)
	}
	for i := len(starts) - 1; i >= 0; i-- {
		if decoded, ok := decodeBase64Config(stripSpace(data[starts[i]:])); ok {
			return decoded, true
		}
	}
	return nil, false
}

func allAlphaNum(b []byte) bool {
	if len(b) != 8 {
		return false
	}
	for _, c := range b {
		if !((c >= '0' && c <= '9') || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z')) {
			return false
		}
	}
	return true
}

func decodeBase64Config(compact []byte) ([]byte, bool) {
	if len(compact) < 16 || !isBase64Text(compact) {
		return nil, false
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		decoded, err := enc.DecodeString(string(compact))
		if err != nil {
			continue
		}
		decoded = bytes.TrimPrefix(bytes.TrimSpace(decoded), []byte{0xEF, 0xBB, 0xBF})
		if textConfigStart(decoded) || json.Valid(decoded) {
			return decoded, true
		}
	}
	return nil, false
}

func isBase64Text(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for _, c := range b {
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '+', c == '/', c == '=', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

func embeddedJSON(data []byte) ([]byte, bool) {
	if !isImage(data) {
		return nil, false
	}
	for _, marker := range [][]byte{[]byte(`{"sites"`), []byte(`{"spider"`), []byte(`{"lives"`), []byte("{\n")} {
		if i := bytes.LastIndex(data, marker); i > 0 && json.Valid(bytes.TrimSpace(data[i:])) {
			return bytes.TrimSpace(data[i:]), true
		}
	}
	return nil, false
}

func stripSpace(b []byte) []byte {
	return bytes.Map(func(r rune) rune {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, b)
}

// decryptTVBox opens the hex-encoded $#key#$ AES-128-CBC documents used by
// TVBox. The key and the 13-byte IV travel with the ciphertext; both are
// lowercased and padded with 0 to 16 bytes.
func decryptTVBox(compact string) ([]byte, error) {
	raw, err := hex.DecodeString(compact)
	if err != nil {
		return nil, errEncrypted
	}
	if len(raw) < 2+1+2+16+13 || raw[0] != '$' || raw[1] != '#' {
		return nil, errEncrypted
	}
	ivStart := len(raw) - 13
	keyEnd := -1
	for i := 2; i < ivStart-1; i++ {
		if raw[i] == '#' && raw[i+1] == '$' {
			keyEnd = i
			break
		}
	}
	if keyEnd < 2 {
		return nil, errEncrypted
	}
	key := padBlock(bytes.ToLower(raw[2:keyEnd]))
	iv := padBlock(bytes.ToLower(raw[ivStart:]))
	ct := raw[keyEnd+2 : ivStart]
	if key == nil || iv == nil || len(ct) == 0 || len(ct)%aes.BlockSize != 0 {
		return nil, errEncrypted
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errEncrypted
	}
	plain := make([]byte, len(ct))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(plain, ct)
	plain, err = pkcs7Unpad(plain)
	if err != nil {
		return nil, errEncrypted
	}
	return bytes.TrimPrefix(plain, []byte{0xEF, 0xBB, 0xBF}), nil
}

func padBlock(b []byte) []byte {
	if len(b) == 0 || len(b) > 16 {
		return nil
	}
	out := bytes.Repeat([]byte{'0'}, 16)
	copy(out, b)
	return out
}

func pkcs7Unpad(b []byte) ([]byte, error) {
	if len(b) == 0 || len(b)%aes.BlockSize != 0 {
		return nil, errEncrypted
	}
	n := int(b[len(b)-1])
	if n == 0 || n > aes.BlockSize || n > len(b) {
		return nil, errEncrypted
	}
	for _, c := range b[len(b)-n:] {
		if int(c) != n {
			return nil, errEncrypted
		}
	}
	return b[:len(b)-n], nil
}

// normalizeLoose blanks comments and trailing commas outside strings.
// A # or // outside a string runs to the end of the line. An unclosed block
// comment is rejected. Bytes inside strings, including // and **, stay put.
func normalizeLoose(in []byte) ([]byte, error) {
	out := append([]byte(nil), in...)
	quoted := false
	for i := 0; i < len(out); {
		if quoted {
			if out[i] == '\\' {
				i += 2
				continue
			}
			if out[i] == '"' {
				quoted = false
			}
			i++
			continue
		}
		if out[i] == '"' {
			quoted = true
			i++
			continue
		}
		next := byte(0)
		if i+1 < len(out) {
			next = out[i+1]
		}
		if out[i] == '#' || (out[i] == '/' && next == '/') {
			for i < len(out) && out[i] != '\n' && out[i] != '\r' {
				out[i] = ' '
				i++
			}
			continue
		}
		if out[i] == '/' && next == '*' {
			out[i], out[i+1] = ' ', ' '
			i += 2
			closed := false
			for i < len(out) {
				if out[i] == '*' && i+1 < len(out) && out[i+1] == '/' {
					out[i], out[i+1] = ' ', ' '
					i += 2
					closed = true
					break
				}
				if out[i] != '\n' && out[i] != '\r' {
					out[i] = ' '
				}
				i++
			}
			if !closed {
				return nil, errBadJSON
			}
			continue
		}
		i++
	}
	quoted = false
	for i := 0; i < len(out); i++ {
		if quoted {
			if out[i] == '\\' {
				i++
				continue
			}
			if out[i] == '"' {
				quoted = false
			}
			continue
		}
		if out[i] == '"' {
			quoted = true
			continue
		}
		if out[i] != ',' {
			continue
		}
		j := i + 1
		for j < len(out) && (out[j] == ' ' || out[j] == '\t' || out[j] == '\n' || out[j] == '\r') {
			j++
		}
		if j < len(out) && (out[j] == ']' || out[j] == '}') {
			out[i] = ' '
		}
	}
	return out, nil
}

// escapeControls turns raw control characters inside JSON strings into
// \u00XX escapes. Gson-based TVBox apps keep those characters; encoding/json
// does not.
func escapeControls(in []byte) []byte {
	var out []byte
	quoted := false
	for i := 0; i < len(in); i++ {
		b := in[i]
		if quoted && b == '\\' && i+1 < len(in) {
			out = append(out, b, in[i+1])
			i++
			continue
		}
		if b == '"' {
			quoted = !quoted
			out = append(out, b)
			continue
		}
		if quoted && b < 32 {
			out = append(out, '\\', 'u', '0', '0')
			const hexdigits = "0123456789abcdef"
			out = append(out, hexdigits[b>>4], hexdigits[b&0x0f])
			continue
		}
		out = append(out, b)
	}
	return out
}

func jsonObject(data []byte) (map[string]json.RawMessage, error) {
	dec := json.NewDecoder(bytes.NewReader(bytes.TrimSpace(data)))
	var obj map[string]json.RawMessage
	if err := dec.Decode(&obj); err != nil {
		return nil, errBadJSON
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errBadJSON
	}
	return obj, nil
}
