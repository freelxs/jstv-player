MODE_ECB = 1
MODE_CBC = 2
MODE_CFB = 3
MODE_OFB = 5
MODE_CTR = 6
block_size = 16

_SBOX = [
    0x63,0x7c,0x77,0x7b,0xf2,0x6b,0x6f,0xc5,0x30,0x01,0x67,0x2b,0xfe,0xd7,0xab,0x76,
    0xca,0x82,0xc9,0x7d,0xfa,0x59,0x47,0xf0,0xad,0xd4,0xa2,0xaf,0x9c,0xa4,0x72,0xc0,
    0xb7,0xfd,0x93,0x26,0x36,0x3f,0xf7,0xcc,0x34,0xa5,0xe5,0xf1,0x71,0xd8,0x31,0x15,
    0x04,0xc7,0x23,0xc3,0x18,0x96,0x05,0x9a,0x07,0x12,0x80,0xe2,0xeb,0x27,0xb2,0x75,
    0x09,0x83,0x2c,0x1a,0x1b,0x6e,0x5a,0xa0,0x52,0x3b,0xd6,0xb3,0x29,0xe3,0x2f,0x84,
    0x53,0xd1,0x00,0xed,0x20,0xfc,0xb1,0x5b,0x6a,0xcb,0xbe,0x39,0x4a,0x4c,0x58,0xcf,
    0xd0,0xef,0xaa,0xfb,0x43,0x4d,0x33,0x85,0x45,0xf9,0x02,0x7f,0x50,0x3c,0x9f,0xa8,
    0x51,0xa3,0x40,0x8f,0x92,0x9d,0x38,0xf5,0xbc,0xb6,0xda,0x21,0x10,0xff,0xf3,0xd2,
    0xcd,0x0c,0x13,0xec,0x5f,0x97,0x44,0x17,0xc4,0xa7,0x7e,0x3d,0x64,0x5d,0x19,0x73,
    0x60,0x81,0x4f,0xdc,0x22,0x2a,0x90,0x88,0x46,0xee,0xb8,0x14,0xde,0x5e,0x0b,0xdb,
    0xe0,0x32,0x3a,0x0a,0x49,0x06,0x24,0x5c,0xc2,0xd3,0xac,0x62,0x91,0x95,0xe4,0x79,
    0xe7,0xc8,0x37,0x6d,0x8d,0xd5,0x4e,0xa9,0x6c,0x56,0xf4,0xea,0x65,0x7a,0xae,0x08,
    0xba,0x78,0x25,0x2e,0x1c,0xa6,0xb4,0xc6,0xe8,0xdd,0x74,0x1f,0x4b,0xbd,0x8b,0x8a,
    0x70,0x3e,0xb5,0x66,0x48,0x03,0xf6,0x0e,0x61,0x35,0x57,0xb9,0x86,0xc1,0x1d,0x9e,
    0xe1,0xf8,0x98,0x11,0x69,0xd9,0x8e,0x94,0x9b,0x1e,0x87,0xe9,0xce,0x55,0x28,0xdf,
    0x8c,0xa1,0x89,0x0d,0xbf,0xe6,0x42,0x68,0x41,0x99,0x2d,0x0f,0xb0,0x54,0xbb,0x16,
]
_INV_SBOX = [0] * 256
for _i, _v in enumerate(_SBOX):
    _INV_SBOX[_v] = _i
_RCON = [0x00, 0x01, 0x02, 0x04, 0x08, 0x10, 0x20, 0x40, 0x80, 0x1b, 0x36]


def _xtime(a):
    return ((a << 1) ^ 0x1b) & 0xff if a & 0x80 else (a << 1)


def _gmul(a, b):
    p = 0
    for _ in range(8):
        if b & 1:
            p ^= a
        a = _xtime(a)
        b >>= 1
    return p


