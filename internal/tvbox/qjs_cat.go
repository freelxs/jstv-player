package tvbox

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"hash"
	"strings"
)

func builtinModuleSpec(spec string) (string, bool) {
	s := strings.TrimSpace(strings.ToLower(spec))
	switch {
	case strings.HasPrefix(s, "assets://") && (strings.HasSuffix(s, "/cat.js") || s == "assets://js/lib/cat.js"):
		return "__builtin_cat", true
	default:
		return "", false
	}
}

func cryptoHashHost(algo, dataHex, keyHex string) string {
	data, err := hex.DecodeString(dataHex)
	if err != nil {
		data = []byte(dataHex)
	}
	newHash := func(name string) func() hash.Hash {
		switch name {
		case "md5":
			return md5.New
		case "sha1":
			return sha1.New
		case "sha256":
			return sha256.New
		default:
			return nil
		}
	}
	lower := strings.ToLower(strings.TrimSpace(algo))
	if strings.HasPrefix(lower, "hmac-") {
		fn := newHash(strings.TrimPrefix(lower, "hmac-"))
		if fn == nil {
			return ""
		}
		key, err := hex.DecodeString(keyHex)
		if err != nil {
			key = []byte(keyHex)
		}
		mac := hmac.New(fn, key)
		mac.Write(data)
		return hex.EncodeToString(mac.Sum(nil))
	}
	fn := newHash(lower)
	if fn == nil {
		return ""
	}
	h := fn()
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

func cryptoAESHost(op, dataHex, keyHex, ivHex, mode, padding string) string {
	data, err := hex.DecodeString(dataHex)
	if err != nil {
		return ""
	}
	key, err := hex.DecodeString(keyHex)
	if err != nil {
		return ""
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return ""
	}
	bs := block.BlockSize()
	iv, _ := hex.DecodeString(ivHex)
	if len(iv) < bs {
		padded := make([]byte, bs)
		copy(padded, iv)
		iv = padded
	} else if len(iv) > bs {
		iv = iv[:bs]
	}
	modeUpper := strings.ToUpper(strings.TrimSpace(mode))
	padLower := strings.ToLower(strings.TrimSpace(padding))

	if strings.EqualFold(op, "encrypt") {
		switch padLower {
		case "nopadding":
			if len(data)%bs != 0 {
				return ""
			}
		case "zeropadding":
			if rem := len(data) % bs; rem != 0 {
				data = append(data, make([]byte, bs-rem)...)
			}
		default:
			padLen := bs - (len(data) % bs)
			data = append(data, bytes.Repeat([]byte{byte(padLen)}, padLen)...)
		}
		out := make([]byte, len(data))
		if modeUpper == "ECB" {
			for i := 0; i < len(data); i += bs {
				block.Encrypt(out[i:i+bs], data[i:i+bs])
			}
		} else {
			cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, data)
		}
		return hex.EncodeToString(out)
	}

	if len(data) == 0 || len(data)%bs != 0 {
		return ""
	}
	out := make([]byte, len(data))
	if modeUpper == "ECB" {
		for i := 0; i < len(data); i += bs {
			block.Decrypt(out[i:i+bs], data[i:i+bs])
		}
	} else {
		cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, data)
	}
	switch padLower {
	case "nopadding":
	case "zeropadding":
		out = bytes.TrimRight(out, "\x00")
	default:
		if len(out) > 0 {
			padLen := int(out[len(out)-1])
			if padLen > 0 && padLen <= bs && padLen <= len(out) {
				valid := true
				for i := len(out) - padLen; i < len(out); i++ {
					if int(out[i]) != padLen {
						valid = false
						break
					}
				}
				if valid {
					out = out[:len(out)-padLen]
				}
			}
		}
	}
	return hex.EncodeToString(out)
}

func b64EncodeHexHost(dataHex string) string {
	data, err := hex.DecodeString(dataHex)
	if err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(data)
}

func b64DecodeHexHost(b64 string) string {
	s := strings.TrimSpace(b64)
	data, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		data, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(s, "="))
		if err != nil {
			return ""
		}
	}
	return hex.EncodeToString(data)
}

