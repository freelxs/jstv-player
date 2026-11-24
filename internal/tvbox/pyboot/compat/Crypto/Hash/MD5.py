import hashlib

def new(data=b""):
    return hashlib.md5(data)
