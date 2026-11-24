def pad(data_to_pad, block_size, style="pkcs7"):
    raw = bytes(data_to_pad)
    padding_len = block_size - (len(raw) % block_size)
    if style == "pkcs7":
        return raw + bytes([padding_len]) * padding_len
    if style == "x923":
        return raw + b"\x00" * (padding_len - 1) + bytes([padding_len])
    if style == "iso7816":
        return raw + b"\x80" + b"\x00" * (padding_len - 1)
    raise ValueError(f"Unknown padding style: {style}")


def unpad(padded_data, block_size, style="pkcs7"):
    raw = bytes(padded_data)
    if not raw or len(raw) % block_size != 0:
        raise ValueError("Data is not padded to block size")
    if style == "pkcs7":
        padding_len = raw[-1]
        if padding_len < 1 or padding_len > block_size:
            raise ValueError("Padding is incorrect")
        if raw[-padding_len:] != bytes([padding_len]) * padding_len:
            raise ValueError("PKCS#7 padding is incorrect")
        return raw[:-padding_len]
    if style == "x923":
        padding_len = raw[-1]
        if padding_len < 1 or padding_len > block_size:
            raise ValueError("Padding is incorrect")
        return raw[:-padding_len]
    if style == "iso7816":
        idx = raw.rfind(b"\x80")
        if idx < 0:
            raise ValueError("ISO7816 padding is incorrect")
        return raw[:idx]
    raise ValueError(f"Unknown padding style: {style}")
