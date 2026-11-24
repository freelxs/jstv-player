import hashlib

def new(data=b""):
    return hashlib.sha256(data)