def _expand_key(key):
    nk = len(key) // 4
    nr = {4: 10, 6: 12, 8: 14}.get(nk)
    if nr is None:
        raise ValueError("AES key must be 16, 24, or 32 bytes")
    w = [list(key[i : i + 4]) for i in range(0, len(key), 4)]
    for i in range(nk, 4 * (nr + 1)):
        temp = list(w[i - 1])
        if i % nk == 0:
            temp = [_SBOX[temp[(j + 1) % 4]] for j in range(4)]
            temp[0] ^= _RCON[i // nk]
        elif nk > 6 and i % nk == 4:
            temp = [_SBOX[b] for b in temp]
        w.append([w[i - nk][j] ^ temp[j] for j in range(4)])
    return [sum(w[r * 4 : (r + 1) * 4], []) for r in range(nr + 1)]


def _encrypt_block(block, round_keys):
    s = [block[i] ^ round_keys[0][i] for i in range(16)]
    nr = len(round_keys) - 1
    for r in range(1, nr + 1):
        s = [_SBOX[b] for b in s]
        s = [
            s[0], s[5], s[10], s[15],
            s[4], s[9], s[14], s[3],
            s[8], s[13], s[2], s[7],
            s[12], s[1], s[6], s[11],
        ]
        if r < nr:
            ns = [0] * 16
            for c in range(4):
                i = c * 4
                a0, a1, a2, a3 = s[i], s[i + 1], s[i + 2], s[i + 3]
                t = a0 ^ a1 ^ a2 ^ a3
                ns[i] = a0 ^ t ^ _xtime(a0 ^ a1)
                ns[i + 1] = a1 ^ t ^ _xtime(a1 ^ a2)
                ns[i + 2] = a2 ^ t ^ _xtime(a2 ^ a3)
                ns[i + 3] = a3 ^ t ^ _xtime(a3 ^ a0)
            s = ns
        rk = round_keys[r]
        s = [s[i] ^ rk[i] for i in range(16)]
    return bytes(s)


def _decrypt_block(block, round_keys):
    nr = len(round_keys) - 1
    s = [block[i] ^ round_keys[nr][i] for i in range(16)]
    for r in range(nr - 1, -1, -1):
        s = [
            s[0], s[13], s[10], s[7],
            s[4], s[1], s[14], s[11],
            s[8], s[5], s[2], s[15],
            s[12], s[9], s[6], s[3],
        ]
        s = [_INV_SBOX[b] for b in s]
        rk = round_keys[r]
        s = [s[i] ^ rk[i] for i in range(16)]
        if r > 0:
            ns = [0] * 16
            for c in range(4):
                i = c * 4
                a0, a1, a2, a3 = s[i], s[i + 1], s[i + 2], s[i + 3]
                ns[i] = _gmul(a0, 14) ^ _gmul(a1, 11) ^ _gmul(a2, 13) ^ _gmul(a3, 9)
                ns[i + 1] = _gmul(a0, 9) ^ _gmul(a1, 14) ^ _gmul(a2, 11) ^ _gmul(a3, 13)
                ns[i + 2] = _gmul(a0, 13) ^ _gmul(a1, 9) ^ _gmul(a2, 14) ^ _gmul(a3, 11)
                ns[i + 3] = _gmul(a0, 11) ^ _gmul(a1, 13) ^ _gmul(a2, 9) ^ _gmul(a3, 14)
            s = ns
    return bytes(s)


class _AESCipher:
    def __init__(self, key, mode, iv=None, nonce=None, initial_value=0):
        self._rk = _expand_key(bytes(key))
        self.mode = mode
        self.iv = bytes(iv) if iv is not None else b"\x00" * 16
        if len(self.iv) != 16:
            self.iv = (self.iv + b"\x00" * 16)[:16]
        self._nonce = bytes(nonce) if nonce is not None else b""
        self._counter = int.from_bytes(initial_value, "big") if isinstance(initial_value, (bytes, bytearray)) else int(initial_value)

    def encrypt(self, data):
        raw = bytes(data)
        if self.mode == MODE_ECB:
            if len(raw) % 16 != 0:
                raise ValueError("Data must be padded to 16 byte boundary in ECB mode")
            return b"".join(_encrypt_block(raw[i : i + 16], self._rk) for i in range(0, len(raw), 16))
        if self.mode == MODE_CBC:
            if len(raw) % 16 != 0:
                raise ValueError("Data must be padded to 16 byte boundary in CBC mode")
            out = bytearray()
            prev = self.iv
            for i in range(0, len(raw), 16):
                blk = bytes(raw[i + j] ^ prev[j] for j in range(16))
                enc = _encrypt_block(blk, self._rk)
                out.extend(enc)
                prev = enc
            self.iv = prev
            return bytes(out)
        if self.mode in (MODE_OFB, MODE_CFB):
            out = bytearray()
            prev = self.iv
            for i in range(0, len(raw), 16):
                ks = _encrypt_block(prev, self._rk)
                chunk = raw[i : i + 16]
                enc = bytes(chunk[j] ^ ks[j] for j in range(len(chunk)))
                out.extend(enc)
                prev = ks if self.mode == MODE_OFB else (enc + b"\x00" * 16)[:16]
            self.iv = prev
            return bytes(out)
        if self.mode == MODE_CTR:
            out = bytearray()
            ctr_len = 16 - len(self._nonce)
            for i in range(0, len(raw), 16):
                ctr_blk = self._nonce + self._counter.to_bytes(ctr_len, "big")
                self._counter = (self._counter + 1) % (1 << (8 * ctr_len))
                ks = _encrypt_block(ctr_blk, self._rk)
                chunk = raw[i : i + 16]
                out.extend(chunk[j] ^ ks[j] for j in range(len(chunk)))
            return bytes(out)
        raise ValueError(f"Unsupported AES mode: {self.mode}")

    def decrypt(self, data):
        raw = bytes(data)
        if self.mode == MODE_ECB:
            if len(raw) % 16 != 0:
                raise ValueError("Data must be aligned to 16 byte boundary in ECB mode")
            return b"".join(_decrypt_block(raw[i : i + 16], self._rk) for i in range(0, len(raw), 16))
        if self.mode == MODE_CBC:
            if len(raw) % 16 != 0:
                raise ValueError("Data must be aligned to 16 byte boundary in CBC mode")
            out = bytearray()
            prev = self.iv
            for i in range(0, len(raw), 16):
                chunk = raw[i : i + 16]
                dec = _decrypt_block(chunk, self._rk)
                out.extend(dec[j] ^ prev[j] for j in range(16))
                prev = chunk
            self.iv = prev
            return bytes(out)
        if self.mode == MODE_CFB:
            out = bytearray()
            prev = self.iv
            for i in range(0, len(raw), 16):
                ks = _encrypt_block(prev, self._rk)
                chunk = raw[i : i + 16]
                out.extend(chunk[j] ^ ks[j] for j in range(len(chunk)))
                prev = (chunk + b"\x00" * 16)[:16]
            self.iv = prev
            return bytes(out)
        if self.mode in (MODE_OFB, MODE_CTR):
            return self.encrypt(raw)
        raise ValueError(f"Unsupported AES mode: {self.mode}")


def new(key, mode, *args, **kwargs):
    iv = kwargs.get("iv", kwargs.get("IV"))
    if iv is None and args:
        iv = args[0]
    return _AESCipher(key, mode, iv=iv, nonce=kwargs.get("nonce"), initial_value=kwargs.get("initial_value", 0))
