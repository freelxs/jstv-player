import hashlib

def new(data=b""):
    return hashlib.sha1(data)