const qjsCatShim = `
(() => {
  if (globalThis.__builtin_cat) return;

  function utf8ToHex(str) {
    const s = String(str ?? '');
    const bytes = [];
    for (let i = 0; i < s.length; i++) {
      let c = s.charCodeAt(i);
      if (c >= 0xd800 && c <= 0xdbff && i + 1 < s.length) {
        const lo = s.charCodeAt(++i);
        c = ((c - 0xd800) << 10) + (lo - 0xdc00) + 0x10000;
      }
      if (c < 0x80) bytes.push(c);
      else if (c < 0x800) bytes.push(0xc0 | (c >> 6), 0x80 | (c & 0x3f));
      else if (c < 0x10000) bytes.push(0xe0 | (c >> 12), 0x80 | ((c >> 6) & 0x3f), 0x80 | (c & 0x3f));
      else bytes.push(0xf0 | (c >> 18), 0x80 | ((c >> 12) & 0x3f), 0x80 | ((c >> 6) & 0x3f), 0x80 | (c & 0x3f));
    }
    return bytes.map(b => b.toString(16).padStart(2, '0')).join('');
  }

  function hexToUtf8(hex) {
    if (!hex) return '';
    let out = '';
    const bytes = [];
    for (let i = 0; i + 1 < hex.length; i += 2) bytes.push(parseInt(hex.slice(i, i + 2), 16));
    let i = 0;
    while (i < bytes.length) {
      const b = bytes[i++];
      if (b < 0x80) out += String.fromCharCode(b);
      else if ((b & 0xe0) === 0xc0 && i < bytes.length) out += String.fromCharCode(((b & 0x1f) << 6) | (bytes[i++] & 0x3f));
      else if ((b & 0xf0) === 0xe0 && i + 1 < bytes.length) out += String.fromCharCode(((b & 0x0f) << 12) | ((bytes[i++] & 0x3f) << 6) | (bytes[i++] & 0x3f));
      else if ((b & 0xf8) === 0xf0 && i + 2 < bytes.length) {
        let cp = ((b & 0x07) << 18) | ((bytes[i++] & 0x3f) << 12) | ((bytes[i++] & 0x3f) << 6) | (bytes[i++] & 0x3f);
        cp -= 0x10000;
        out += String.fromCharCode(0xd800 + (cp >> 10), 0xdc00 + (cp & 0x3ff));
      }
    }
    return out;
  }

  function latin1ToHex(str) {
    const s = String(str ?? '');
    let out = '';
    for (let i = 0; i < s.length; i++) out += (s.charCodeAt(i) & 0xff).toString(16).padStart(2, '0');
    return out;
  }

  function hexToLatin1(hex) {
    let out = '';
    for (let i = 0; i + 1 < hex.length; i += 2) out += String.fromCharCode(parseInt(hex.slice(i, i + 2), 16));
    return out;
  }

  class WordArray {
    constructor(hex) { this._hex = String(hex || ''); }
    toString(encoder) {
      if (!encoder || encoder === enc.Hex) return this._hex;
      return encoder.stringify(this);
    }
  }

  const enc = {
    Utf8: { parse: (s) => new WordArray(utf8ToHex(s)), stringify: (wa) => hexToUtf8(wa._hex) },
    Hex: { parse: (s) => new WordArray(String(s || '').replace(/[^0-9a-fA-F]/g, '').toLowerCase()), stringify: (wa) => wa._hex },
    Base64: { parse: (s) => new WordArray(__b64DecodeHex(String(s || ''))), stringify: (wa) => __b64EncodeHex(wa._hex) },
    Latin1: { parse: (s) => new WordArray(latin1ToHex(s)), stringify: (wa) => hexToLatin1(wa._hex) },
  };

  function toHex(val) {
    if (val instanceof WordArray) return val._hex;
    if (val && typeof val === 'object' && typeof val._hex === 'string') return val._hex;
    return utf8ToHex(val);
  }

  const Crypto = {
    enc,
    mode: { CBC: 'CBC', ECB: 'ECB' },
    pad: { Pkcs7: 'Pkcs7', ZeroPadding: 'ZeroPadding', NoPadding: 'NoPadding' },
    MD5: (s) => new WordArray(__cryptoHash('md5', toHex(s), '')),
    SHA1: (s) => new WordArray(__cryptoHash('sha1', toHex(s), '')),
    SHA256: (s) => new WordArray(__cryptoHash('sha256', toHex(s), '')),
    HmacMD5: (s, k) => new WordArray(__cryptoHash('hmac-md5', toHex(s), toHex(k))),
    HmacSHA1: (s, k) => new WordArray(__cryptoHash('hmac-sha1', toHex(s), toHex(k))),
    HmacSHA256: (s, k) => new WordArray(__cryptoHash('hmac-sha256', toHex(s), toHex(k))),
    AES: {
      encrypt(msg, key, cfg) {
        cfg = cfg || {};
        const hex = __cryptoAES('encrypt', toHex(msg), toHex(key), cfg.iv ? toHex(cfg.iv) : '', cfg.mode || 'CBC', cfg.padding || 'Pkcs7');
        const ct = new WordArray(hex);
        return { ciphertext: ct, toString(fmt) { return fmt ? fmt.stringify(ct) : enc.Base64.stringify(ct); } };
      },
      decrypt(cipherInput, key, cfg) {
        cfg = cfg || {};
        let cipherHex = '';
        if (typeof cipherInput === 'string') cipherHex = __b64DecodeHex(cipherInput);
        else if (cipherInput && cipherInput.ciphertext) cipherHex = toHex(cipherInput.ciphertext);
        else cipherHex = toHex(cipherInput);
        const hex = __cryptoAES('decrypt', cipherHex, toHex(key), cfg.iv ? toHex(cfg.iv) : '', cfg.mode || 'CBC', cfg.padding || 'Pkcs7');
        return new WordArray(hex);
      },
    },
  };

  function wrapSelection(items) {
    const arr = Array.isArray(items) ? items : [];
    const sel = {
      length: arr.length,
      get(i) { return i === undefined ? arr.slice() : arr[i < 0 ? arr.length + i : i]; },
      toArray() { return arr.slice(); },
      eq(i) { const idx = i < 0 ? arr.length + i : i; return wrapSelection(idx >= 0 && idx < arr.length ? [arr[idx]] : []); },
      first() { return wrapSelection(arr.length ? [arr[0]] : []); },
      last() { return wrapSelection(arr.length ? [arr[arr.length - 1]] : []); },
      find(q) {
        const out = [];
        for (const h of arr) {
          const found = pdfa(h, q);
          for (const f of found) out.push(f);
        }
        return wrapSelection(out);
      },
      text() {
        return arr.map(h => pdfh(h, 'body&&Text') || pdfh(h, '*&&Text')).join('').trim();
      },
      html() { return arr.length ? arr[0] : ''; },
      attr(name) {
        if (!arr.length || !name) return undefined;
        const v = pdfh(arr[0], '*&&' + name);
        return v === '' ? undefined : v;
      },
      each(fn) {
        for (let i = 0; i < arr.length; i++) {
          if (fn.call(arr[i], i, arr[i]) === false) break;
        }
        return sel;
      },
      map(fn) {
        const mapped = [];
        for (let i = 0; i < arr.length; i++) {
          const r = fn.call(arr[i], i, arr[i]);
          if (r !== null && r !== undefined) mapped.push(r);
        }
        return wrapSelection(mapped);
      },
    };
    for (let i = 0; i < arr.length; i++) sel[i] = arr[i];
    return sel;
  }

  function load(html) {
    const doc = String(html || '');
    function $(q) {
      if (typeof q === 'string') {
        const t = q.trim();
        if (t.startsWith('<')) return wrapSelection([t]);
        return wrapSelection(pdfa(doc, t));
      }
      if (q && typeof q === 'object' && typeof q.html === 'function') return q;
      return wrapSelection([]);
    }
    $.html = (el) => (el == null ? doc : (typeof el === 'string' ? el : (el.html ? el.html() : '')));
    return $;
  }

  const _ = {
    isEmpty(v) {
      if (v == null) return true;
      if (typeof v === 'string' || Array.isArray(v)) return v.length === 0;
      if (typeof v === 'object') return Object.keys(v).length === 0;
      return false;
    },
    isArray: Array.isArray,
    isString: (v) => typeof v === 'string',
    isNumber: (v) => typeof v === 'number',
    isObject: (v) => v !== null && (typeof v === 'object' || typeof v === 'function'),
    isUndefined: (v) => v === undefined,
    isNull: (v) => v === null,
    each(col, fn) {
      if (!col) return col;
      if (Array.isArray(col)) { for (let i = 0; i < col.length; i++) if (fn(col[i], i, col) === false) break; }
      else if (typeof col === 'object') { for (const k of Object.keys(col)) if (fn(col[k], k, col) === false) break; }
      return col;
    },
    forEach(col, fn) { return _.each(col, fn); },
    map(col, fn) {
      const out = [];
      _.each(col, (v, k) => out.push(typeof fn === 'string' ? (v && v[fn]) : fn(v, k, col)));
      return out;
    },
    filter(col, fn) {
      const out = [];
      _.each(col, (v, k) => { if (fn(v, k, col)) out.push(v); });
      return out;
    },
    find(col, fn) {
      let res;
      _.each(col, (v, k) => { if (fn(v, k, col)) { res = v; return false; } });
      return res;
    },
    uniq(arr) { return Array.from(new Set(arr || [])); },
    uniqBy(arr, iter) {
      const seen = new Set();
      const out = [];
      for (const item of (arr || [])) {
        const key = typeof iter === 'function' ? iter(item) : (item && item[iter]);
        if (!seen.has(key)) { seen.add(key); out.push(item); }
      }
      return out;
    },
    compact(arr) { return (arr || []).filter(Boolean); },
    flatten(arr) { return (arr || []).reduce((a, b) => a.concat(b), []); },
    chunk(arr, size) {
      const n = Math.max(1, size || 1);
      const out = [];
      for (let i = 0; i < (arr || []).length; i += n) out.push(arr.slice(i, i + n));
      return out;
    },
    trim(s, chars) {
      const str = String(s ?? '');
      if (!chars) return str.trim();
      const re = new RegExp('^[' + chars.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + ']+|[' + chars.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + ']+$', 'g');
      return str.replace(re, '');
    },
    get(obj, path, defVal) {
      const parts = Array.isArray(path) ? path : String(path || '').replace(/\[(\d+)\]/g, '.$1').split('.').filter(Boolean);
      let cur = obj;
      for (const p of parts) {
        if (cur == null) return defVal;
        cur = cur[p];
      }
      return cur === undefined ? defVal : cur;
    },
    merge(target, ...sources) { return Object.assign(target || {}, ...sources); },
    cloneDeep(v) { try { return JSON.parse(JSON.stringify(v)); } catch (e) { return v; } },
    keys: (o) => (o && typeof o === 'object' ? Object.keys(o) : []),
    values: (o) => (o && typeof o === 'object' ? Object.values(o) : []),
    includes: (c, v) => (typeof c === 'string' || Array.isArray(c) ? c.includes(v) : false),
    startsWith: (s, p) => String(s ?? '').startsWith(String(p ?? '')),
    endsWith: (s, p) => String(s ?? '').endsWith(String(p ?? '')),
    random(a, b) {
      const min = b === undefined ? 0 : a;
      const max = b === undefined ? (a || 1) : b;
      return Math.floor(Math.random() * (max - min + 1)) + min;
    },
    sample(arr) { return arr && arr.length ? arr[Math.floor(Math.random() * arr.length)] : undefined; },
    range(start, end, step) {
      if (end === undefined) { end = start; start = 0; }
      step = step || (start < end ? 1 : -1);
      const out = [];
      for (let i = start; step > 0 ? i < end : i > end; i += step) out.push(i);
      return out;
    },
  };

  globalThis.CryptoJS = globalThis.CryptoJS || Crypto;
  globalThis.__builtin_cat = { Crypto, CryptoJS: Crypto, load, cheerio: { load }, _, default: { Crypto, load, _ } };
  globalThis.__builtin_crypto = Object.assign({ default: Crypto }, Crypto);
  globalThis.__builtin_lodash = Object.assign({ default: _ }, _);
  globalThis.__builtin_cheerio = { load, default: { load } };
})();
`
